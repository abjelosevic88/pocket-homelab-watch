package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"encoding/json"

	"github.com/abjelosevic88/pocket-homelab-watch/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// ---- settings ----

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

// publicSettings is what one person sees: their settings with secrets masked
// (the box's services only for admins).
func (s *Server) publicSettings(ctx context.Context, u *store.User, st store.Settings) map[string]any {
	out := map[string]any{
		"core_configured": s.core.Configured(), "core_registered": s.coreStatus()["registered"],
		"title": st.Title, "date_format": st.DateFormat, "time_format": st.TimeFormat,
		"quality": st.Quality, "audio_lang": st.AudioLang, "subtitle_lang": st.SubtitleLang, "subtitle_mode": st.SubtitleMode, "subtitle_size": st.SubtitleSize,
		"autoplay_next": st.AutoplayNext, "skip_intro": st.SkipIntro, "completion": st.Completion,
		"history_all": st.HistoryAll, "upcoming_scope": st.UpcomingScope,
		"notify_url": st.NotifyURL, "notify_format": st.NotifyFormat, "notify_available": st.NotifyAvailable, "notify_weekly": st.NotifyWeekly, "notify_configured": st.NotifyURL != "",
		"ai_provider": st.AIProvider, "ai_pocket_model": st.AIPocketModel, "openai_api_key": mask(st.OpenAIKey), "openai_model": st.OpenAIModel, "openai_base_url": st.OpenAIBaseURL,
		"setup_complete": st.SetupComplete, "base_url": s.cfg.BaseURL, "jellyfin_url": s.cfg.JellyfinURL != "", "jellyfin_public_url": s.cfg.JellyfinPublicURL,
		"requests": s.boxUp(ctx, "seerr"), "upcoming": s.boxUp(ctx, "sonarr") || s.boxUp(ctx, "radarr"),
		"seerr_link": firstNonEmpty(st.SeerrPublicURL, st.SeerrURL),
	}
	if u != nil && u.Role == "admin" {
		out["seerr_url"], out["seerr_public_url"], out["seerr_key"] = st.SeerrURL, st.SeerrPublicURL, mask(st.SeerrKey)
		out["sonarr_url"], out["sonarr_key"] = st.SonarrURL, mask(st.SonarrKey)
		out["radarr_url"], out["radarr_key"] = st.RadarrURL, mask(st.RadarrKey)
		out["maintainerr_url"], out["subtitles_url"] = st.MaintainerrURL, st.SubtitlesURL
		out["subtitles_mode"], out["subtitles_api_url"], out["subtitles_token"] = st.SubtitlesMode, st.SubtitlesAPIURL, mask(st.SubtitlesToken)
		out["subtitles_mode_effective"] = s.subsMode(st)
		out["seerr_mode"], out["sonarr_mode"], out["radarr_mode"], out["maintainerr_mode"] = st.SeerrMode, st.SonarrMode, st.RadarrMode, st.MaintainerrMode
	}
	return out
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), userFrom(r.Context()), st))
}

