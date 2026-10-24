package server

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-watch/internal/jellyfin"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// ---- playing ----
//
// The browser asks Pocket Watch how to play a title; Pocket Watch asks
// Jellyfin with a device profile built from what this browser can decode
// (direct play when the file fits, otherwise an HLS stream Jellyfin
// transcodes or remuxes) and hands back a URL under /api/v1/media/. Every
// request there is passed to Jellyfin with the person's own token added, so
// the token never reaches the browser. Progress goes back to Jellyfin as the
// person (resume points in every Jellyfin app) and into the history here.

// caps is what the browser says it can play.
type caps struct {
	NativeHLS bool `json:"native_hls"` // Safari: HLS without hls.js
	MSE       bool `json:"mse"`        // hls.js works
	HEVC      bool `json:"hevc"`
	AV1       bool `json:"av1"`
	VP9       bool `json:"vp9"`
	AC3       bool `json:"ac3"`
	EAC3      bool `json:"eac3"`
	Opus      bool `json:"opus"`
	FLAC      bool `json:"flac"`
	MaxWidth  int  `json:"max_width"`
	Surround  bool `json:"surround"`
}

// deviceProfile is the Jellyfin DeviceProfile for a browser with these capabilities.
func deviceProfile(c caps, maxBitrate int) map[string]any {
	video := []string{"h264"}
	if c.HEVC {
		video = append(video, "hevc")
	}
	if c.AV1 {
		video = append(video, "av1")
	}
	if c.VP9 {
		video = append(video, "vp9")
	}
	audio := []string{"aac", "mp3", "mp2"}
	if c.Opus {
		audio = append(audio, "opus")
	}
	if c.FLAC {
		audio = append(audio, "flac")
	}
	if c.AC3 {
		audio = append(audio, "ac3")
	}
	if c.EAC3 {
		audio = append(audio, "eac3")
	}
	hlsVideo := "h264"
	hlsContainer := "ts"
	if c.HEVC && c.NativeHLS {
		hlsVideo, hlsContainer = "h264,hevc", "mp4"
	}
	hlsAudio := "aac,mp3"
	if c.AC3 {
		hlsAudio += ",ac3"
	}
	if c.EAC3 {
		hlsAudio += ",eac3"
	}
	channels := "2"
	if c.Surround {
		channels = "6"
	}
	webmVideo := []string{"vp8"}
	if c.VP9 {
		webmVideo = append(webmVideo, "vp9")
	}
	if c.AV1 {
		webmVideo = append(webmVideo, "av1")
	}
	direct := []map[string]any{
		{"Container": "mp4,m4v", "Type": "Video", "VideoCodec": strings.Join(video, ","), "AudioCodec": strings.Join(audio, ",")},
		{"Container": "webm", "Type": "Video", "VideoCodec": strings.Join(webmVideo, ","), "AudioCodec": "vorbis,opus"},
	}
	if c.NativeHLS {
		direct = append(direct, map[string]any{"Container": "mov", "Type": "Video", "VideoCodec": strings.Join(video, ","), "AudioCodec": strings.Join(audio, ",")})
	}
	codec := []map[string]any{
		{"Type": "Video", "Codec": "h264", "Conditions": []map[string]any{
			{"Condition": "EqualsAny", "Property": "VideoProfile", "Value": "high|main|baseline|constrained baseline", "IsRequired": false},
			{"Condition": "LessThanEqual", "Property": "VideoLevel", "Value": "52", "IsRequired": false},
			{"Condition": "NotEquals", "Property": "IsAnamorphic", "Value": "true", "IsRequired": false},
			{"Condition": "LessThanEqual", "Property": "VideoBitDepth", "Value": "8", "IsRequired": false},
		}},
		{"Type": "VideoAudio", "Conditions": []map[string]any{{"Condition": "LessThanEqual", "Property": "AudioChannels", "Value": map[bool]string{true: "8", false: "2"}[c.Surround], "IsRequired": false}}},
	}
	if c.MaxWidth > 0 {
		codec = append(codec, map[string]any{"Type": "Video", "Conditions": []map[string]any{{"Condition": "LessThanEqual", "Property": "Width", "Value": strconv.Itoa(c.MaxWidth), "IsRequired": false}}})
	}
	return map[string]any{
		"Name":                             "Pocket Watch",
		"MaxStreamingBitrate":              maxBitrate,
		"MaxStaticBitrate":                 maxBitrate,
		"MusicStreamingTranscodingBitrate": 384000,
		"DirectPlayProfiles":               direct,
		"TranscodingProfiles": []map[string]any{
			{"Container": hlsContainer, "Type": "Video", "VideoCodec": hlsVideo, "AudioCodec": hlsAudio, "Context": "Streaming", "Protocol": "hls",
				"MaxAudioChannels": channels, "MinSegments": "1", "BreakOnNonKeyFrames": true},
		},
		"ContainerProfiles": []any{},
		"CodecProfiles":     codec,
		"SubtitleProfiles": []map[string]any{
			{"Format": "vtt", "Method": "External"},
			{"Format": "ass", "Method": "External"},
			{"Format": "ssa", "Method": "External"},
			{"Format": "pgssub", "Method": "Encode"},
			{"Format": "dvdsub", "Method": "Encode"},
			{"Format": "dvbsub", "Method": "Encode"},
		},
	}
}

