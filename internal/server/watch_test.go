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
	"time"

	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

const movieID = "0123456789abcdef0123456789abcdef"

// fakeJellyfin answers the calls playback makes and remembers whose token streamed.
func fakeJellyfin(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var streamed []string
	token := func(r *http.Request) string {
		h := r.Header.Get("Authorization")
		if i := strings.Index(h, `Token="`); i >= 0 {
			return strings.TrimSuffix(h[i+7:], `"`)
		}
		return ""
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/Items/"+movieID, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": movieID, "Name": "Sintel", "Type": "Movie", "ProductionYear": 2010, "RunTimeTicks": 900 * 10_000_000,
			"MediaSources": []map[string]any{{"Id": "ms1", "Container": "mp4", "RunTimeTicks": 900 * 10_000_000, "DefaultAudioStreamIndex": 1,
				"MediaStreams": []map[string]any{{"Index": 0, "Type": "Video", "Codec": "h264"}, {"Index": 1, "Type": "Audio", "Codec": "aac", "Language": "eng"},
					{"Index": 2, "Type": "Subtitle", "Codec": "subrip", "Language": "eng", "IsTextSubtitleStream": true, "IsExternal": true, "Path": "/media/movies/Sintel (2010)/Sintel (2010).en.srt"}}}}})
	})
	mux.HandleFunc("/Items/"+movieID+"/PlaybackInfo", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"PlaySessionId": "ps-" + r.URL.Query().Get("userId"), "MediaSources": []map[string]any{{"Id": "ms1", "Container": "mp4", "SupportsDirectPlay": true}}})
	})
	mux.HandleFunc("/MediaSegments/"+movieID, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"Items":[]}`) })
	mux.HandleFunc("/Videos/"+movieID+"/stream", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "" {
			t.Errorf("a token reached Jellyfin in the URL")
		}
		streamed = append(streamed, token(r)+"|"+r.Header.Get("Range"))
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "frames")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }) // reports, user data
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &streamed
}

type person struct {
	u      *store.User
	cookie *http.Cookie
}

func testServer(t *testing.T) (*Server, http.Handler, *httptest.Server, *[]string) {
	t.Helper()
	jf, streamed := fakeJellyfin(t)
	cfg := testConfig(t.TempDir())
	cfg.JellyfinURL = jf.URL
	st := openTestStore(t, cfg)
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := s.loadSecretKey(); err != nil {
		t.Fatal(err)
	}
	return s, s.Router(), jf, streamed
}

func addPerson(t *testing.T, s *Server, email, token string) person {
	t.Helper()
	ctx := context.Background()
	u := &store.User{Email: email, Name: email, Role: "member", PasswordHash: "x"}
	if err := s.store.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	sealed, _ := s.seal(token)
	if err := s.store.SaveConnection(ctx, &store.Connection{UserID: u.ID, Kind: "direct", Username: email, JFUserID: "user-" + email[:3], Secret: sealed, DeviceID: "dev-" + token}); err != nil {
		t.Fatal(err)
	}
	id, err := s.store.CreateSession(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return person{u: u, cookie: &http.Cookie{Name: sessionCookie, Value: id}}
}

func call(t *testing.T, h http.Handler, p person, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.AddCookie(p.cookie)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// History, watchlist and play sessions are strictly per person: another
// person's ids answer 404 and never show up in lists.
func TestEverythingIsPerPerson(t *testing.T) {
	s, h, _, _ := testServer(t)
	ctx := context.Background()
	ana, ben := addPerson(t, s, "ana@example.com", "tokA"), addPerson(t, s, "ben@example.com", "tokB")
	w := store.Watch{ItemID: movieID, Kind: "movie", Title: "Sintel", WatchedAt: store.Now(), Seconds: 900, Completed: true, Source: "pocket"}
	if _, err := s.store.UpsertWatch(ctx, ana.u.ID, &w); err != nil {
		t.Fatal(err)
	}
	wl := store.WatchlistItem{MediaType: "movie", TMDBID: 45745, Title: "Sintel"}
	if err := s.store.AddToWatchlist(ctx, ana.u.ID, &wl); err != nil {
		t.Fatal(err)
	}

	if r := call(t, h, ben, "GET", "/api/v1/history", ""); !strings.Contains(r.Body.String(), `"total":0`) {
		t.Fatalf("Ben sees history he doesn't have: %s", r.Body)
	}
	if r := call(t, h, ana, "GET", "/api/v1/history", ""); !strings.Contains(r.Body.String(), `"total":1`) {
		t.Fatalf("Ana's history: %s", r.Body)
	}
	for _, path := range []string{fmt.Sprintf("/api/v1/history/%d", w.ID), fmt.Sprintf("/api/v1/watchlist/%d", wl.ID)} {
		if r := call(t, h, ben, "DELETE", path, ""); r.Code != http.StatusNotFound {
			t.Fatalf("Ben deleting %s: %d, want 404", path, r.Code)
		}
	}
	if r := call(t, h, ben, "GET", "/api/v1/watchlist", ""); strings.TrimSpace(r.Body.String()) != "[]" {
		t.Fatalf("Ben's watchlist: %s", r.Body)
	}
	if r := call(t, h, ben, "GET", "/api/v1/day/"+store.Today(), ""); !strings.Contains(r.Body.String(), `"seconds":0`) {
		t.Fatalf("Ben's day: %s", r.Body)
	}

	// Ana plays; Ben can't report on (or end) her session
	r := call(t, h, ana, "POST", "/api/v1/play/"+movieID, `{"start":-1,"caps":{"mse":true}}`)
	if r.Code != http.StatusOK {
		t.Fatalf("play: %d %s", r.Code, r.Body)
	}
	var p struct {
		Session string `json:"session"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &p)
	if rr := call(t, h, ben, "POST", "/api/v1/play/"+movieID+"/report", `{"session":"`+p.Session+`","event":"stop","position":10}`); rr.Code != http.StatusNotFound {
		t.Fatalf("Ben reporting on Ana's session: %d, want 404", rr.Code)
	}
}

