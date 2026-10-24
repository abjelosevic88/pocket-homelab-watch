package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-watch/internal/jellyfin"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// ---- subtitle timing ----
//
// Two layers. Every person can move a subtitle track earlier or later while
// watching: the offset is theirs, remembered per title and track, and the
// player applies it to the cues (works for embedded subtitles too). When the
// track is a .srt file next to the video, they can also save that offset
// into the file through Pocket Subtitles: it shifts every cue, pins the file
// so the nightly sweep keeps the timing, tells Jellyfin, and keeps a backup
// for "undo". Then everyone, in every Jellyfin app, gets the fixed timing.
//
// Watch reaches Pocket Subtitles as the person through a Pocket Core app
// link (scope subtitles:sync), or with a token an admin set for the box.

// subsClient is one way into Pocket Subtitles' API.
type subsClient struct {
	base, token, via string
	http             *http.Client
}

var errNoSubs = errors.New("Pocket Subtitles is not connected")

// subsMode is the mode in effect: direct with a token, else Core when the box has one.
func (s *Server) subsMode(box store.Settings) string {
	switch box.SubtitlesMode {
	case "off", "direct", "core":
		return box.SubtitlesMode
	}
	if box.SubtitlesToken != "" && box.SubtitlesAPIURL != "" {
		return "direct"
	}
	if s.core.Configured() {
		return "core"
	}
	return "off"
}

// subsFor connects to Pocket Subtitles for a person (or says why it can't).
func (s *Server) subsFor(ctx context.Context, u *store.User) (*subsClient, error) {
	box, _ := s.store.GetSettings(ctx, 0)
	switch s.subsMode(box) {
	case "direct":
		if box.SubtitlesAPIURL == "" || box.SubtitlesToken == "" {
			return nil, errors.New("Pocket Subtitles needs an address and a token under Settings → Services")
		}
		return &subsClient{base: strings.TrimRight(box.SubtitlesAPIURL, "/"), token: box.SubtitlesToken, via: "direct", http: s.http}, nil
	case "core":
		lt, err := s.core.ActAs(ctx, u.Email, "subtitles", "subtitles:sync")
		if err != nil {
			return nil, errors.New(coreReason(err, "Pocket Subtitles"))
		}
		if lt.URL == "" {
			return nil, errors.New("Pocket Core did not say where Pocket Subtitles is")
		}
		return &subsClient{base: strings.TrimRight(lt.URL, "/"), token: lt.AccessToken, via: "core", http: s.http}, nil
	}
	return nil, errNoSubs
}

type subsError struct {
	Status int
	Msg    string
}

func (e *subsError) Error() string { return "Pocket Subtitles: " + e.Msg }

func (c *subsClient) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+"/api/v1"+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Pocket Subtitles is not reachable: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = res.Status
		}
		return &subsError{res.StatusCode, e.Error}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// subsFile is what Pocket Subtitles says about one file.
type subsFile struct {
	ID      int64  `json:"id"`
	Rel     string `json:"rel"`
	Lang    string `json:"lang"`
	Pinned  bool   `json:"pinned"`
	NudgeMS int    `json:"nudge_ms"`
	CanUndo bool   `json:"can_undo"`
	Exists  bool   `json:"exists"`
}

// subtitleStream finds one subtitle track of a title.
func (s *Server) subtitleStream(ctx context.Context, jc *jfConn, itemID string, index int) (*jellyfin.Item, *jellyfin.MediaStream, error) {
	it, err := jc.c.Item(ctx, itemID)
	if err != nil {
		return nil, nil, err
	}
	for _, ms := range it.MediaSources {
		for i := range ms.MediaStreams {
			if st := &ms.MediaStreams[i]; st.Type == "Subtitle" && st.Index == index {
				return it, st, nil
			}
		}
	}
	return nil, nil, store.ErrNotFound
}

// fileTrack reports whether a track is a .srt file Pocket Subtitles can change.
func fileTrack(st *jellyfin.MediaStream) bool {
	return st.IsExternal && st.Path != "" && strings.EqualFold(strings.TrimPrefix(pathExt(st.Path), "."), "srt")
}

func pathExt(p string) string {
	if i := strings.LastIndex(p, "."); i >= 0 && i > strings.LastIndex(p, "/") {
		return p[i:]
	}
	return ""
}

func trackParams(r *http.Request) (string, int, bool) {
	id := chi.URLParam(r, "id")
	idx, err := strconv.Atoi(chi.URLParam(r, "index"))
	return id, idx, err == nil && hexID.MatchString(id)
}