// handlePutSettings saves the caller's personal settings. The box-wide title
// is only taken from an admin and silently ignored for everyone else.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())
	isAdmin := me.Role == "admin"
	cur, err := s.store.GetSettings(r.Context(), me.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	var in store.Settings
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if keys["title"] && isAdmin {
		cur.Title = strings.TrimSpace(in.Title)
		if cur.Title == "" {
			cur.Title = store.DefaultTitle
		}
	}
	oneOf := func(k string, dst *string, v string, allowed ...string) {
		if !keys[k] {
			return
		}
		for _, a := range allowed {
			if v == a {
				*dst = v
				return
			}
		}
	}
	intIn := func(k string, dst *int, v, lo, hi int) {
		if keys[k] && v >= lo && v <= hi {
			*dst = v
		}
	}
	secret := func(k string, dst *string, v string) {
		if keys[k] && !strings.Contains(v, "••") {
			*dst = strings.TrimSpace(v)
		}
	}
	url := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimRight(strings.TrimSpace(v), "/")
		}
	}
	text := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimSpace(v)
		}
	}
	boolIn := func(k string, dst *bool, v bool) {
		if keys[k] {
			*dst = v
		}
	}
	text("date_format", &cur.DateFormat, in.DateFormat)
	text("time_format", &cur.TimeFormat, in.TimeFormat)
	oneOf("quality", &cur.Quality, in.Quality, "auto", "60", "40", "20", "10", "8", "4", "2", "1")
	text("audio_lang", &cur.AudioLang, strings.ToLower(in.AudioLang))
	text("subtitle_lang", &cur.SubtitleLang, strings.ToLower(in.SubtitleLang))
	oneOf("subtitle_mode", &cur.SubtitleMode, in.SubtitleMode, "auto", "always", "forced", "off")
	oneOf("subtitle_size", &cur.SubtitleSize, in.SubtitleSize, "s", "m", "l", "xl")
	boolIn("autoplay_next", &cur.AutoplayNext, in.AutoplayNext)
	oneOf("skip_intro", &cur.SkipIntro, in.SkipIntro, "button", "auto", "off")
	intIn("completion", &cur.Completion, in.Completion, 50, 100)
	boolIn("history_all", &cur.HistoryAll, in.HistoryAll)
	oneOf("upcoming_scope", &cur.UpcomingScope, in.UpcomingScope, "mine", "all")
	text("notify_url", &cur.NotifyURL, in.NotifyURL)
	if keys["notify_format"] && in.NotifyFormat != "" {
		cur.NotifyFormat = in.NotifyFormat
	}
	boolIn("notify_available", &cur.NotifyAvailable, in.NotifyAvailable)
	boolIn("notify_weekly", &cur.NotifyWeekly, in.NotifyWeekly)
	// the box's services: admins only, silently ignored for everyone else
	boxChanged := false
	if isAdmin {
		before := cur
		url("seerr_url", &cur.SeerrURL, in.SeerrURL)
		url("seerr_public_url", &cur.SeerrPublicURL, in.SeerrPublicURL)
		secret("seerr_key", &cur.SeerrKey, in.SeerrKey)
		url("sonarr_url", &cur.SonarrURL, in.SonarrURL)
		secret("sonarr_key", &cur.SonarrKey, in.SonarrKey)
		url("radarr_url", &cur.RadarrURL, in.RadarrURL)
		secret("radarr_key", &cur.RadarrKey, in.RadarrKey)
		url("maintainerr_url", &cur.MaintainerrURL, in.MaintainerrURL)
		url("subtitles_url", &cur.SubtitlesURL, in.SubtitlesURL)
		oneOf("subtitles_mode", &cur.SubtitlesMode, in.SubtitlesMode, "", "core", "direct", "off")
		oneOf("seerr_mode", &cur.SeerrMode, in.SeerrMode, "", "core", "direct", "off")
		oneOf("sonarr_mode", &cur.SonarrMode, in.SonarrMode, "", "core", "direct", "off")
		oneOf("radarr_mode", &cur.RadarrMode, in.RadarrMode, "", "core", "direct", "off")
		oneOf("maintainerr_mode", &cur.MaintainerrMode, in.MaintainerrMode, "", "core", "direct", "off")
		url("subtitles_api_url", &cur.SubtitlesAPIURL, in.SubtitlesAPIURL)
		secret("subtitles_token", &cur.SubtitlesToken, in.SubtitlesToken)
		boxChanged = before != cur
		if boxChanged {
			forgetBoxServices()
		}
		if before.MaintainerrURL != cur.MaintainerrURL {
			_ = s.store.CacheDelete(r.Context(), "maintainerr:")
		}
	}
	if keys["ai_provider"] {
		if !validAIProvider(in.AIProvider) {
			writeErr(w, http.StatusBadRequest, "AI is automatic, Pocket AI or OpenAI")
			return
		}
		cur.AIProvider = in.AIProvider
	}
	if keys["ai_pocket_model"] {
		if !validPocketModel(in.AIPocketModel) {
			writeErr(w, http.StatusBadRequest, "unknown model")
			return
		}
		cur.AIPocketModel = in.AIPocketModel
	}
	secret("openai_api_key", &cur.OpenAIKey, in.OpenAIKey)
	text("openai_model", &cur.OpenAIModel, in.OpenAIModel)
	url("openai_base_url", &cur.OpenAIBaseURL, in.OpenAIBaseURL)
	if err := s.store.SaveUserSettings(r.Context(), me.ID, cur); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	if isAdmin && (keys["title"] || boxChanged) {
		if err := s.store.SaveBoxSettings(r.Context(), cur); err != nil {
			s.fail(w, err, "save settings")
			return
		}
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), me, cur))
}

