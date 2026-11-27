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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// capScopes are the link tokens of the fake Core: pcx_<who>_<what> acts for
// <who>@example.com with one scope.
var capScopes = map[string]string{
	"search": core.ScopeSearch, "day": core.ScopeDay, "widget": core.ScopeWidget, "capture": core.ScopeCapture,
	"read": "watching:read", "requests": "requests:write",
}

// capLibrary is Ana's Jellyfin (token tokA): a film she watched, a show she is
// in the middle of, and what Continue watching, Next up and Latest answer.
func capLibrary() map[string][]map[string]any {
	ticks := func(sec int) int64 { return int64(sec) * 10_000_000 }
	ago := func(h int) string {
		return time.Now().Add(-time.Duration(h) * time.Hour).UTC().Format("2006-01-02T15:04:05.0000000Z")
	}
	pct := 60.0
	return map[string][]map[string]any{
		"Movie": {
			{"Id": "film1", "Name": "Sintel", "Type": "Movie", "ProductionYear": 2010, "RunTimeTicks": ticks(888), "UserData": map[string]any{"Played": true}},
			{"Id": "film2", "Name": "Sintel Shorts", "Type": "Movie", "ProductionYear": 2011, "RunTimeTicks": ticks(5400), "UserData": map[string]any{"Played": false, "PlaybackPositionTicks": ticks(1800), "PlayedPercentage": 33.4}},
		},
		"Series": {
			{"Id": "sev", "Name": "Severance", "Type": "Series", "ProductionYear": 2022, "Status": "Continuing", "RecursiveItemCount": 19, "UserData": map[string]any{"Played": false, "UnplayedItemCount": 10}},
		},
		"Episode": {
			{"Id": "ep205", "Name": "Hello, Ms. Cobel", "Type": "Episode", "SeriesId": "sev", "SeriesName": "Severance", "ParentIndexNumber": 2, "IndexNumber": 5, "ProductionYear": 2025,
				"RunTimeTicks": ticks(3000), "UserData": map[string]any{"PlaybackPositionTicks": ticks(1800), "PlayedPercentage": pct}},
		},
		"Person": {
			{"Id": "p1", "Name": "Sintel Stone", "Type": "Person"},
		},
		"resume": {
			{"Id": "film3", "Name": "Big Buck Bunny", "Type": "Movie", "RunTimeTicks": ticks(600), "UserData": map[string]any{"PlaybackPositionTicks": ticks(300), "LastPlayedDate": ago(48)}},
			{"Id": "ep205", "Name": "Hello, Ms. Cobel", "Type": "Episode", "SeriesId": "sev", "SeriesName": "Severance", "ParentIndexNumber": 2, "IndexNumber": 5,
				"RunTimeTicks": ticks(3000), "UserData": map[string]any{"PlaybackPositionTicks": ticks(1620), "LastPlayedDate": ago(20)}},
		},
		"nextup": {
			{"Id": "ep206", "Name": "Woe's Hollow", "Type": "Episode", "SeriesId": "sev", "SeriesName": "Severance", "ParentIndexNumber": 2, "IndexNumber": 6},
			{"Id": "pio102", "Name": "The Signal", "Type": "Episode", "SeriesId": "pio", "SeriesName": "Pioneer One", "ParentIndexNumber": 1, "IndexNumber": 2},
		},
		"latestMovie": {
			{"Id": "film4", "Name": "Elephants Dream", "Type": "Movie", "DateCreated": ago(5)},
			{"Id": "film3", "Name": "Big Buck Bunny", "Type": "Movie", "DateCreated": ago(1)},
		},
		"latestEpisode": {
			{"Id": "bon", "Name": "Bonanza", "Type": "Series", "DateCreated": ago(10), "UserData": map[string]any{"UnplayedItemCount": 3}},
			{"Id": "pio", "Name": "Pioneer One", "Type": "Series", "DateCreated": ago(2)},
		},
	}
}