// The browser gets a Pocket Watch URL; the media proxy adds the person's own
// token (never in the URL) and passes byte ranges through.
func TestPlayStreamsWithThePersonsToken(t *testing.T) {
	s, h, _, streamed := testServer(t)
	ana := addPerson(t, s, "ana@example.com", "tokA")
	r := call(t, h, ana, "POST", "/api/v1/play/"+movieID, `{"start":-1,"caps":{"mse":true}}`)
	var p struct {
		URL       string `json:"url"`
		Method    string `json:"method"`
		Subtitles []struct {
			URL string `json:"url"`
		} `json:"subtitles"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &p); err != nil {
		t.Fatal(err, r.Body.String())
	}
	if p.Method != "DirectPlay" || !strings.HasPrefix(p.URL, "/api/v1/media/Videos/"+movieID+"/stream?") || strings.Contains(p.URL, "tokA") {
		t.Fatalf("play answer: %+v", p)
	}
	if len(p.Subtitles) != 1 || !strings.HasSuffix(p.Subtitles[0].URL, "/Subtitles/2/0/Stream.vtt") {
		t.Fatalf("subtitles: %+v", p.Subtitles)
	}
	req := httptest.NewRequest("GET", p.URL+"&api_key=injected", nil)
	req.AddCookie(ana.cookie)
	req.Header.Set("Range", "bytes=0-5")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusPartialContent || w.Body.String() != "frames" {
		t.Fatalf("stream: %d %q", w.Code, w.Body.String())
	}
	if len(*streamed) != 1 || (*streamed)[0] != "tokA|bytes=0-5" {
		t.Fatalf("Jellyfin saw %v", *streamed)
	}
	// only video paths go through
	req = httptest.NewRequest("GET", "/api/v1/media/Users", nil)
	req.AddCookie(ana.cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("proxying /Users: %d, want 404", w.Code)
	}
}

func TestCleanTranscodeURL(t *testing.T) {
	got := cleanTranscodeURL("/videos/abc/master.m3u8?DeviceId=d&ApiKey=secret&StartTimeTicks=100&VideoCodec=h264")
	if strings.Contains(got, "secret") || strings.Contains(got, "StartTimeTicks") || !strings.Contains(got, "VideoCodec=h264") {
		t.Fatalf("cleanTranscodeURL = %s", got)
	}
}

func TestLinkScopes(t *testing.T) {
	get := func(p string) *http.Request { return httptest.NewRequest("GET", p, nil) }
	if !linkAllows([]string{"watching:read"}, get("/api/v1/day/2026-10-24")) {
		t.Fatal("watching:read should read a day")
	}
	if linkAllows([]string{"watching:read"}, httptest.NewRequest("POST", "/api/v1/requests", nil)) {
		t.Fatal("watching:read must not request")
	}
	if linkAllows([]string{"watching:read", "requests:write"}, get("/api/v1/media/Videos/x/stream")) {
		t.Fatal("no link may stream")
	}
}

func TestDaySummary(t *testing.T) {
	ws := []store.Watch{{Kind: "movie", Title: "Sintel"}, {Kind: "episode", Series: "Bonanza"}, {Kind: "episode", Series: "Bonanza"}, {Kind: "episode", Series: "Pioneer One"}}
	if got := daySummary(ws); got != "Sintel, 2 episodes of Bonanza and an episode of Pioneer One" {
		t.Fatalf("daySummary = %q", got)
	}
}

// Fixing subtitle timing: the offset is per person; saving sends it to Pocket
// Subtitles as a nudge of that file and forgets the personal offset.
func TestSubtitleTiming(t *testing.T) {
	s, h, _, _ := testServer(t)
	ctx := context.Background()
	var nudged []string
	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer subs-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/api/v1/files/by-path" && strings.HasSuffix(r.URL.Query().Get("path"), "Sintel (2010).en.srt"):
			_, _ = io.WriteString(w, `{"id":7,"rel":"movies/Sintel (2010)/Sintel (2010).en.srt","pinned":false,"nudge_ms":0,"can_undo":false,"exists":true,"runs":[],"events":[]}`)
		case r.URL.Path == "/api/v1/files/7/nudge":
			b, _ := io.ReadAll(r.Body)
			nudged = append(nudged, string(b))
			_, _ = io.WriteString(w, `{"cues":3}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ps.Close()
	box, _ := s.store.GetSettings(ctx, 0)
	box.SubtitlesAPIURL, box.SubtitlesToken = ps.URL, "subs-token"
	if err := s.store.SaveBoxSettings(ctx, box); err != nil {
		t.Fatal(err)
	}
	ana, ben := addPerson(t, s, "ana@example.com", "tokA"), addPerson(t, s, "ben@example.com", "tokB")
	base := "/api/v1/play/" + movieID + "/subtitles/2"

	if r := call(t, h, ana, "GET", base, ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"file":true`) || !strings.Contains(r.Body.String(), `"file_id":7`) {
		t.Fatalf("timing: %d %s", r.Code, r.Body)
	}
	if r := call(t, h, ana, "PUT", base+"/offset", `{"ms":350}`); r.Code != 200 {
		t.Fatalf("offset: %d %s", r.Code, r.Body)
	}
	if got := s.store.SubtitleOffsets(ctx, ben.u.ID, movieID)[2]; got != 0 {
		t.Fatalf("Ben sees Ana's offset: %d", got)
	}
	r := call(t, h, ana, "POST", "/api/v1/play/"+movieID, `{"start":-1,"caps":{"mse":true}}`)
	if !strings.Contains(r.Body.String(), `"offset_ms":350`) || !strings.Contains(r.Body.String(), `"file":true`) {
		t.Fatalf("play answer lacks the offset: %s", r.Body)
	}
	if r := call(t, h, ana, "POST", base+"/save", `{"ms":350}`); r.Code != 200 {
		t.Fatalf("save: %d %s", r.Code, r.Body)
	}
	if len(nudged) != 1 || !strings.Contains(nudged[0], `"ms":350`) {
		t.Fatalf("Pocket Subtitles got %v", nudged)
	}
	if got := s.store.SubtitleOffsets(ctx, ana.u.ID, movieID)[2]; got != 0 {
		t.Fatalf("offset kept after saving: %d", got)
	}
	if r := call(t, h, ana, "GET", "/api/v1/play/"+movieID+"/subtitles/9", ""); r.Code != http.StatusNotFound {
		t.Fatalf("unknown track: %d", r.Code)
	}
}