func bitrateFor(q string) int {
	switch q {
	case "60", "40", "20", "10", "8", "4", "2", "1":
		n, _ := strconv.Atoi(q)
		return n * 1_000_000
	}
	return 120_000_000
}

// playState is one title being watched in one browser.
type playState struct {
	uid        int64
	jfKey      string
	itemID     string
	source     string
	session    string
	method     string // DirectPlay | Transcode
	audio, sub int
	device     string
	item       store.Watch
	segments   []jellyfin.Segment
	started    time.Time
	lastPos    int // seconds
	lastAt     time.Time
	watched    int // seconds actually played
	paused     bool
	finished   bool // counted as watched (published once)
	savedAt    time.Time
}

type streamOut struct {
	Index    int    `json:"index"`
	Label    string `json:"label"`
	Lang     string `json:"lang"`
	Codec    string `json:"codec"`
	Channels int    `json:"channels,omitempty"`
	Default  bool   `json:"default,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
	External bool   `json:"external,omitempty"`
	Text     bool   `json:"text"`
	URL      string `json:"url,omitempty"`
	Offset   int    `json:"offset_ms,omitempty"` // the person's timing for this track
	File     bool   `json:"file,omitempty"`      // an .srt Pocket Subtitles can save the timing into
}

var hexID = regexp.MustCompile(`^[0-9a-fA-F-]{32,36}$`)

// handlePlay prepares playback of a film or an episode.
func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	jc := s.jf(w, r)
	if jc == nil {
		return
	}
	id := chi.URLParam(r, "id")
	var in struct {
		Start     float64 `json:"start"` // seconds; < 0 = resume where the person stopped
		Audio     *int    `json:"audio"`
		Subtitle  *int    `json:"subtitle"` // -1 = none
		Source    string  `json:"media_source"`
		Caps      caps    `json:"caps"`
		Quality   string  `json:"quality"`
		Device    string  `json:"device"`
		Replace   string  `json:"replace"`   // the session this one takes over from (switching track)
		Transcode bool    `json:"transcode"` // the browser failed to play the file as it is
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	st, _ := s.store.GetSettings(ctx, u.ID)
	it, err := jc.c.Item(ctx, id)
	if err != nil {
		s.jfErr(w, err)
		return
	}
	if it.Type != "Movie" && it.Type != "Episode" && it.Type != "Video" {
		writeErr(w, http.StatusBadRequest, "only films and episodes play")
		return
	}
	if len(it.MediaSources) == 0 {
		writeErr(w, http.StatusConflict, "this title has no file yet")
		return
	}
	src := it.MediaSources[0]
	for _, m := range it.MediaSources {
		if m.ID == in.Source {
			src = m
		}
	}
	runtime := int(firstTicks(src.RunTimeTicks, it.RunTimeTicks) / jellyfin.Ticks)
	start := in.Start
	if start < 0 {
		start = 0
		if ud := it.UserData; ud != nil && ud.PlaybackPositionTicks > 0 {
			p := float64(ud.PlaybackPositionTicks) / jellyfin.Ticks
			if runtime == 0 || p < float64(runtime)*0.95 {
				start = p
			}
		}
	}
	audio := s.pickAudio(src, st, in.Audio)
	sub := s.pickSubtitle(src, st, audio, in.Subtitle)
	burn := false
	direct := !in.Transcode
	defAudio := -1
	if src.DefaultAudioStreamIndex != nil {
		defAudio = *src.DefaultAudioStreamIndex
	}
	for _, ms := range src.MediaStreams {
		if ms.Type == "Subtitle" && ms.Index == sub && !ms.IsTextSubtitleStream {
			burn = true
		}
	}
	quality := firstNonEmpty(in.Quality, st.Quality)
	body := map[string]any{
		"DeviceProfile":        deviceProfile(in.Caps, bitrateFor(quality)),
		"MaxStreamingBitrate":  bitrateFor(quality),
		"StartTimeTicks":       int64(start * jellyfin.Ticks),
		"MediaSourceId":        src.ID,
		"AudioStreamIndex":     audio,
		"EnableDirectPlay":     direct && !burn && (audio == defAudio || audio < 0),
		"EnableDirectStream":   true,
		"EnableTranscoding":    true,
		"AllowVideoStreamCopy": true,
		"AllowAudioStreamCopy": true,
		"AutoOpenLiveStream":   true,
	}
	if burn {
		body["SubtitleStreamIndex"] = sub
	}
	pi, err := jc.c.PlaybackInfo(ctx, id, body)
	if err != nil {
		s.jfErr(w, err)
		return
	}
	if len(pi.MediaSources) == 0 {
		writeErr(w, http.StatusBadGateway, "Jellyfin offered no way to play this")
		return
	}
	ms := pi.MediaSources[0]
	out := map[string]any{"session": pi.PlaySessionID, "media_source": ms.ID, "start": start, "runtime": runtime, "title": view(it, false)}
	method := "Transcode"
	switch {
	case direct && ms.SupportsDirectPlay && !burn && (audio == defAudio || audio < 0):
		method = "DirectPlay"
		q := url.Values{"static": {"true"}, "mediaSourceId": {ms.ID}, "PlaySessionId": {pi.PlaySessionID}}
		if ms.ETag != "" {
			q.Set("Tag", ms.ETag)
		}
		out["url"] = "/api/v1/media/Videos/" + url.PathEscape(id) + "/stream?" + q.Encode()
		out["hls"] = false
	case ms.TranscodingURL != "":
		out["url"] = "/api/v1/media" + cleanTranscodeURL(ms.TranscodingURL)
		out["hls"] = strings.EqualFold(ms.TranscodingSubProtocol, "hls") || strings.Contains(ms.TranscodingURL, ".m3u8")
	default:
		writeErr(w, http.StatusConflict, "this file can't be played in a browser and Jellyfin won't transcode it")
		return
	}
	out["method"] = method
	// streams to choose from
	var audios, subs []streamOut
	offsets := s.store.SubtitleOffsets(ctx, u.ID, id)
	for _, x := range src.MediaStreams {
		switch x.Type {
		case "Audio":
			audios = append(audios, streamOut{Index: x.Index, Label: firstNonEmpty(x.DisplayTitle, x.Title, x.Language, "Track "+strconv.Itoa(x.Index)), Lang: x.Language, Codec: x.Codec, Channels: x.Channels, Default: x.IsDefault})
		case "Subtitle":
			so := streamOut{Index: x.Index, Label: firstNonEmpty(x.DisplayTitle, x.Title, x.Language, "Subtitles "+strconv.Itoa(x.Index)), Lang: x.Language, Codec: x.Codec, Default: x.IsDefault, Forced: x.IsForced, External: x.IsExternal, Text: x.IsTextSubtitleStream,
				Offset: offsets[x.Index], File: fileTrack(&x)}
			if x.IsTextSubtitleStream {
				so.URL = "/api/v1/media/Videos/" + url.PathEscape(id) + "/" + url.PathEscape(src.ID) + "/Subtitles/" + strconv.Itoa(x.Index) + "/0/Stream.vtt"
			}
			subs = append(subs, so)
		}
	}
	out["audio"], out["audio_index"] = nz(audios), audio
	out["subtitles"], out["subtitle_index"] = nz(subs), sub
	out["burned"] = burn
	var chapters []map[string]any
	for _, c := range it.Chapters {
		chapters = append(chapters, map[string]any{"name": c.Name, "start": float64(c.StartPositionTicks) / jellyfin.Ticks})
	}
	out["chapters"] = nz(chapters)
	segs, _ := jc.c.Segments(ctx, id)
	var so []map[string]any
	for _, sg := range segs {
		so = append(so, map[string]any{"type": strings.ToLower(sg.Type), "start": float64(sg.StartTicks) / jellyfin.Ticks, "end": float64(sg.EndTicks) / jellyfin.Ticks})
	}
	out["segments"] = nz(so)
	if tp, ok := it.Trickplay[src.ID]; ok {
		best := 0
		for k := range tp {
			if n, _ := strconv.Atoi(k); n > best && n <= 480 {
				best = n
			}
		}
		if best == 0 {
			for k := range tp {
				if n, _ := strconv.Atoi(k); best == 0 || n < best {
					best = n
				}
			}
		}
		if t, ok := tp[strconv.Itoa(best)]; ok && t.Interval > 0 {
			out["trickplay"] = map[string]any{"width": t.Width, "height": t.Height, "tile_w": t.TileWidth, "tile_h": t.TileHeight, "count": t.ThumbnailCount, "interval": t.Interval,
				"url": "/api/v1/media/Videos/" + url.PathEscape(id) + "/Trickplay/" + strconv.Itoa(best) + "/", "query": "MediaSourceId=" + url.QueryEscape(src.ID)}
		}
	}
	if it.Type == "Episode" && it.SeriesID != "" {
		if prev, next := s.neighbours(ctx, jc, it); next != nil || prev != nil {
			if next != nil {
				out["next"] = view(next, false)
			}
			if prev != nil {
				out["prev"] = view(prev, false)
			}
		}
	}
	if u.Role == "admin" {
		if box, _ := s.store.GetSettings(ctx, 0); box.SubtitlesURL != "" {
			q := strings.ToLower(it.Name)
			if it.Type == "Episode" && it.ParentIndexNumber != nil && it.IndexNumber != nil {
				q = strings.ToLower(it.SeriesName) + " " + strings.ToLower(episodeCode(*it.ParentIndexNumber, *it.IndexNumber))
			}
			out["subtitles_tool"] = box.SubtitlesURL + "/nudge?q=" + url.QueryEscape(q)
		}
	}
	// the play state: what history and Jellyfin hear about
	x := watchFromItem(it, store.Now(), "pocket")
	if it.Type == "Episode" && it.SeriesID != "" {
		x.Genres = s.seriesGenres(ctx, jc, it.SeriesID)
	}
	x.Device = trim(in.Device, 60)
	ps := &playState{uid: u.ID, jfKey: jc.key, itemID: id, source: ms.ID, session: pi.PlaySessionID, method: method, audio: audio, sub: sub, device: x.Device,
		item: x, segments: segs, started: time.Now(), lastPos: int(start), lastAt: time.Now()}
	s.playMu.Lock()
	if old := s.plays[in.Replace]; old != nil && old.uid == u.ID && old.itemID == id {
		// switching track or quality: the history entry carries on
		ps.watched, ps.finished = old.watched, old.finished
		delete(s.plays, in.Replace)
		go s.endEncoding(jc, old)
	}
	s.plays[pi.PlaySessionID] = ps
	s.playMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func nz[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func firstTicks(v ...int64) int64 {
	for _, x := range v {
		if x > 0 {
			return x
		}
	}
	return 0
}

func trim(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// cleanTranscodeURL drops the token Jellyfin put in the URL (the proxy adds
// the person's own) and the start time (the player seeks itself).
func cleanTranscodeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	for k := range q {
		switch strings.ToLower(k) {
		case "api_key", "apikey", "starttimeticks":
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	return u.Path + "?" + u.RawQuery
}

// pickAudio chooses the audio track: asked for, else the person's language, else the file's default.
func (s *Server) pickAudio(src jellyfin.MediaSource, st store.Settings, asked *int) int {
	if asked != nil {
		return *asked
	}
	if st.AudioLang != "" {
		for _, x := range src.MediaStreams {
			if x.Type == "Audio" && strings.EqualFold(x.Language, st.AudioLang) {
				return x.Index
			}
		}
	}
	if src.DefaultAudioStreamIndex != nil {
		return *src.DefaultAudioStreamIndex
	}
	for _, x := range src.MediaStreams {
		if x.Type == "Audio" {
			return x.Index
		}
	}
	return -1
}

// pickSubtitle chooses subtitles: asked for, else by the person's settings,
// else what Jellyfin chose with the account's own preferences.
func (s *Server) pickSubtitle(src jellyfin.MediaSource, st store.Settings, audio int, asked *int) int {
	if asked != nil {
		return *asked
	}
	var langs []string
	for _, l := range strings.Split(st.SubtitleLang, ",") {
		if l = strings.TrimSpace(strings.ToLower(l)); l != "" {
			langs = append(langs, l)
		}
	}
	audioLang := ""
	for _, x := range src.MediaStreams {
		if x.Type == "Audio" && x.Index == audio {
			audioLang = strings.ToLower(x.Language)
		}
	}
	find := func(lang string, forced bool) int {
		best := -1
		for _, x := range src.MediaStreams {
			if x.Type != "Subtitle" || (lang != "" && !strings.EqualFold(x.Language, lang)) || (forced && !x.IsForced) {
				continue
			}
			// text subtitles first: they don't need a transcode
			if best < 0 || (x.IsTextSubtitleStream && !isText(src, best)) {
				best = x.Index
			}
		}
		return best
	}
	switch st.SubtitleMode {
	case "off":
		return -1
	case "always":
		for _, l := range langs {
			if i := find(l, false); i >= 0 {
				return i
			}
		}
		if len(langs) == 0 && src.DefaultSubtitleStreamIndex != nil {
			return *src.DefaultSubtitleStreamIndex
		}
		return -1
	case "forced":
		return find(audioLang, true)
	}
	// auto: subtitles when the audio isn't in a language the person reads, forced ones otherwise
	if len(langs) > 0 {
		for _, l := range langs {
			if l == audioLang {
				return find(audioLang, true)
			}
		}
		for _, l := range langs {
			if i := find(l, false); i >= 0 {
				return i
			}
		}
		return -1
	}
	if src.DefaultSubtitleStreamIndex != nil {
		return *src.DefaultSubtitleStreamIndex
	}
	return -1
}

func isText(src jellyfin.MediaSource, idx int) bool {
	for _, x := range src.MediaStreams {
		if x.Index == idx {
			return x.IsTextSubtitleStream
		}
	}
	return false
}

// neighbours finds the episodes before and after this one (skipping missing ones).
func (s *Server) neighbours(ctx context.Context, jc *jfConn, it *jellyfin.Item) (prev, next *jellyfin.Item) {
	res, err := jc.c.Episodes(ctx, it.SeriesID, "")
	if err != nil {
		return nil, nil
	}
	var list []jellyfin.Item
	for _, e := range res.Items {
		if e.LocationType != "Virtual" {
			list = append(list, e)
		}
	}
	for i := range list {
		if list[i].ID == it.ID {
			if i > 0 {
				prev = &list[i-1]
			}
			if i+1 < len(list) {
				next = &list[i+1]
			}
			return
		}
	}
	return nil, nil
}

// seriesGenres are a show's genres (episodes have none of their own), cached for a day.
func (s *Server) seriesGenres(ctx context.Context, jc *jfConn, seriesID string) []string {
	key := "genres:" + seriesID
	if v, at, ok := s.store.CacheGet(ctx, key); ok && fresh(at, 24*time.Hour) {
		if v == "" {
			return []string{}
		}
		return strings.Split(v, "|")
	}
	it, err := jc.c.Item(ctx, seriesID)
	if err != nil {
		return []string{}
	}
	_ = s.store.CachePut(ctx, key, strings.Join(it.Genres, "|"))
	if it.Genres == nil {
		return []string{}
	}
	return it.Genres
}

func fresh(at string, d time.Duration) bool {
	t, err := time.Parse(time.RFC3339Nano, at)
	return err == nil && time.Since(t) < d
}

// handleReport is the player's progress: start, every ten seconds, pause, stop.
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		Session  string  `json:"session"`
		Event    string  `json:"event"` // start | progress | pause | unpause | stop
		Position float64 `json:"position"`
		Paused   bool    `json:"paused"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	s.playMu.Lock()
	ps := s.plays[in.Session]
	s.playMu.Unlock()
	if ps == nil || ps.uid != u.ID || ps.itemID != chi.URLParam(r, "id") {
		writeErr(w, http.StatusNotFound, "no such play session")
		return
	}
	jc, err := s.jfFor(ctx, u)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.advance(ps, int(in.Position), in.Paused)
	body := s.reportBody(ps)
	switch in.Event {
	case "start":
		err = jc.c.Report(ctx, "", body)
	case "stop":
		s.finish(context.WithoutCancel(ctx), jc, ps)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	default:
		body["EventName"] = map[string]string{"pause": "Pause", "unpause": "Unpause"}[in.Event]
		if body["EventName"] == "" {
			body["EventName"] = "TimeUpdate"
		}
		err = jc.c.Report(ctx, "Progress", body)
	}
	if err != nil {
		s.log.Debug("jellyfin report", "event", in.Event, "err", err)
	}
	// keep history current while it plays (a closed tab never says stop)
	if time.Since(ps.savedAt) > 2*time.Minute || in.Event == "pause" {
		s.record(ctx, jc, ps)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "completed": ps.finished})
}