// capJellyfin answers the library calls for Ana's token only; tokC is a
// sign-in Jellyfin no longer accepts. calls counts what reached it.
func capJellyfin(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	lib := capLibrary()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		h := r.Header.Get("Authorization")
		switch {
		case strings.Contains(h, `Token="tokC"`):
			w.WriteHeader(http.StatusUnauthorized)
			return
		case !strings.Contains(h, `Token="tokA"`):
			io.WriteString(w, `{"Items":[]}`)
			return
		}
		q := r.URL.Query()
		var items []map[string]any
		switch r.URL.Path {
		case "/Items":
			term := strings.ToLower(q.Get("SearchTerm"))
			for _, it := range lib[q.Get("IncludeItemTypes")] {
				if term != "" && strings.Contains(strings.ToLower(it["Name"].(string)), term) {
					items = append(items, it)
				}
			}
		case "/UserItems/Resume":
			items = lib["resume"]
		case "/Shows/NextUp":
			items = lib["nextup"]
		case "/Items/Latest":
			_ = json.NewEncoder(w).Encode(lib["latest"+q.Get("IncludeItemTypes")])
			return
		}
		if items == nil {
			items = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": items, "TotalRecordCount": len(items)})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

type capWorld struct {
	s        *Server
	h        http.Handler
	jf       *httptest.Server
	calls    *atomic.Int64
	manifest func() map[string]any
	ana      person // tokA: a working Jellyfin account
	ben      person // Jellyfin switched off
	carl     person // a sign-in Jellyfin no longer accepts
}

// capServer is Watch behind a fake Core that knows the capScopes tokens and
// keeps the manifest the app registers with, on capJellyfin.
func capServer(t *testing.T) *capWorld {
	t.Helper()
	var mu sync.Mutex
	var manifest map[string]any
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/register":
			mu.Lock()
			manifest = map[string]any{}
			_ = json.NewDecoder(r.Body).Decode(&manifest)
			mu.Unlock()
			io.WriteString(w, `{"token":"pca_test"}`)
		case "/api/v1/auth/introspect":
			var in struct {
				Token string `json:"token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
			if len(parts) != 2 || capScopes[parts[1]] == "" {
				io.WriteString(w, `{"active":false}`)
				return
			}
			email := parts[0] + "@example.com"
			fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"watch","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"search","name":"Pocket Search"}}`,
				capScopes[parts[1]], email, email, parts[0])
		default:
			io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(coreSrv.Close)
	jf, calls := capJellyfin(t)
	cfg := testConfig(t.TempDir())
	cfg.JellyfinURL = jf.URL
	cfg.CoreURL = coreSrv.URL
	st := openTestStore(t, cfg)
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := s.loadSecretKey(); err != nil {
		t.Fatal(err)
	}
	cw := &capWorld{s: s, h: s.Router(), jf: jf, calls: calls, manifest: func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return manifest
	}}
	cw.ana = addPerson(t, s, "ana@example.com", "tokA")
	cw.ben = addPerson(t, s, "ben@example.com", "tokB")
	if err := s.store.SaveConnection(context.Background(), &store.Connection{UserID: cw.ben.u.ID, Kind: "off"}); err != nil {
		t.Fatal(err)
	}
	cw.carl = addPerson(t, s, "carl@example.com", "tokC")
	return cw
}

func linkCall(t *testing.T, h http.Handler, tok, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// pocketGet calls a capability route as a person (a session) or with a link token.
func pocketGet(t *testing.T, h http.Handler, who any, path string, out any) int {
	t.Helper()
	var w *httptest.ResponseRecorder
	switch v := who.(type) {
	case person:
		w = call(t, h, v, "GET", "/api/v1/pocket/"+path, "")
	case string:
		w = linkCall(t, h, v, "GET", "/api/v1/pocket/"+path)
	}
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("%s: %v (%s)", path, err, w.Body)
		}
	}
	return w.Code
}

func pocketFind(t *testing.T, h http.Handler, who any, q string) []core.SearchItem {
	t.Helper()
	var res core.SearchResults
	if code := pocketGet(t, h, who, "search?q="+q, &res); code != 200 || res.Items == nil {
		t.Fatalf("search %s = %d %+v", q, code, res)
	}
	return res.Items
}

