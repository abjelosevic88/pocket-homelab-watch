package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-watch/internal/jellyfin"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// ---- reading history from Jellyfin ----
//
// Pocket Watch's own player writes history as it plays. Everything watched in
// other Jellyfin apps (the TV, Infuse, Swiftfin, the web client) is read from
// the person's Jellyfin state every SYNC_EVERY: titles whose "last played"
// moved since the last look. The first look copies everything the account
// has ever finished, each on the day it was last played.

func (s *Server) isSyncing(uid int64) bool {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return s.syncing[uid]
}

// historySoon reads one person's history in the background.
func (s *Server) historySoon(uid int64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := s.syncUser(ctx, uid); err != nil && !isNoJellyfin(err) {
			s.log.Warn("history", "user", uid, "err", err)
		}
	}()
}

func (s *Server) syncAll(ctx context.Context) {
	ids, err := s.store.ConnectedUsers(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		if err := s.syncUser(cctx, id); err != nil && !isNoJellyfin(err) {
			s.log.Debug("history", "user", id, "err", err)
		}
		cancel()
	}
}

// syncUser reads what changed in one person's Jellyfin since the last look.
func (s *Server) syncUser(ctx context.Context, uid int64) error {
	s.syncMu.Lock()
	if s.syncing[uid] {
		s.syncMu.Unlock()
		return nil
	}
	s.syncing[uid] = true
	s.syncMu.Unlock()
	defer func() {
		s.syncMu.Lock()
		delete(s.syncing, uid)
		s.syncMu.Unlock()
	}()
	u, err := s.store.GetUser(ctx, uid)
	if err != nil {
		return err
	}
	jc, err := s.jfFor(ctx, u)
	if err != nil {
		return err
	}
	settings, _ := s.store.GetSettings(ctx, uid)
	st := s.store.GetSync(ctx, uid)
	if st.ServerKey != jc.key {
		// another server or account: start over (rows already kept stay)
		st = store.SyncState{ServerKey: jc.key}
	}
	genres := map[string][]string{}
	seriesGenres := func(id string) []string {
		if g, ok := genres[id]; ok {
			return g
		}
		g := s.seriesGenres(ctx, jc, id)
		genres[id] = g
		return g
	}
	mark := st.Watermark
	// Marking a season or a whole show watched stamps every episode with the
	// same second: those count as watched, but not as time spent watching.
	bulk := map[string]int{}
	count := func(items []jellyfin.Item) {
		for _, it := range items {
			if it.UserData != nil && it.UserData.Played && len(it.UserData.LastPlayedDate) >= 19 {
				bulk[it.UserData.LastPlayedDate[:19]]++
			}
		}
	}
	take := func(it *jellyfin.Item, source string) {
		ud := it.UserData
		if ud == nil || ud.LastPlayedDate == "" {
			return
		}
		at := normTime(ud.LastPlayedDate)
		if at <= st.Watermark {
			return
		}
		x := watchFromItem(it, at, source)
		if it.Type == "Episode" && it.SeriesID != "" {
			x.Genres = seriesGenres(it.SeriesID)
		}
		x.Day = store.LocalDay(at)
		if ud.Played {
			x.Completed, x.Position, x.Seconds = true, x.Runtime, x.Runtime
			if len(ud.LastPlayedDate) >= 19 && bulk[ud.LastPlayedDate[:19]] >= 3 {
				x.Seconds = 0
			}
		} else {
			x.Position = int(ud.PlaybackPositionTicks / jellyfin.Ticks)
			x.Seconds = x.Position - s.store.PrevPosition(ctx, uid, x.ItemID, x.Day)
			if x.Seconds < 0 {
				x.Seconds = 0
			}
		}
		if x.Seconds < 60 && !x.Completed {
			return
		}
		added, err := s.store.UpsertWatch(ctx, uid, &x)
		if err != nil {
			s.log.Debug("history row", "err", err)
		}
		if added && source == "jellyfin" && x.Completed {
			p := map[string]any{"item_id": x.ItemID, "kind": x.Kind, "title": x.Title, "year": x.Year, "runtime": x.Runtime, "elsewhere": true}
			if x.Kind == "episode" {
				p["series"], p["season"], p["episode"] = x.Series, x.Season, x.Episode
			}
			s.publish(ctx, u, "watch.watched", x.ItemID, p)
		}
		if at > mark {
			mark = at
		}
	}
	fields := "Genres,ProviderIds,PremiereDate,ProductionYear"
	if !st.Backfilled {
		for start := 0; ; start += 500 {
			res, err := jc.c.Items(ctx, url.Values{"IncludeItemTypes": {"Movie,Episode"}, "Recursive": {"true"}, "Filters": {"IsPlayed"}, "SortBy": {"DatePlayed"}, "SortOrder": {"Descending"},
				"Fields": {fields}, "EnableImageTypes": {"Primary"}, "StartIndex": {strconv.Itoa(start)}, "Limit": {"500"}})
			if err != nil {
				st.Error = err.Error()
				_ = s.store.SaveSync(ctx, uid, st)
				return err
			}
			count(res.Items)
			for i := range res.Items {
				take(&res.Items[i], "backfill")
			}
			if len(res.Items) < 500 {
				break
			}
		}
		st.Backfilled = true
	} else if settings.HistoryAll {
		for _, f := range []string{"IsPlayed", "IsResumable"} {
			res, err := jc.c.Items(ctx, url.Values{"IncludeItemTypes": {"Movie,Episode"}, "Recursive": {"true"}, "Filters": {f}, "SortBy": {"DatePlayed"}, "SortOrder": {"Descending"},
				"Fields": {fields}, "EnableImageTypes": {"Primary"}, "Limit": {"100"}})
			if err != nil {
				st.Error = err.Error()
				_ = s.store.SaveSync(ctx, uid, st)
				return err
			}
			count(res.Items)
			for i := range res.Items {
				take(&res.Items[i], "jellyfin")
			}
		}
	}
	st.Watermark, st.SyncedAt, st.Error = mark, store.Now(), ""
	if err := s.store.SaveSync(ctx, uid, st); err != nil {
		return err
	}
	if s.cfg.DemoData && uid == s.store.OwnerID(ctx) {
		s.demoHistory(ctx, uid, jc)
	}
	return nil
}