// advance counts the seconds actually watched since the last report.
func (s *Server) advance(ps *playState, pos int, paused bool) {
	s.playMu.Lock()
	defer s.playMu.Unlock()
	now := time.Now()
	if !ps.paused {
		d := pos - ps.lastPos
		wall := int(now.Sub(ps.lastAt).Seconds()) + 5
		if d > 0 && d <= wall {
			ps.watched += d
		}
	}
	ps.lastPos, ps.lastAt, ps.paused = pos, now, paused
}

func (s *Server) reportBody(ps *playState) map[string]any {
	b := map[string]any{"ItemId": ps.itemID, "MediaSourceId": ps.source, "PlaySessionId": ps.session, "PositionTicks": int64(ps.lastPos) * jellyfin.Ticks,
		"PlayMethod": ps.method, "CanSeek": true, "IsPaused": ps.paused, "AudioStreamIndex": ps.audio}
	if ps.sub >= 0 {
		b["SubtitleStreamIndex"] = ps.sub
	}
	return b
}

// completed: past the person's share of the runtime, or into the end credits.
func (s *Server) completed(ps *playState, st store.Settings) bool {
	rt := ps.item.Runtime
	if rt <= 0 {
		return false
	}
	pct := st.Completion
	if pct < 50 || pct > 100 {
		pct = 90
	}
	if ps.lastPos*100 >= rt*pct {
		return true
	}
	for _, sg := range ps.segments {
		if sg.Type == "Outro" && float64(ps.lastPos) >= float64(sg.StartTicks)/jellyfin.Ticks && float64(sg.EndTicks)/jellyfin.Ticks >= float64(rt)-30 {
			return true
		}
	}
	return false
}