func pocketDayOf(t *testing.T, h http.Handler, who any, date, tz string) core.Day {
	t.Helper()
	path := "day/" + date
	if tz != "" {
		path += "?tz=" + tz
	}
	var d core.Day
	if code := pocketGet(t, h, who, path, &d); code != 200 {
		t.Fatalf("day %s = %d", path, code)
	}
	return d
}

func pocketWidgetOf(t *testing.T, h http.Handler, who any) core.WidgetData {
	t.Helper()
	var d core.WidgetData
	if code := pocketGet(t, h, who, "widget", &d); code != 200 {
		t.Fatalf("widget = %d", code)
	}
	return d
}

func found(items []core.SearchItem) string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID+" "+it.Title)
	}
	return strings.Join(out, " | ")
}

// Pocket Search finds films, shows and episodes in the person's own library,
// best match first; never people, and nothing for someone without a working
// Jellyfin account.
func TestPocketSearch(t *testing.T) {
	cw := capServer(t)
	h := cw.h

	got := pocketFind(t, h, cw.ana, "sintel")
	if found(got) != "film:film1 Sintel | film:film2 Sintel Shorts" {
		t.Fatalf("sintel = %s", found(got))
	}
	if it := got[0]; it.Kind != "film" || it.URL != "/title/film1" || it.Snippet != "2010 · 15m · Watched" {
		t.Errorf("Sintel = %+v", it)
	}
	if it := got[1]; it.Snippet != "2011 · 1h 30m · 33% watched" {
		t.Errorf("Sintel Shorts = %+v", it)
	}
	if got = pocketFind(t, h, cw.ana, "severance"); len(got) != 1 || got[0].ID != "series:sev" || got[0].Kind != "series" || got[0].Snippet != "2022– · 19 episodes · 10 episodes left" || got[0].URL != "/title/sev" {
		t.Errorf("severance = %+v", got)
	}
	if got = pocketFind(t, h, cw.ana, "cobel"); len(got) != 1 || got[0].ID != "episode:ep205" || got[0].Kind != "episode" || got[0].Snippet != "Severance · S02E05 · 2025 · 50m · 60% watched" || got[0].URL != "/title/ep205" {
		t.Errorf("cobel = %+v", got)
	}
	if got = pocketFind(t, h, cw.ana, "sintel&limit=1"); len(got) != 1 {
		t.Errorf("limit 1 = %s", found(got))
	}
	// no letter or digit: nothing, and Jellyfin is not asked
	before := cw.calls.Load()
	for _, q := range []string{"%3F%3F", "*", "%20-%20"} {
		if got = pocketFind(t, h, cw.ana, q); len(got) != 0 {
			t.Errorf("%q = %s", q, found(got))
		}
	}
	if cw.calls.Load() != before {
		t.Error("a search without words asked Jellyfin")
	}
	// Jellyfin switched off, or a sign-in it no longer takes: an empty list, not an error
	for _, p := range []person{cw.ben, cw.carl} {
		if got = pocketFind(t, h, p, "sintel"); len(got) != 0 {
			t.Errorf("%s found %s", p.u.Email, found(got))
		}
	}
	// GET /search still answers as before, people included
	var old map[string][]Title
	if r := call(t, h, cw.ana, "GET", "/api/v1/search?q=sintel", ""); r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &old) != nil || len(old["movies"]) != 2 || len(old["people"]) != 1 || old["series"] == nil || old["episodes"] == nil {
		t.Errorf("GET /search = %d %s", r.Code, r.Body)
	}
}