// normTime turns Jellyfin's 7-digit fractions into a sortable RFC 3339 UTC time.
func normTime(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	uid := actorID(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"sync": s.store.GetSync(r.Context(), uid), "syncing": s.isSyncing(uid)})
}

// handleSyncNow reads history now (full=1 copies everything again).
func (s *Server) handleSyncNow(w http.ResponseWriter, r *http.Request) {
	uid := actorID(r.Context())
	if queryBool(r, "full") {
		st := s.store.GetSync(r.Context(), uid)
		st.Backfilled, st.Watermark = false, ""
		_ = s.store.SaveSync(r.Context(), uid, st)
	}
	s.historySoon(uid)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

// ---- history, day, stats ----

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.WatchFilter{From: q.Get("from"), To: q.Get("to"), Kind: q.Get("kind"), SeriesID: q.Get("series"), Q: q.Get("q"), Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset")}
	if !validDate(f.From) || !validDate(f.To) {
		writeErr(w, http.StatusBadRequest, "dates are YYYY-MM-DD")
		return
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	ws, total, err := s.store.Watches(r.Context(), actorID(r.Context()), f)
	if err != nil {
		s.fail(w, err, "history")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": decorate(ws), "total": total})
}

// watchOut is a history row with its pictures.
type watchOut struct {
	store.Watch
	Poster string `json:"poster"`
	Thumb  string `json:"thumb"`
}

func decorate(ws []store.Watch) []watchOut {
	out := make([]watchOut, 0, len(ws))
	for _, x := range ws {
		o := watchOut{Watch: x, Thumb: "/api/v1/img/" + x.ItemID + "/Primary"}
		if x.Kind == "episode" && x.SeriesID != "" {
			o.Poster = "/api/v1/img/" + x.SeriesID + "/Primary"
		} else {
			o.Poster = o.Thumb
			o.Thumb = "/api/v1/img/" + x.ItemID + "/Backdrop"
		}
		out = append(out, o)
	}
	return out
}

func (s *Server) handleDeleteWatch(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteWatch(r.Context(), actorID(r.Context()), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDay is what the person watched on one day (Pocket Day reads it through an app link).
func (s *Server) handleDay(w http.ResponseWriter, r *http.Request) {
	d := chi.URLParam(r, "date")
	if !validDate(d) || d == "" {
		writeErr(w, http.StatusBadRequest, "date is YYYY-MM-DD")
		return
	}
	ws, _, err := s.store.Watches(r.Context(), actorID(r.Context()), store.WatchFilter{From: d, To: d, Limit: -1})
	if err != nil {
		s.fail(w, err, "day")
		return
	}
	sec, movies, eps := 0, 0, 0
	for _, x := range ws {
		sec += x.Seconds
		if x.Kind == "movie" {
			movies++
		} else {
			eps++
		}
	}
	sort.Slice(ws, func(i, j int) bool { return ws[i].WatchedAt < ws[j].WatchedAt })
	writeJSON(w, http.StatusOK, map[string]any{"date": d, "seconds": sec, "movies": movies, "episodes": eps, "items": decorate(ws), "summary": daySummary(ws)})
}

// daySummary says the day in one line: "Dune: Part Two and 3 episodes of Severance".
func daySummary(ws []store.Watch) string {
	var films []string
	shows := map[string]int{}
	var order []string
	for _, x := range ws {
		if x.Kind == "movie" {
			films = append(films, x.Title)
			continue
		}
		if shows[x.Series] == 0 {
			order = append(order, x.Series)
		}
		shows[x.Series]++
	}
	var parts []string
	parts = append(parts, films...)
	for _, sh := range order {
		if n := shows[sh]; n == 1 {
			parts = append(parts, "an episode of "+sh)
		} else {
			parts = append(parts, fmt.Sprintf("%d episodes of %s", n, sh))
		}
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

type countRow struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Count   int    `json:"count"`
	Seconds int    `json:"seconds"`
	Image   string `json:"image,omitempty"`
}

// handleStats sums a period up: time watched, by week or month, top shows and
// genres, when in the week and day, streaks.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	rng := r.URL.Query().Get("range")
	today := store.Today()
	from := ""
	switch rng {
	case "7d":
		from = store.AddDays(today, -6)
	case "30d", "":
		rng, from = "30d", store.AddDays(today, -29)
	case "90d":
		from = store.AddDays(today, -89)
	case "365d":
		from = store.AddDays(today, -364)
	case "all":
	default:
		if y, err := strconv.Atoi(rng); err == nil && y > 1900 && y < 3000 {
			from = fmt.Sprintf("%d-01-01", y)
			today = fmt.Sprintf("%d-12-31", y)
		} else {
			writeErr(w, http.StatusBadRequest, "range is 7d, 30d, 90d, 365d, all or a year")
			return
		}
	}
	ws, _, err := s.store.Watches(ctx, uid, store.WatchFilter{From: from, To: today, Limit: -1})
	if err != nil {
		s.fail(w, err, "stats")
		return
	}
	writeJSON(w, http.StatusOK, statsOf(ws, rng, from, today))
}

func statsOf(ws []store.Watch, rng, from, to string) map[string]any {
	tot := map[string]int{"titles": len(ws), "movies": 0, "episodes": 0, "seconds": 0, "movie_seconds": 0, "episode_seconds": 0, "completed": 0}
	shows := map[string]*countRow{}
	genres := map[string]*countRow{}
	days := map[string]int{}
	weekday := make([]int, 7)
	hours := make([]int, 24)
	for _, x := range ws {
		tot["seconds"] += x.Seconds
		if x.Completed {
			tot["completed"]++
		}
		if x.Kind == "movie" {
			tot["movies"]++
			tot["movie_seconds"] += x.Seconds
		} else {
			tot["episodes"]++
			tot["episode_seconds"] += x.Seconds
			k := firstNonEmpty(x.SeriesID, x.Series)
			if shows[k] == nil {
				shows[k] = &countRow{Key: x.SeriesID, Name: x.Series, Image: "/api/v1/img/" + x.SeriesID + "/Primary"}
			}
			shows[k].Count++
			shows[k].Seconds += x.Seconds
		}
		for _, g := range x.Genres {
			if genres[g] == nil {
				genres[g] = &countRow{Key: g, Name: g}
			}
			genres[g].Count++
			genres[g].Seconds += x.Seconds
		}
		days[x.Day] += x.Seconds
		if t, err := time.Parse(time.RFC3339Nano, x.WatchedAt); err == nil {
			lt := t.Local()
			weekday[(int(lt.Weekday())+6)%7] += x.Seconds
			hours[lt.Hour()] += x.Seconds
		}
	}
	top := func(m map[string]*countRow, n int) []countRow {
		out := make([]countRow, 0, len(m))
		for _, v := range m {
			out = append(out, *v)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Seconds != out[j].Seconds {
				return out[i].Seconds > out[j].Seconds
			}
			return out[i].Name < out[j].Name
		})
		if len(out) > n {
			out = out[:n]
		}
		return out
	}
	// time series: days for a month or less, weeks up to a quarter, months beyond
	type point struct {
		Key     string `json:"key"`
		Label   string `json:"label"`
		Seconds int    `json:"seconds"`
	}
	var series []point
	start := from
	if start == "" {
		start = to
		for d := range days {
			if d < start {
				start = d
			}
		}
	}
	t0, _ := store.ParseDate(start)
	t1, _ := store.ParseDate(to)
	span := int(t1.Sub(t0).Hours()/24) + 1
	switch {
	case span <= 31:
		for d := t0; !d.After(t1); d = d.AddDate(0, 0, 1) {
			k := d.Format("2006-01-02")
			series = append(series, point{Key: k, Label: d.Format("2"), Seconds: days[k]})
		}
	case span <= 120:
		for d := t0; !d.After(t1); d = d.AddDate(0, 0, 7) {
			sum := 0
			for i := 0; i < 7; i++ {
				sum += days[d.AddDate(0, 0, i).Format("2006-01-02")]
			}
			series = append(series, point{Key: d.Format("2006-01-02"), Label: d.Format("Jan 2"), Seconds: sum})
		}
	default:
		m := time.Date(t0.Year(), t0.Month(), 1, 0, 0, 0, 0, time.UTC)
		for !m.After(t1) {
			sum := 0
			for d, v := range days {
				if strings.HasPrefix(d, m.Format("2006-01")) {
					sum += v
				}
			}
			series = append(series, point{Key: m.Format("2006-01"), Label: m.Format("Jan"), Seconds: sum})
			m = m.AddDate(0, 1, 0)
		}
	}
	// streaks over the days watched
	var dl []string
	for d := range days {
		dl = append(dl, d)
	}
	sort.Strings(dl)
	longest, run := 0, 0
	prev := ""
	for _, d := range dl {
		if prev != "" && store.AddDays(prev, 1) == d {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
		prev = d
	}
	current := 0
	d := store.Today()
	if days[d] == 0 {
		d = store.AddDays(d, -1) // nothing yet today doesn't break the streak
	}
	for days[d] > 0 {
		current++
		d = store.AddDays(d, -1)
	}
	return map[string]any{"range": rng, "from": from, "to": to, "totals": tot, "days_active": len(days), "series": series,
		"top_shows": top(shows, 10), "top_genres": top(genres, 10), "weekday": weekday, "hours": hours,
		"streak": map[string]int{"longest": longest, "current": current}}
}

// handleDashboard is a compact summary (Homepage widgets, Pocket Day, Pocket Hub).
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	today := store.Today()
	week, _, _ := s.store.Watches(ctx, uid, store.WatchFilter{From: store.AddDays(today, -6), To: today, Limit: -1})
	sec, todaySec := 0, 0
	for _, x := range week {
		sec += x.Seconds
		if x.Day == today {
			todaySec += x.Seconds
		}
	}
	out := map[string]any{"today_seconds": todaySec, "week_seconds": sec, "week_titles": len(week)}
	if len(week) > 0 {
		out["last"] = decorate(week[:1])[0]
	}
	var now []map[string]any
	s.playMu.Lock()
	for _, ps := range s.plays {
		if ps.uid == uid && time.Since(ps.lastAt) < 2*time.Minute {
			now = append(now, map[string]any{"item_id": ps.itemID, "title": ps.item.Title, "series": ps.item.Series, "position": ps.lastPos, "runtime": ps.item.Runtime, "paused": ps.paused, "device": ps.device})
		}
	}
	s.playMu.Unlock()
	out["now"] = nz(now)
	reqs, _ := s.store.Requests(ctx, uid)
	open := 0
	for _, rq := range reqs {
		if rq.Status != "available" && rq.Status != "declined" && rq.Status != "failed" {
			open++
		}
	}
	out["open_requests"] = open
	writeJSON(w, http.StatusOK, out)
}

// ---- weekly recap ----

func (s *Server) weeklyRecaps(ctx context.Context) {
	now := time.Now()
	if now.Weekday() != time.Monday || now.Hour() < 9 || now.Hour() > 11 {
		return
	}
	y, wk := now.ISOWeek()
	ids, _ := s.store.SettingsUsers(ctx)
	for _, uid := range ids {
		st, _ := s.store.GetSettings(ctx, uid)
		if !st.NotifyWeekly || st.NotifyURL == "" {
			continue
		}
		key := fmt.Sprintf("recap:%d:%d-%d", uid, y, wk)
		if _, _, done := s.store.CacheGet(ctx, key); done {
			continue
		}
		_ = s.store.CachePut(ctx, key, "1")
		to := store.AddDays(store.Today(), -1)
		ws, _, _ := s.store.Watches(ctx, uid, store.WatchFilter{From: store.AddDays(to, -6), To: to, Limit: -1})
		if len(ws) == 0 {
			continue
		}
		stats := statsOf(ws, "7d", store.AddDays(to, -6), to)
		tot := stats["totals"].(map[string]int)
		body := fmt.Sprintf("%s watched: %d films, %d episodes.", longDuration(tot["seconds"]), tot["movies"], tot["episodes"])
		if shows := stats["top_shows"].([]countRow); len(shows) > 0 {
			body += " Most: " + shows[0].Name + "."
		}
		n := notify.New(st.NotifyURL, st.NotifyFormat)
		if err := n.Send(ctx, notify.Message{Title: "Your week in Pocket Watch", Body: body, Priority: "info", Tags: []string{"tv"}, Link: s.cfg.BaseURL + "/stats"}); err != nil {
			s.log.Warn("weekly recap", "user", uid, "err", err)
		}
	}
}

func longDuration(sec int) string {
	h, m := sec/3600, (sec%3600)/60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dm", m)
}