// record writes the play into the person's history (and tells Core once it counts as watched).
func (s *Server) record(ctx context.Context, jc *jfConn, ps *playState) {
	st, _ := s.store.GetSettings(ctx, ps.uid)
	s.playMu.Lock()
	x := ps.item
	x.WatchedAt, x.Day = store.Now(), ""
	x.Position, x.Seconds = ps.lastPos, ps.watched
	done := s.completed(ps, st)
	x.Completed = done
	first := done && !ps.finished
	if done {
		ps.finished = true
	}
	ps.savedAt = time.Now()
	s.playMu.Unlock()
	if x.Seconds < 60 && !done {
		return // a peek isn't a watch
	}
	if _, err := s.store.UpsertWatch(ctx, ps.uid, &x); err != nil {
		s.log.Warn("history", "err", err)
	}
	if first {
		if u, err := s.store.GetUser(ctx, ps.uid); err == nil {
			p := map[string]any{"item_id": x.ItemID, "kind": x.Kind, "title": x.Title, "year": x.Year, "runtime": x.Runtime}
			if x.Kind == "episode" {
				p["series"], p["season"], p["episode"] = x.Series, x.Season, x.Episode
			}
			s.publish(ctx, u, "watch.watched", x.ItemID, p)
		}
	}
}

// finish ends a play session: Jellyfin hears it stopped, the transcode ends,
// the resume point is saved and history gets the final word.
func (s *Server) finish(ctx context.Context, jc *jfConn, ps *playState) {
	s.playMu.Lock()
	delete(s.plays, ps.session)
	s.playMu.Unlock()
	st, _ := s.store.GetSettings(ctx, ps.uid)
	body := s.reportBody(ps)
	if err := jc.c.Report(ctx, "Stopped", body); err != nil {
		s.log.Debug("jellyfin stopped", "err", err)
	}
	s.endEncoding(jc, ps)
	done := s.completed(ps, st)
	if jc.apiKey {
		// an API key has no session of its own: write the person's state by hand
		data := map[string]any{"PlaybackPositionTicks": int64(ps.lastPos) * jellyfin.Ticks, "LastPlayedDate": time.Now().UTC().Format(time.RFC3339)}
		if done {
			data["PlaybackPositionTicks"] = 0
			data["Played"] = true
		}
		if err := jc.c.SaveUserData(ctx, ps.itemID, data); err != nil {
			s.log.Debug("jellyfin user data", "err", err)
		}
		if done {
			_ = jc.c.SetPlayed(ctx, ps.itemID, true)
		}
	}
	s.record(ctx, jc, ps)
}