// Pocket Day gets one item per film or episode of the day from the history
// kept here, with the minutes, the show and number, and whether it was finished.
func TestPocketDay(t *testing.T) {
	cw := capServer(t)
	s, h := cw.s, cw.h
	ctx := context.Background()
	at := func(hh, mm int) string {
		return time.Date(2026, 11, 20, hh, mm, 0, 0, time.Local).UTC().Format(time.RFC3339Nano)
	}
	for _, w := range []store.Watch{
		{ItemID: "film1", Kind: "movie", Title: "Sintel", Year: 2010, WatchedAt: at(10, 0), Seconds: 900, Completed: true},
		{ItemID: "ep205", Kind: "episode", Title: "Hello, Ms. Cobel", SeriesID: "sev", Series: "Severance", Season: 2, Episode: 5, WatchedAt: at(20, 0), Seconds: 3000, Completed: true},
		{ItemID: "ep206", Kind: "episode", Title: "Woe's Hollow", SeriesID: "sev", Series: "Severance", Season: 2, Episode: 6, WatchedAt: at(21, 30), Seconds: 1200},
		{ItemID: "film4", Kind: "movie", Title: "Elephants Dream", WatchedAt: time.Date(2026, 11, 21, 9, 0, 0, 0, time.Local).UTC().Format(time.RFC3339Nano), Seconds: 600, Completed: true},
	} {
		w.Source = "pocket"
		if _, err := s.store.UpsertWatch(ctx, cw.ana.u.ID, &w); err != nil {
			t.Fatal(err)
		}
	}
	d := pocketDayOf(t, h, cw.ana, "2026-11-20", "")
	if d.Date != "2026-11-20" || d.Summary != "Watched 2 episodes of Severance and a film" || len(d.Items) != 3 {
		t.Fatalf("day = %+v", d)
	}
	if len(d.Metrics) != 1 || d.Metrics[0].Label != "Time" || d.Metrics[0].Value != "1h 25m" {
		t.Errorf("metrics = %+v", d.Metrics)
	}
	if it := d.Items[0]; it.ID != "film:film1" || it.Kind != "film" || it.Title != "Sintel" || it.Sub != "2010" || it.Minutes != 15 || !it.Done || it.Time != "10:00" || it.URL != "/title/film1" || it.Image != "/api/v1/img/film1/Primary" {
		t.Errorf("film = %+v", it)
	}
	if it := d.Items[1]; it.ID != "episode:ep205" || it.Kind != "episode" || it.Title != "Hello, Ms. Cobel" || it.Sub != "Severance · S02E05" || it.Minutes != 50 || !it.Done || it.Time != "20:00" || it.Image != "/api/v1/img/sev/Primary" {
		t.Errorf("episode = %+v", it)
	}
	if it := d.Items[2]; it.Sub != "Severance · S02E06" || it.Minutes != 20 || it.Done || it.Time != "21:30" {
		t.Errorf("unfinished episode = %+v", it)
	}
	if d = pocketDayOf(t, h, cw.ana, "2026-11-21", ""); d.Summary != "Watched Elephants Dream" || len(d.Items) != 1 {
		t.Errorf("a day of one film = %+v", d)
	}
	if d = pocketDayOf(t, h, cw.ana, "2026-11-22", ""); d.Summary != "" || len(d.Items) != 0 || d.Metrics != nil {
		t.Errorf("a day of nothing = %+v", d)
	}
	// someone else's day is empty, and Jellyfin is never asked
	if d = pocketDayOf(t, h, cw.ben, "2026-11-20", ""); d.Summary != "" || len(d.Items) != 0 {
		t.Errorf("Ben's day = %+v", d)
	}
	if n := cw.calls.Load(); n != 0 {
		t.Errorf("the day asked Jellyfin %d times", n)
	}
	if r := call(t, h, cw.ana, "GET", "/api/v1/pocket/day/2026-13-01", ""); r.Code != http.StatusBadRequest {
		t.Errorf("a bad date = %d", r.Code)
	}
}

