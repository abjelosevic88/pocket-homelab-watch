package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// ---- Jellyfin apps through Pocket Watch (/jellyfin/*) ----
//
// The Pocket Homelab app speaks the Jellyfin API, so Pocket Watch answers it
// under /jellyfin as a thin gateway, the way Pocket Music answers Subsonic at
// /rest: the app signs in with the person's Pocket Watch token (pwt_) or the
// device token Pocket Core gave the phone, and each call goes on to Jellyfin
// with the person's own Jellyfin login (jfFor). That login never leaves the
// box: where Jellyfin writes it into an answer (HLS playlists, transcoding
// URLs) it is swapped for the token the app sent. Only the parts of the API a
// player needs are passed: the library, images, streams, playback reporting,
// watched marks and favourites, and the library scan.

var (
	gatewayRead = regexp.MustCompile(`(?i)^/(Users|Items|Videos|Audio|Shows|Sessions|System/Info|UserItems|UserViews|UserPlayedItems|UserFavoriteItems|Playback|MediaSegments|Persons|Genres|Studios|Artists|Search|Movies|Trailers|ScheduledTasks)(/|$)`)
	// writes a player makes; nothing that changes users, policies or the server.
	// Case-insensitive like Jellyfin: its own transcoding URLs are /videos/…/master.m3u8.
	gatewayWrite = regexp.MustCompile(`(?i)^/(UserItems/[^/]+/UserData|UserPlayedItems/[^/]+|UserFavoriteItems/[^/]+|Users/[^/]+/(PlayedItems|FavoriteItems|PlayingItems)/[^/]+(/[^/]+)?|Items/[^/]+/PlaybackInfo|Sessions/Playing(/Progress|/Stopped|/Ping)?|Videos/ActiveEncodings|Library/Refresh)$`)
	// how Jellyfin clients pass a token in a header
	mediaBrowserToken = regexp.MustCompile(`(?i)\btoken=(?:"([^"]*)"|([^,\s"]+))`)
)

// gatewayToken is the token a Jellyfin client sent, and whether it came in the query
// (images and streams that a player opens as plain URLs).
func gatewayToken(r *http.Request) (string, bool) {
	for _, h := range []string{"Authorization", "X-Emby-Authorization"} {
		if m := mediaBrowserToken.FindStringSubmatch(r.Header.Get(h)); m != nil {
			if t := m[1] + m[2]; t != "" {
				return t, false
			}
		}
	}
	for _, h := range []string{"X-Emby-Token", "X-MediaBrowser-Token"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v, false
		}
	}
	for k, vs := range r.URL.Query() {
		if l := strings.ToLower(k); (l == "api_key" || l == "apikey") && len(vs) > 0 && vs[0] != "" {
			return vs[0], true
		}
	}
	return "", false
}

func (s *Server) gatewayUser(ctx context.Context, tok string) (*store.User, error) {
	if s.core.Configured() && core.IsCoreToken(tok) {
		return s.coreUser(ctx, tok)
	}
	return s.store.GetUserByAPIToken(ctx, tok)
}

