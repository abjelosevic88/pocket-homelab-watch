package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// moveServer starts Watch behind a fake Core whose pcx_<who>_<export|import|read>
// tokens are Pocket Move links acting for ana@ or ben@.
func moveServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/auth/introspect" {
			io.WriteString(w, `{}`)
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
		if len(parts) != 2 {
			io.WriteString(w, `{"active":false}`)
			return
		}
		scope := map[string]string{"export": core.ScopeMoveExport, "import": core.ScopeMoveImport, "read": "watching:read"}[parts[1]]
		email := parts[0] + "@example.com"
		fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"watch","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"move","name":"Pocket Move"}}`, scope, email, email, parts[0])
	}))
	t.Cleanup(coreSrv.Close)
	jf, _ := fakeJellyfin(t)
	cfg := testConfig(t.TempDir())
	cfg.JellyfinURL = jf.URL
	cfg.CoreURL = coreSrv.URL
	st := openTestStore(t, cfg)
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := s.loadSecretKey(); err != nil {
		t.Fatal(err)
	}
	return s, s.Router()
}

func moveCall(t *testing.T, h http.Handler, tok, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestMoveLinkScopes(t *testing.T) {
	_, h := moveServer(t)
	exp, imp, read := "pcx_ben_export", "pcx_ben_import", "pcx_ben_read"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{exp, "GET", core.PortablePath, 200},
		{exp, "GET", "/api/v1/export.json", 200},
		{exp, "POST", "/api/v1/import", 403},
		{exp, "GET", "/api/v1/settings", 403},
		{exp, "GET", "/api/v1/history", 403},
		{exp, "GET", "/api/v1/backup", 403},
		{imp, "GET", core.PortablePath, 200},
		{imp, "GET", "/api/v1/export.json", 403},
		{imp, "PUT", "/api/v1/settings", 403},
		{imp, "POST", "/api/v1/watchlist", 403},
		{read, "GET", core.PortablePath, 403},
		{read, "GET", "/api/v1/export.json", 403},
	} {
		if r := moveCall(t, h, c.tok, c.method, c.path, "{}"); r.Code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, r.Code, c.want, r.Body)
		}
	}
	var p core.Portable
	if err := json.Unmarshal(moveCall(t, h, exp, "GET", core.PortablePath, "").Body.Bytes(), &p); err != nil || len(p.Exports) != 1 || len(p.Imports) != 1 || p.Exports[0].Kind != core.KindPocket {
		t.Fatalf("portable: %+v %v", p, err)
	}
}

// Move brings Ana's history and watchlist to Ben: the import adds once, and
// nothing of one person shows up for the other.
func TestMoveImportAddsOnce(t *testing.T) {
	s, h := moveServer(t)
	ctx := context.Background()
	ana, ben := addPerson(t, s, "ana@example.com", "tokA"), addPerson(t, s, "ben@example.com", "tokB")
	w := store.Watch{ItemID: movieID, Kind: "movie", Title: "Sintel", WatchedAt: store.Now(), Seconds: 900, Completed: true, Source: "pocket"}
	if _, err := s.store.UpsertWatch(ctx, ana.u.ID, &w); err != nil {
		t.Fatal(err)
	}
	if err := s.store.AddToWatchlist(ctx, ana.u.ID, &store.WatchlistItem{MediaType: "movie", TMDBID: 45745, Title: "Big Buck Bunny"}); err != nil {
		t.Fatal(err)
	}

	r := moveCall(t, h, "pcx_ana_export", "GET", "/api/v1/export.json", "")
	export := r.Body.String()
	if r.Code != 200 || !strings.Contains(export, "Sintel") || !strings.Contains(export, "Big Buck Bunny") {
		t.Fatalf("ana's export: %d %s", r.Code, export)
	}
	if b := moveCall(t, h, "pcx_ben_export", "GET", "/api/v1/export.json", "").Body.String(); strings.Contains(b, "Sintel") || strings.Contains(b, "Bunny") {
		t.Fatalf("ben's export holds ana's: %s", b)
	}

	imp := func() map[string]float64 {
		r := moveCall(t, h, "pcx_ben_import", "POST", "/api/v1/import", export)
		if r.Code != 200 {
			t.Fatalf("import → %d %s", r.Code, r.Body)
		}
		var m map[string]float64
		_ = json.Unmarshal(r.Body.Bytes(), &m)
		return m
	}
	if first := imp(); first["watches"] != 1 || first["watchlist"] != 1 {
		t.Fatalf("first import: %v", first)
	}
	if second := imp(); second["watches"]+second["watchlist"] != 0 {
		t.Fatalf("second import added: %v", second)
	}
	if l, _ := s.store.Watchlist(ctx, ben.u.ID); len(l) != 1 {
		t.Fatalf("ben's watchlist: %v", l)
	}
	if _, n, _ := s.store.Watches(ctx, ana.u.ID, store.WatchFilter{Limit: -1}); n != 1 {
		t.Fatalf("ana's history changed: %d", n)
	}
	if _, n, _ := s.store.Watches(ctx, ben.u.ID, store.WatchFilter{Limit: -1}); n != 1 {
		t.Fatalf("ben's history: %d", n)
	}
}