// With a time zone the day is that zone's day, whatever the server's clock.
func TestPocketDayInTheCallersZone(t *testing.T) {
	cw := capServer(t)
	ctx := context.Background()
	for _, w := range []store.Watch{
		{ItemID: "film1", Kind: "movie", Title: "Sintel", WatchedAt: "2026-11-20T23:30:00Z", Seconds: 900, Completed: true},
		{ItemID: "ep205", Kind: "episode", Title: "Hello, Ms. Cobel", SeriesID: "sev", Series: "Severance", Season: 2, Episode: 5, WatchedAt: "2026-11-21T00:30:00.25Z", Seconds: 1800},
	} {
		if _, err := cw.s.store.UpsertWatch(ctx, cw.ana.u.ID, &w); err != nil {
			t.Fatal(err)
		}
	}
	day := func(date, tz string) string {
		d := pocketDayOf(t, cw.h, cw.ana, date, tz)
		var out []string
		for _, it := range d.Items {
			out = append(out, it.Time+" "+it.Title)
		}
		return strings.Join(out, ", ")
	}
	if got := day("2026-11-20", "UTC"); got != "23:30 Sintel" {
		t.Errorf("20th in UTC = %q", got)
	}
	if got := day("2026-11-21", "UTC"); got != "00:30 Hello, Ms. Cobel" {
		t.Errorf("21st in UTC = %q", got)
	}
	if got := day("2026-11-20", "Etc/GMT%2B12"); got != "11:30 Sintel, 12:30 Hello, Ms. Cobel" { // UTC-12
		t.Errorf("20th in UTC-12 = %q", got)
	}
	if got := day("2026-11-20", "Etc/GMT-14"); got != "" { // UTC+14: both on the 21st
		t.Errorf("20th in UTC+14 = %q", got)
	}
	if got := day("2026-11-21", "Pacific/Kiritimati"); got != "13:30 Sintel, 14:30 Hello, Ms. Cobel" {
		t.Errorf("21st in Kiritimati = %q", got)
	}
}

// Pocket Hub gets what the person is in the middle of and the next episode
// of their shows, most recently watched first, then what was added lately;
// each show once. The Jellyfin part is kept for a few minutes.
func TestPocketWidget(t *testing.T) {
	cw := capServer(t)
	s, h := cw.s, cw.h
	ctx := context.Background()
	// Pioneer One was last watched three days ago (here), so its next episode comes after the two resumes
	w := store.Watch{ItemID: "pio101", Kind: "episode", Title: "Pilot", SeriesID: "pio", Series: "Pioneer One", Season: 1, Episode: 1,
		WatchedAt: time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339Nano), Seconds: 2400, Completed: true, Source: "pocket"}
	if _, err := s.store.UpsertWatch(ctx, cw.ana.u.ID, &w); err != nil {
		t.Fatal(err)
	}
	d := pocketWidgetOf(t, h, cw.ana)
	var rows []string
	for _, it := range d.Items {
		rows = append(rows, it.ID+" "+it.Title+" ("+it.Detail+") "+it.URL)
	}
	want := []string{
		"episode:ep205 Severance (S02E05 · 23m left) /title/ep205",
		"film:film3 Big Buck Bunny (5m left) /title/film3",
		"episode:pio102 Pioneer One (Next: S01E02 · The Signal) /title/pio102",
		"film:film4 Elephants Dream (New film) /title/film4",
		"series:bon Bonanza (3 new episodes) /title/bon",
	}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
	if d.Line != "" || d.URL != "/" || len(d.Metrics) != 0 {
		t.Errorf("widget = %+v", d)
	}

	// kept for a few minutes: no second trip to Jellyfin, even while it is away
	n := cw.calls.Load()
	cw.jf.Close()
	if d = pocketWidgetOf(t, h, cw.ana); len(d.Items) != 5 || cw.calls.Load() != n {
		t.Errorf("second look = %d rows, %d more calls", len(d.Items), cw.calls.Load()-n)
	}
	// playing in Pocket Watch now: the line says so
	s.playMu.Lock()
	if s.plays == nil {
		s.plays = map[string]*playState{}
	}
	s.plays["p1"] = &playState{uid: cw.ana.u.ID, item: store.Watch{Kind: "episode", Title: "Hello, Ms. Cobel", Series: "Severance", Season: 2, Episode: 5}, lastAt: time.Now()}
	s.plays["p2"] = &playState{uid: cw.ben.u.ID, item: store.Watch{Kind: "movie", Title: "Sintel"}, lastAt: time.Now()}
	s.playMu.Unlock()
	if d = pocketWidgetOf(t, h, cw.ana); d.Line != "Playing now: Severance S02E05" {
		t.Errorf("playing = %+v", d)
	}
	s.playMu.Lock()
	s.plays["p1"].paused = true
	s.playMu.Unlock()
	if d = pocketWidgetOf(t, h, cw.ana); d.Line != "" {
		t.Errorf("paused = %q", d.Line)
	}

	// a person whose Jellyfin cannot be reached and who has nothing kept: the app cannot answer
	dan := addPerson(t, s, "dan@example.com", "tokD")
	if r := call(t, h, dan, "GET", "/api/v1/pocket/widget", ""); r.Code != http.StatusInternalServerError || strings.Contains(r.Body.String(), "127.0.0.1") {
		t.Errorf("Jellyfin away = %d %s", r.Code, r.Body)
	}
}