func (s *Server) endEncoding(jc *jfConn, ps *playState) {
	if ps.method == "DirectPlay" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = jc.c.StopEncoding(ctx, ps.session)
}

// sweepPlays ends sessions whose browser went quiet (tab closed, phone asleep).
func (s *Server) sweepPlays(ctx context.Context) {
	s.playMu.Lock()
	var stale []*playState
	for _, ps := range s.plays {
		if time.Since(ps.lastAt) > 10*time.Minute {
			stale = append(stale, ps)
		}
	}
	s.playMu.Unlock()
	for _, ps := range stale {
		u, err := s.store.GetUser(ctx, ps.uid)
		if err != nil {
			s.playMu.Lock()
			delete(s.plays, ps.session)
			s.playMu.Unlock()
			continue
		}
		if jc, err := s.jfFor(ctx, u); err == nil {
			s.finish(ctx, jc, ps)
		}
	}
}

// ---- the media proxy ----

var mediaPath = regexp.MustCompile(`^/[Vv]ideos/[0-9a-fA-F-]{32,36}/`)

// handleMedia passes streams, HLS playlists and segments, subtitles and
// trickplay tiles to Jellyfin as the signed-in person.
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	jc, err := s.jfFor(r.Context(), userFrom(r.Context()))
	if err != nil {
		http.Error(w, "not connected", http.StatusConflict)
		return
	}
	path := "/" + chi.URLParam(r, "*")
	if !mediaPath.MatchString(path) || strings.Contains(path, "..") {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	for k := range q {
		if l := strings.ToLower(k); l == "api_key" || l == "apikey" {
			q.Del(k)
		}
	}
	hdr := http.Header{}
	for _, h := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if v := r.Header.Get(h); v != "" {
			hdr.Set(h, v)
		}
	}
	res, err := jc.c.Raw(r.Context(), r.Method, path, q.Encode(), hdr)
	if err != nil {
		if r.Context().Err() == nil {
			http.Error(w, "jellyfin unreachable", http.StatusBadGateway)
		}
		return
	}
	defer res.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Cache-Control"} {
		if v := res.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	if strings.HasSuffix(path, ".m3u8") {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.WriteHeader(res.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	buf := make([]byte, 256<<10)
	fl, _ := w.(http.Flusher)
	for {
		n, rerr := res.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if fl != nil && strings.HasSuffix(path, ".m3u8") {
				fl.Flush()
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				s.log.Debug("media proxy", "path", path, "err", rerr)
			}
			return
		}
	}
}