// handleSubtitleTiming is a track's timing: the person's offset and, for a
// .srt file, what Pocket Subtitles knows (moved so far, pinned, undo).
func (s *Server) handleSubtitleTiming(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	id, idx, ok := trackParams(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	jc := s.jf(w, r)
	if jc == nil {
		return
	}
	_, st, err := s.subtitleStream(ctx, jc, id, idx)
	if err != nil {
		s.jfOrFail(w, err)
		return
	}
	out := s.timingOut(ctx, u, id, idx, st)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) timingOut(ctx context.Context, u *store.User, id string, idx int, st *jellyfin.MediaStream) map[string]any {
	out := map[string]any{"offset_ms": s.store.SubtitleOffsets(ctx, u.ID, id)[idx], "file": false, "via": ""}
	box, _ := s.store.GetSettings(ctx, 0)
	mode := s.subsMode(box)
	out["connected"] = mode != "off"
	if !fileTrack(st) {
		out["why"] = "These subtitles are inside the video file: your timing stays yours (it can't be saved for everyone)."
		if st.IsExternal {
			out["why"] = "Only .srt files can be saved for everyone; your timing stays yours."
		}
		return out
	}
	if mode == "off" {
		out["why"] = "Connect Pocket Subtitles (Settings → Services) to save the timing into the file for everyone."
		return out
	}
	sc, err := s.subsFor(ctx, u)
	if err != nil {
		out["why"] = err.Error()
		return out
	}
	out["via"] = sc.via
	var f subsFile
	if err := sc.do(ctx, http.MethodGet, "/files/by-path?"+url.Values{"path": {st.Path}}.Encode(), nil, &f); err != nil {
		var se *subsError
		if errors.As(err, &se) && se.Status == http.StatusNotFound {
			out["why"] = "Pocket Subtitles doesn't look after this file (it is outside its library)."
		} else {
			out["why"] = err.Error()
		}
		return out
	}
	if f.ID == 0 || !f.Exists {
		out["why"] = "Pocket Subtitles can't find this file."
		return out
	}
	out["file"] = true
	out["file_id"] = f.ID
	out["file_name"] = f.Rel
	out["file_ms"] = f.NudgeMS
	out["pinned"] = f.Pinned
	out["can_undo"] = f.CanUndo
	if box.SubtitlesURL != "" && u.Role == "admin" {
		out["open_url"] = box.SubtitlesURL + "/files/" + strconv.FormatInt(f.ID, 10)
	}
	return out
}

// handleSubtitleOffset stores the person's offset for a track (ms, positive = later).
func (s *Server) handleSubtitleOffset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, idx, ok := trackParams(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in struct {
		MS int `json:"ms"`
	}
	if err := decode(r, &in); err != nil || in.MS < -600000 || in.MS > 600000 {
		writeErr(w, http.StatusBadRequest, "ms is a number of milliseconds within ±10 minutes")
		return
	}
	if err := s.store.SetSubtitleOffset(ctx, actorID(ctx), id, idx, in.MS); err != nil {
		s.fail(w, err, "offset")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"offset_ms": in.MS})
}

// handleSubtitleSave writes the person's offset into the file through Pocket
// Subtitles (or undoes the last change), then forgets the personal offset.
func (s *Server) handleSubtitleSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	id, idx, ok := trackParams(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	undo := strings.HasSuffix(r.URL.Path, "/undo")
	var in struct {
		MS int `json:"ms"`
	}
	if !undo {
		if err := decode(r, &in); err != nil || in.MS == 0 {
			writeErr(w, http.StatusBadRequest, "move the subtitles first: ms is the shift in milliseconds")
			return
		}
	}
	jc := s.jf(w, r)
	if jc == nil {
		return
	}
	it, st, err := s.subtitleStream(ctx, jc, id, idx)
	if err != nil {
		s.jfOrFail(w, err)
		return
	}
	if !fileTrack(st) {
		writeErr(w, http.StatusConflict, "only an .srt file next to the video can be saved")
		return
	}
	sc, err := s.subsFor(ctx, u)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	var f subsFile
	if err := sc.do(ctx, http.MethodGet, "/files/by-path?"+url.Values{"path": {st.Path}}.Encode(), nil, &f); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if f.ID == 0 {
		writeErr(w, http.StatusBadGateway, "Pocket Subtitles can't find this file")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	path := fmt.Sprintf("/files/%d/nudge", f.ID)
	var body any = map[string]int{"ms": in.MS}
	if undo {
		path, body = fmt.Sprintf("/files/%d/undo", f.ID), map[string]any{}
	}
	if err := sc.do(cctx, http.MethodPost, path, body, nil); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if !undo {
		_ = s.store.SetSubtitleOffset(ctx, u.ID, id, idx, 0)
		title := it.Name
		if it.Type == "Episode" {
			title = it.SeriesName + " " + episodeCodeOf(it) + " · " + it.Name
		}
		s.log.Info("subtitle timing saved", "user", u.Email, "title", title, "track", idx, "ms", in.MS, "via", sc.via)
	}
	writeJSON(w, http.StatusOK, s.timingOut(ctx, u, id, idx, st))
}

func episodeCodeOf(it *jellyfin.Item) string {
	if it.ParentIndexNumber == nil || it.IndexNumber == nil {
		return ""
	}
	return episodeCode(*it.ParentIndexNumber, *it.IndexNumber)
}

// jfOrFail writes a missing track as 404 and anything else as a Jellyfin error.
func (s *Server) jfOrFail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such subtitle track")
		return
	}
	s.jfErr(w, err)
}