// No Jellyfin account, or one it no longer accepts: nothing to show, not an
// error. Someone else's rows never show up.
func TestPocketWidgetWithoutAnAccount(t *testing.T) {
	cw := capServer(t)
	if d := pocketWidgetOf(t, cw.h, cw.ana); len(d.Items) == 0 {
		t.Fatal("Ana has rows")
	}
	for _, p := range []person{cw.ben, cw.carl} {
		if d := pocketWidgetOf(t, cw.h, p); d.Line != "" || len(d.Items) != 0 {
			t.Errorf("%s = %+v", p.u.Email, d)
		}
	}
	// Ben plays something: the line alone
	cw.s.playMu.Lock()
	if cw.s.plays == nil {
		cw.s.plays = map[string]*playState{}
	}
	cw.s.plays["p"] = &playState{uid: cw.ben.u.ID, item: store.Watch{Kind: "movie", Title: "Sintel"}, lastAt: time.Now()}
	cw.s.playMu.Unlock()
	if d := pocketWidgetOf(t, cw.h, cw.ben); d.Line != "Playing now: Sintel" || len(d.Items) != 0 {
		t.Errorf("Ben playing = %+v", d)
	}
}

// A link token with one of the pocket:* scopes reaches its one route (and
// who it acts for) and nothing else; the older scopes do not open the new
// routes, and requests:write keeps GET /search to itself.
func TestPocketLinkScopes(t *testing.T) {
	cw := capServer(t)
	h := cw.h
	w := store.Watch{ItemID: "film1", Kind: "movie", Title: "Sintel", WatchedAt: "2026-11-20T12:00:00Z", Seconds: 900, Completed: true, Source: "pocket"}
	if _, err := cw.s.store.UpsertWatch(context.Background(), cw.ana.u.ID, &w); err != nil {
		t.Fatal(err)
	}
	const day = "/api/v1/pocket/day/2026-11-20"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{"pcx_ana_search", "GET", "/api/v1/pocket/search?q=sintel", 200},
		{"pcx_ana_search", "GET", "/api/v1/auth/me", 200},
		{"pcx_ana_search", "POST", "/api/v1/pocket/search?q=sintel", 405},
		{"pcx_ana_search", "GET", day, 403},
		{"pcx_ana_search", "GET", "/api/v1/pocket/widget", 403},
		{"pcx_ana_search", "GET", "/api/v1/search?q=sintel", 403},
		{"pcx_ana_search", "GET", "/api/v1/discover?q=sintel", 403},
		{"pcx_ana_search", "GET", "/api/v1/titles/film1", 403},
		{"pcx_ana_search", "POST", "/api/v1/requests", 403},
		{"pcx_ana_search", "POST", "/api/v1/watchlist", 403},
		{"pcx_ana_search", "POST", "/api/v1/play/film1", 403},
		{"pcx_ana_search", "GET", "/api/v1/img/abc123/Primary", 403},
		{"pcx_ana_day", "GET", day, 200},
		{"pcx_ana_day", "GET", day + "?tz=Europe/Sarajevo", 200},
		{"pcx_ana_day", "GET", "/api/v1/auth/me", 200},
		{"pcx_ana_day", "GET", "/api/v1/day/2026-11-20", 403},
		{"pcx_ana_day", "GET", "/api/v1/history", 403},
		{"pcx_ana_day", "GET", "/api/v1/stats", 403},
		{"pcx_ana_day", "DELETE", "/api/v1/history/1", 403},
		{"pcx_ana_day", "GET", "/api/v1/pocket/search?q=sintel", 403},
		{"pcx_ana_day", "GET", "/api/v1/pocket/widget", 403},
		{"pcx_ana_widget", "GET", "/api/v1/pocket/widget", 200},
		{"pcx_ana_widget", "GET", "/api/v1/auth/me", 200},
		{"pcx_ana_widget", "GET", "/api/v1/home", 403},
		{"pcx_ana_widget", "GET", "/api/v1/dashboard", 403},
		{"pcx_ana_widget", "POST", "/api/v1/cleanup/run", 403},
		{"pcx_ana_widget", "POST", "/api/v1/leaving/film1", 403},
		{"pcx_ana_widget", "POST", "/api/v1/titles/film1/played", 403},
		{"pcx_ana_widget", "GET", day, 403},
		{"pcx_ana_widget", "GET", "/api/v1/pocket/search?q=sintel", 403},
		// nothing can be kept here: the scope is not offered and there is no route
		{"pcx_ana_capture", "POST", "/api/v1/pocket/capture", 404},
		{"pcx_ana_capture", "GET", "/api/v1/pocket/search?q=sintel", 403},
		// the older scopes keep their own routes and do not open the new ones
		{"pcx_ana_read", "GET", "/api/v1/day/2026-11-20", 200},
		{"pcx_ana_read", "GET", day, 403},
		{"pcx_ana_read", "GET", "/api/v1/pocket/widget", 403},
		{"pcx_ana_read", "GET", "/api/v1/pocket/search?q=sintel", 403},
		{"pcx_ana_requests", "GET", "/api/v1/search?q=sintel", 200},
		{"pcx_ana_requests", "GET", "/api/v1/pocket/search?q=sintel", 403},
		{"pcx_ana_requests", "GET", "/api/v1/pocket/widget", 403},
		{"pcx_ana_requests", "GET", day, 403},
	} {
		if r := linkCall(t, h, c.tok, c.method, c.path); r.Code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, r.Code, c.want, r.Body)
		}
	}
	for _, c := range []struct {
		scope, method, path string
		want                bool
	}{
		{core.ScopeSearch, "GET", core.SearchPath, true},
		{core.ScopeSearch, "POST", core.SearchPath, false},
		{core.ScopeDay, "GET", core.DayPath + "2026-11-20", true},
		{core.ScopeDay, "PUT", core.DayPath + "2026-11-20", false},
		{core.ScopeWidget, "GET", core.WidgetPath, true},
		{core.ScopeWidget, "DELETE", core.WidgetPath, false},
		{core.ScopeCapture, "POST", core.CapturePath, false},
		{core.ScopeSearch, "GET", "/api/v1/search", false},
		{core.ScopeDay, "GET", "/api/v1/day/2026-11-20", false},
		{"watching:read", "GET", core.WidgetPath, false},
		{"requests:write", "GET", core.SearchPath, false},
	} {
		r, _ := http.NewRequest(c.method, c.path, nil)
		if got := linkAllows([]string{c.scope}, r); got != c.want {
			t.Errorf("linkAllows(%s, %s %s) = %v, want %v", c.scope, c.method, c.path, got, c.want)
		}
	}
	// the token answers for its person only
	if got := pocketFind(t, h, "pcx_ana_search", "sintel"); len(got) != 2 {
		t.Errorf("Ana through a link = %s", found(got))
	}
	if got := pocketFind(t, h, "pcx_ben_search", "sintel"); len(got) != 0 {
		t.Errorf("Ben through a link = %s", found(got))
	}
	if d := pocketDayOf(t, h, "pcx_ana_day", "2026-11-20", "UTC"); len(d.Items) != 1 || d.Items[0].Title != "Sintel" {
		t.Errorf("Ana's day through a link = %+v", d)
	}
	if d := pocketDayOf(t, h, "pcx_ben_day", "2026-11-20", "UTC"); len(d.Items) != 0 {
		t.Errorf("Ben's day through a link = %+v", d)
	}
	if d := pocketWidgetOf(t, h, "pcx_ana_widget"); len(d.Items) == 0 {
		t.Errorf("Ana's widget through a link = %+v", d)
	}
	if d := pocketWidgetOf(t, h, "pcx_ben_widget"); d.Line != "" || len(d.Items) != 0 {
		t.Errorf("Ben's widget through a link = %+v", d)
	}
	// reading through the links changed nothing
	if _, n, _ := cw.s.store.Watches(context.Background(), cw.ana.u.ID, store.WatchFilter{Limit: -1}); n != 1 {
		t.Errorf("history after the link calls = %d", n)
	}
	if l, _ := cw.s.store.Watchlist(context.Background(), cw.ana.u.ID); len(l) != 0 {
		t.Errorf("watchlist after the link calls = %v", l)
	}
}