func gatewayError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// handleGateway answers /jellyfin/* for Jellyfin apps.
func (s *Server) handleGateway(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tok, inQuery := gatewayToken(r)
	if tok == "" {
		gatewayError(w, http.StatusUnauthorized, "sign in with a Pocket Watch token")
		return
	}
	u, err := s.gatewayUser(ctx, tok)
	if err != nil || u == nil {
		gatewayError(w, http.StatusUnauthorized, "this token is not valid for Pocket Watch")
		return
	}
	jc, err := s.jfFor(ctx, u)
	if err != nil {
		if isNoJellyfin(err) {
			gatewayError(w, http.StatusServiceUnavailable, "Pocket Watch: "+err.Error())
			return
		}
		gatewayError(w, http.StatusBadGateway, "Pocket Watch could not reach Jellyfin")
		return
	}
	path := "/" + chi.URLParam(r, "*")
	if v, err := url.PathUnescape(path); err == nil {
		path = v
	}
	if strings.Contains(path, "..") {
		http.NotFound(w, r)
		return
	}
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	if (read && !gatewayRead.MatchString(path)) || (!read && !gatewayWrite.MatchString(path)) {
		gatewayError(w, http.StatusForbidden, "Pocket Watch does not pass this on to Jellyfin")
		return
	}
	// The person's own account, never the household's list.
	if read && (strings.EqualFold(path, "/Users") || strings.EqualFold(path, "/Users/Me")) {
		s.gatewaySelf(w, r, jc, strings.EqualFold(path, "/Users"))
		return
	}
	q := r.URL.Query()
	for k := range q {
		if l := strings.ToLower(k); l == "api_key" || l == "apikey" {
			q.Del(k)
		}
	}
	// A player that authenticates in the URL follows the URLs Jellyfin writes into
	// playlists, so those must carry a token too: Jellyfin's, swapped below.
	if inQuery {
		q.Set("api_key", jc.c.Token)
	}
	hdr := http.Header{}
	for k, vs := range r.Header {
		switch http.CanonicalHeaderKey(k) {
		case "Authorization", "X-Emby-Authorization", "X-Emby-Token", "X-Mediabrowser-Token", "Cookie", "Host",
			"Accept-Encoding", "Connection", "Keep-Alive", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length":
			continue
		}
		hdr[k] = vs
	}
	// Request bodies are small JSON (playback reports, PlaybackInfo): read them whole so
	// Jellyfin gets a Content-Length.
	var body io.Reader
	if !read {
		b, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			gatewayError(w, http.StatusBadRequest, "could not read the request")
			return
		}
		body = bytes.NewReader(b)
	}
	res, err := jc.c.RawBody(ctx, r.Method, path, q.Encode(), hdr, body)
	if err != nil {
		if ctx.Err() == nil {
			gatewayError(w, http.StatusBadGateway, "Jellyfin unreachable")
		}
		return
	}
	defer res.Body.Close()
	ct := strings.ToLower(res.Header.Get("Content-Type"))
	text := strings.Contains(ct, "json") || strings.Contains(ct, "mpegurl") || strings.HasPrefix(ct, "text/") || strings.HasSuffix(path, ".m3u8")
	for k, vs := range res.Header {
		switch http.CanonicalHeaderKey(k) {
		case "Set-Cookie", "Connection", "Keep-Alive", "Transfer-Encoding", "Content-Encoding":
			continue
		case "Content-Length":
			if text {
				continue
			}
		}
		w.Header()[k] = vs
	}
	if text {
		raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
		if err != nil {
			return
		}
		raw = swapToken(raw, jc.c.Token, tok)
		if strings.HasSuffix(path, ".m3u8") {
			w.Header().Set("Cache-Control", "no-store")
		}
		w.WriteHeader(res.StatusCode)
		if r.Method != http.MethodHead {
			_, _ = w.Write(raw)
		}
		return
	}
	w.WriteHeader(res.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	buf := make([]byte, 256<<10)
	for {
		n, rerr := res.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
		}
		if rerr != nil {
			return
		}
	}
}

// swapToken replaces the person's Jellyfin token in an answer with the one the app used.
func swapToken(b []byte, jf, client string) []byte {
	if jf == "" || jf == client {
		return b
	}
	b = bytes.ReplaceAll(b, []byte(jf), []byte(client))
	if e := url.QueryEscape(jf); e != jf {
		b = bytes.ReplaceAll(b, []byte(e), []byte(url.QueryEscape(client)))
	}
	return b
}

// gatewaySelf answers /Users and /Users/Me with the person's own Jellyfin user.
func (s *Server) gatewaySelf(w http.ResponseWriter, r *http.Request, jc *jfConn, list bool) {
	res, err := jc.c.RawBody(r.Context(), http.MethodGet, "/Users/"+url.PathEscape(jc.c.UserID), "", nil, nil)
	if err != nil {
		gatewayError(w, http.StatusBadGateway, "Jellyfin unreachable")
		return
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil || res.StatusCode != http.StatusOK {
		w.WriteHeader(res.StatusCode)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if list {
		raw = append(append([]byte("["), raw...), ']')
	}
	_, _ = w.Write(raw)
}
