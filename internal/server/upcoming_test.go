package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

type upcomingOut struct {
	Items      []Upcoming `json:"items"`
	Configured bool       `json:"configured"`
	Problems   []string   `json:"problems"`
	Setup      struct {
		Missing  []string `json:"missing"`
		SetupURL string   `json:"setup_url"`
		Admin    string   `json:"admin"`
	} `json:"setup"`
}

func addAdmin(t *testing.T, s *Server, email, name string) person {
	t.Helper()
	ctx := context.Background()
	u := &store.User{Email: email, Name: name, Role: "admin", PasswordHash: "x"}
	if err := s.store.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	id, err := s.store.CreateSession(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return person{u: u, cookie: &http.Cookie{Name: sessionCookie, Value: id}}
}

func getUpcoming(t *testing.T, h http.Handler, p person) upcomingOut {
	t.Helper()
	r := call(t, h, p, "GET", "/api/v1/upcoming", "")
	if r.Code != http.StatusOK {
		t.Fatalf("upcoming: %d %s", r.Code, r.Body)
	}
	var out upcomingOut
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Without Sonarr and Radarr, Upcoming is a calm "not set up" (no errors), and
// says whom to ask; without Core, admins finish it under Settings → Services.
func TestUpcomingNotSetUp(t *testing.T) {
	forgetBoxServices()
	t.Cleanup(forgetBoxServices)
	s, h, _, _ := testServer(t)
	admin := addAdmin(t, s, "owner@example.com", "Aleks")
	ana := addPerson(t, s, "ana@example.com", "tokA")
	for _, p := range []person{admin, ana} {
		out := getUpcoming(t, h, p)
		if out.Configured || len(out.Problems) != 0 || len(out.Setup.Missing) != 2 || out.Setup.SetupURL != "" || out.Setup.Admin != "Aleks" {
			t.Fatalf("not set up: %+v", out)
		}
	}
	r := call(t, h, admin, "GET", "/api/v1/cleanup", "")
	var cl struct {
		Services map[string]bool `json:"services"`
		SetupURL string          `json:"setup_url"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &cl)
	if r.Code != http.StatusOK || cl.Services["radarr"] || cl.Services["sonarr"] || cl.SetupURL != "" {
		t.Fatalf("cleanup: %d %s", r.Code, r.Body)
	}
}

// With Core: a service Core doesn't have is "not set up" with a link to Core's
// Downloads settings; the one it has still fills the calendar.
func TestUpcomingPartlyFromCore(t *testing.T) {
	forgetBoxServices()
	t.Cleanup(forgetBoxServices)
	sonarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/calendar" || r.Header.Get("X-Api-Key") != "sk" {
			http.NotFound(w, r)
			return
		}
		at := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
		_, _ = io.WriteString(w, `[{"title":"Pilot","seasonNumber":1,"episodeNumber":1,"airDateUtc":"`+at+`","series":{"title":"Show","tvdbId":7}}]`)
	}))
	t.Cleanup(sonarr.Close)
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Type string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if r.URL.Path != "/api/v1/services/credential" {
			http.NotFound(w, r)
			return
		}
		if in.Type == "sonarr" {
			_, _ = io.WriteString(w, `{"url":"`+sonarr.URL+`","key":"sk"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"no such service","reason":"target_missing"}`)
	}))
	t.Cleanup(coreSrv.Close)

	jf, _ := fakeJellyfin(t)
	cfg := testConfig(t.TempDir())
	cfg.JellyfinURL, cfg.CoreURL, cfg.CorePublicURL = jf.URL, coreSrv.URL, "https://core.example.com"
	st := openTestStore(t, cfg)
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := s.loadSecretKey(); err != nil {
		t.Fatal(err)
	}
	h := s.Router()
	admin := addAdmin(t, s, "owner@example.com", "Aleks")
	r := call(t, h, admin, "GET", "/api/v1/upcoming?scope=all", "")
	var out upcomingOut
	_ = json.Unmarshal(r.Body.Bytes(), &out)
	if !out.Configured || len(out.Items) != 1 || len(out.Problems) != 0 || len(out.Setup.Missing) != 1 || out.Setup.Missing[0] != "radarr" ||
		out.Setup.SetupURL != "https://core.example.com/settings/system#downloads" {
		t.Fatalf("partly set up: %s", r.Body)
	}
}