// The manifest tells Core the shelf, what can be asked here and the scopes
// behind it; a quick line is not among them.
func TestManifestCapabilities(t *testing.T) {
	cw := capServer(t)
	cw.s.cfg.CoreRegistrationKey = "pcr_test"
	if err := cw.s.registerWithCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := cw.manifest()
	if m["slug"] != "watch" || m["category"] != "Media" {
		t.Fatalf("manifest: %v %v", m["slug"], m["category"])
	}
	search, _ := m["search"].(map[string]any)
	day, _ := m["day"].(map[string]any)
	widget, _ := m["widget"].(map[string]any)
	if search == nil || day == nil || widget == nil || m["capture"] != nil {
		t.Fatalf("capabilities: %v %v %v %v", search, day, widget, m["capture"])
	}
	if fmt.Sprint(search["bangs"]) != "[watch film series]" || fmt.Sprint(search["kinds"]) != "[film series episode]" || search["what"] == "" {
		t.Errorf("search: %v", search)
	}
	if day["label"] != "Watch" || day["one"] != "title" || day["many"] != "titles" || day["unit"] != "min" || day["what"] == "" {
		t.Errorf("day: %v", day)
	}
	if widget["title"] != "Continue watching" || widget["what"] == "" {
		t.Errorf("widget: %v", widget)
	}
	offered := map[string]bool{}
	for _, o := range m["offers"].([]any) {
		offered[o.(map[string]any)["scope"].(string)] = true
	}
	for _, sc := range []string{"watching:read", "requests:write", core.ScopeSearch, core.ScopeDay, core.ScopeWidget, core.ScopeMoveExport, core.ScopeMoveImport} {
		if !offered[sc] {
			t.Errorf("%s is not offered", sc)
		}
	}
	if offered[core.ScopeCapture] {
		t.Error("pocket:capture is offered")
	}
}

func TestDaySentence(t *testing.T) {
	ep := func(series string) store.Watch { return store.Watch{Kind: "episode", Series: series} }
	film := func(title string) store.Watch { return store.Watch{Kind: "movie", Title: title} }
	for _, c := range []struct {
		ws   []store.Watch
		want string
	}{
		{nil, ""},
		{[]store.Watch{film("Sintel")}, "Watched Sintel"},
		{[]store.Watch{film("Sintel"), film("Elephants Dream")}, "Watched 2 films"},
		{[]store.Watch{ep("Severance")}, "Watched an episode of Severance"},
		{[]store.Watch{ep("Severance"), film("Sintel"), ep("Severance")}, "Watched 2 episodes of Severance and a film"},
		{[]store.Watch{ep("Severance"), ep("Bonanza"), ep("Bonanza")}, "Watched an episode of Severance and 2 episodes of Bonanza"},
		{[]store.Watch{ep("A"), ep("B"), ep("C"), ep("C"), film("X"), film("Y")}, "Watched 4 episodes of 3 shows and 2 films"},
	} {
		if got := daySentence(c.ws); got != c.want {
			t.Errorf("daySentence(%v) = %q, want %q", c.ws, got, c.want)
		}
	}
}