// handleIntegrationTest checks a connection with the caller's own saved
// settings (or the URL/key typed into the form).
func (s *Server) handleIntegrationTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Which  string `json:"which"`
		URL    string `json:"url"`
		Key    string `json:"key"`
		Format string `json:"format"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	u := userFrom(r.Context())
	st, _ := s.store.GetSettings(r.Context(), u.ID)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	keyOr := func(saved string) string {
		if strings.Contains(in.Key, "••") || in.Key == "" {
			return saved
		}
		return in.Key
	}
	urlOr := func(saved string) string {
		if strings.TrimSpace(in.URL) == "" {
			return saved
		}
		return strings.TrimRight(strings.TrimSpace(in.URL), "/")
	}
	var msg string
	var err error
	switch in.Which {
	case "subtitles":
		if u.Role != "admin" {
			writeErr(w, http.StatusForbidden, "only an admin can do that")
			return
		}
		var sc *subsClient
		if in.URL != "" || (in.Key != "" && !strings.Contains(in.Key, "••")) {
			box, _ := s.store.GetSettings(ctx, 0)
			sc = &subsClient{base: urlOr(box.SubtitlesAPIURL), token: keyOr(box.SubtitlesToken), via: "direct", http: s.http}
		} else {
			sc, err = s.subsFor(ctx, u)
		}
		if err == nil {
			var me struct {
				Email string `json:"email"`
			}
			if err = sc.do(ctx, http.MethodGet, "/auth/me", nil, &me); err == nil {
				how := "with this box's token"
				if sc.via == "core" {
					how = "as you, through Pocket Core"
				}
				msg = "Pocket Subtitles answers " + how + " (" + me.Email + ")"
			}
		}
	case "seerr", "sonarr", "radarr", "maintainerr":
		if u.Role != "admin" {
			writeErr(w, http.StatusForbidden, "only an admin can do that")
			return
		}
		box, _ := s.store.GetSettings(ctx, 0)
		// typed into the form: test exactly that; else whatever is in use now (Core or the saved address)
		dBase, _, dKey, _ := boxDirect(box, in.Which)
		target := &boxSvc{base: urlOr(dBase), key: keyOr(dKey), via: "direct"}
		if strings.TrimSpace(in.URL) == "" && (in.Key == "" || strings.Contains(in.Key, "••")) {
			forgetBoxServices()
			if target, err = s.boxService(ctx, in.Which); err != nil {
				break
			}
		}
		how := " (directly)"
		if target.via == "core" {
			how = " (through Pocket Core)"
		}
		switch in.Which {
		case "seerr":
			c := &seerr{base: target.base, key: target.key, http: s.http}
			var st struct {
				Version string `json:"version"`
			}
			if err = c.do(ctx, http.MethodGet, "/settings/main", "", nil, nil); err == nil {
				_ = c.do(ctx, http.MethodGet, "/status", "", nil, &st)
				msg = "Jellyseerr " + st.Version + " answers and accepts the key" + how
			}
		case "sonarr", "radarr":
			var st struct {
				Version string `json:"version"`
				AppName string `json:"appName"`
			}
			if err = s.arrGet(ctx, target.base, target.key, "/system/status", nil, &st); err == nil {
				msg = fmt.Sprintf("%s %s answers%s", firstNonEmpty(st.AppName, in.Which), st.Version, how)
			}
		case "maintainerr":
			var cols []json.RawMessage
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target.base+"/api/collections", nil)
			var res *http.Response
			if res, err = s.http.Do(req); err == nil {
				err = json.NewDecoder(res.Body).Decode(&cols)
				res.Body.Close()
				msg = fmt.Sprintf("Maintainerr answers: %d collections%s", len(cols), how)
			}
		}
	case "openai":
		c := ai.New(urlOr(st.OpenAIBaseURL), keyOr(st.OpenAIKey), st.OpenAIModel, 20*time.Second)
		var ids []string
		if ids, err = c.Models(ctx); err == nil {
			msg = fmt.Sprintf("The key works (%d models)", len(ids))
		}
	case "notify":
		f := in.Format
		if f == "" {
			f = st.NotifyFormat
		}
		n := notify.New(urlOr(st.NotifyURL), f)
		err = n.Send(ctx, notify.Message{Title: "Pocket Watch", Body: "Notifications work. Requests that arrive and your weekly recap will come here.", Priority: "info", Tags: []string{"tv"}})
		msg = "Test message sent via " + n.Format
	default:
		writeErr(w, http.StatusBadRequest, "unknown integration")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var size int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "data_dir": s.cfg.DataDir, "db_path": s.cfg.DBPath, "db_bytes": size,
		"uptime": time.Since(s.started).Round(time.Second).String(), "timezone": time.Now().Location().String(),
		"counts": s.store.CountsFor(r.Context(), actorID(r.Context())), "jellyfin_url": s.cfg.JellyfinURL,
	})
}

// handleBackup downloads the whole database, so it is a box operation for an
// admin only (everyone can export their own data as JSON).
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "backups")
	name := fmt.Sprintf("pocket-watch-%s.db", time.Now().Format("20060102-150405"))
	dst := filepath.Join(dir, name)
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, dst)
}

// exportDoc is the JSON export of one person's own data: history and
// watchlist (the library itself lives in Jellyfin).
type exportDoc struct {
	App       string                `json:"app"`
	Version   string                `json:"version"`
	Exported  string                `json:"exported_at"`
	Watches   []store.Watch         `json:"watches"`
	Watchlist []store.WatchlistItem `json:"watchlist"`
}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	doc := exportDoc{App: "pocket-watch", Version: Version, Exported: store.Now()}
	doc.Watches, _, _ = s.store.Watches(ctx, uid, store.WatchFilter{Limit: -1})
	doc.Watchlist, _ = s.store.Watchlist(ctx, uid)
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-watch-export.json"`)
	writeJSON(w, http.StatusOK, doc)
}

// handleImportJSON adds an export to the caller's own data (rows already there are kept).
func (s *Server) handleImportJSON(w http.ResponseWriter, r *http.Request) {
	var doc exportDoc
	if err := decode(r, &doc); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx := r.Context()
	uid := actorID(ctx)
	counts := map[string]int{}
	n, err := s.store.AddWatches(ctx, uid, doc.Watches)
	if err != nil {
		s.fail(w, err, "import history")
		return
	}
	counts["watches"] = n
	for _, x := range doc.Watchlist {
		x.ID = 0
		if (x.ItemID != "" || x.TMDBID > 0) && s.store.AddToWatchlist(ctx, uid, &x) == nil {
			counts["watchlist"]++
		}
	}
	writeJSON(w, http.StatusOK, counts)
}
