package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/jellyfin"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// The three routes behind `capabilities` (links.go): /api/v1/pocket/search,
// /day/{date} and /widget, for Pocket Search, Pocket Day and Pocket Hub. Each
// runs as the request's person and reads only what is theirs; the SDK's
// handlers check and cap what goes in and out. None of them requests,
// downloads or removes a title, starts playback or runs a clean-up rule.
//
// The day is read from the watch history kept here (what GET /history and
// /day/{date} read). The search and the widget need the library, which is in
// Jellyfin: they ask it as GET /search and GET /home do, with the person's
// own Jellyfin account (jfFor: their sign-in here, or the login Pocket Core
// keeps for them) and under the same limits. Someone with no working
// Jellyfin account gets an empty answer, not an error. The widget's
// Jellyfin part is kept for a few minutes per person.

const (
	dayRows       = 200             // titles of one day read (the most a day's answer holds)
	widgetRows    = 8               // rows the widget shows
	widgetAdded   = 3               // of them kept for what was added lately, when there is some
	widgetFresh   = 3 * time.Minute // how long the widget's Jellyfin part is kept
	widgetCacheKV = "pocket-widget:"
)

// titleURL opens a film, show or episode in the app.
func titleURL(id string) string { return "/title/" + id }

// capKind is a title's kind in the capability answers: film, series, episode.
func capKind(t string) string {
	if t == "movie" {
		return "film"
	}
	return t
}

// dots joins the parts of a line that are there: "2024 · 2h 46m · Watched".
func dots(parts ...string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// count is a number with its word: "1 film", "3 films"; nothing for none.
func count(n int, one, many string) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// length is a running time as the pages show it: "1h 52m", "48m".
func length(sec int) string {
	if sec <= 0 {
		return ""
	}
	h, m := sec/3600, int(math.Round(float64(sec%3600)/60))
	if m == 60 {
		h, m = h+1, 0
	}
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dm", m)
}

// timeLeft is what remains of a film or episode started: "23m left".
func timeLeft(t Title) string {
	if t.Runtime <= 0 || t.Position <= 0 || t.Position >= t.Runtime {
		return ""
	}
	if left := t.Runtime - t.Position; left >= 60 {
		return length(left) + " left"
	}
	return "under a minute left"
}

// noAccount reports an answer that means the person has no working Jellyfin
// account here: no way in at all, or one Jellyfin no longer accepts.
func noAccount(err error) bool { return isNoJellyfin(err) || jellyfin.IsAuth(err) }

// ---------- search ----------

// hasWord reports whether there is a letter or a digit to look for.
func hasWord(q string) bool {
	return strings.IndexFunc(q, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0
}

// nameScore says how well a title answers the words: it is the title (3),
// the title starts with them (2), Jellyfin found it some other way (1).
func nameScore(name, q string) float64 {
	n, w := strings.ToLower(strings.Join(strings.Fields(name), " ")), strings.ToLower(strings.Join(strings.Fields(q), " "))
	switch {
	case n == w:
		return 3
	case strings.HasPrefix(n, w):
		return 2
	}
	return 1
}

// watchedWords says how far the person is with a film or an episode.
func watchedWords(t Title) string {
	switch {
	case t.Played:
		return "Watched"
	case t.Progress > 0:
		return fmt.Sprintf("%d%% watched", int(math.Round(t.Progress)))
	}
	return ""
}

// searchSnippet is a title's line in Pocket Search: year, length, and
// watched or how far ("2024 · 2h 46m · 40% watched"); a show's years and
// episodes, an episode's show and number first.
func searchSnippet(t Title) string {
	yr := ""
	if t.Year > 0 {
		yr = strconv.Itoa(t.Year)
	}
	switch t.Type {
	case "series":
		switch {
		case t.EndYear > 0 && t.EndYear != t.Year && yr != "":
			yr += "–" + strconv.Itoa(t.EndYear)
		case t.Status == "Continuing" && yr != "":
			yr += "–"
		}
		left := ""
		if t.Played {
			left = "Watched"
		} else if t.Unplayed != nil && *t.Unplayed > 0 && t.ChildCount > 0 && *t.Unplayed < t.ChildCount {
			left = count(*t.Unplayed, "episode left", "episodes left")
		}
		return dots(yr, count(t.ChildCount, "episode", "episodes"), left)
	case "episode":
		code := ""
		if t.Season != nil && t.Episode != nil {
			code = episodeCode(*t.Season, *t.Episode)
		}
		return dots(t.SeriesName, code, yr, length(t.Runtime), watchedWords(t))
	}
	return dots(yr, length(t.Runtime), watchedWords(t))
}

// pocketSearch answers Pocket Search with the films, shows and episodes GET
// /search finds in the person's library (the same Jellyfin lookup, with
// their own account), as one list: the title that answers the words best
// first, and of equally good ones the show, then the film, then the episode.
// People are left out, and nothing outside the library is looked up.
func (s *Server) pocketSearch(r *http.Request, q string, limit int) ([]core.SearchItem, error) {
	ctx, u := r.Context(), userFrom(r.Context())
	if u == nil || !hasWord(q) {
		return nil, nil
	}
	jc, err := s.jfFor(ctx, u)
	if noAccount(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	found := searchLibrary(ctx, jc, q, "series", "movies", "episodes")
	bonus := map[string]float64{"series": 0.2, "movies": 0.1, "episodes": 0}
	var out []core.SearchItem
	for _, key := range []string{"series", "movies", "episodes"} {
		for _, t := range found[key] {
			kind := capKind(t.Type)
			out = append(out, core.SearchItem{ID: kind + ":" + t.ID, Title: t.Name, Kind: kind, Snippet: searchSnippet(t),
				Date: t.Premiere, URL: titleURL(t.ID), Score: nameScore(t.Name, q) + bonus[key]})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---------- day ----------

// sameZone reports whether two zones keep the same clock all through the day starting at d.
func sameZone(d time.Time, a, b *time.Location) bool {
	for _, t := range []time.Time{d, d.AddDate(0, 0, 1).Add(-time.Second)} {
		_, oa := t.In(a).Zone()
		_, ob := t.In(b).Zone()
		if oa != ob {
			return false
		}
	}
	return true
}

// daySentence says a day of watching in one line: "Watched 2 episodes of
// Severance and a film". A day of one film names it.
func daySentence(ws []store.Watch) string {
	var films []string
	shows := map[string]int{}
	var order []string
	eps := 0
	for _, x := range ws {
		if x.Kind == "movie" {
			films = append(films, x.Title)
			continue
		}
		if shows[x.Series] == 0 {
			order = append(order, x.Series)
		}
		shows[x.Series]++
		eps++
	}
	if len(films) == 1 && eps == 0 {
		return "Watched " + films[0]
	}
	var parts []string
	if len(order) <= 2 {
		for _, sh := range order {
			n := shows[sh]
			what := "an episode"
			if n > 1 {
				what = fmt.Sprintf("%d episodes", n)
			}
			if sh != "" {
				what += " of " + sh
			}
			parts = append(parts, what)
		}
	} else {
		parts = append(parts, fmt.Sprintf("%d episodes of %d shows", eps, len(order)))
	}
	switch len(films) {
	case 0:
	case 1:
		parts = append(parts, "a film")
	default:
		parts = append(parts, fmt.Sprintf("%d films", len(films)))
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return "Watched " + parts[0]
	}
	return "Watched " + strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// pocketDay answers Pocket Day from the watch history kept here: one item per
// film or episode watched that day, with the minutes it got, its show and
// number, when it was last watched and whether it was finished. The day is
// the one GET /day/{date} shows, unless the caller's time zone keeps another
// clock than the server's: then it is that zone's day. Only that day's rows
// are read, never the whole history, and Jellyfin is not asked.
func (s *Server) pocketDay(r *http.Request, date string, tz *time.Location) (*core.Day, error) {
	ctx, uid := r.Context(), actorID(r.Context())
	loc := time.Local
	if tz != nil {
		loc = tz
	}
	d, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return nil, err
	}
	var ws []store.Watch
	if sameZone(d, time.Local, loc) {
		ws, _, err = s.store.Watches(ctx, uid, store.WatchFilter{From: date, To: date, Limit: dayRows})
	} else {
		const f = "2006-01-02T15:04:05"
		ws, err = s.store.WatchesBetween(ctx, uid, d.UTC().Format(f), d.AddDate(0, 0, 1).UTC().Format(f), dayRows)
	}
	if err != nil {
		return nil, err
	}
	if len(ws) == 0 {
		return nil, nil
	}
	sort.SliceStable(ws, func(i, j int) bool { return ws[i].WatchedAt < ws[j].WatchedAt })

	day := &core.Day{Date: date, Summary: daySentence(ws)}
	sec := 0
	for _, x := range ws {
		sec += x.Seconds
		it := core.DayItem{ID: "film:" + x.ItemID, Title: x.Title, Kind: "film", Minutes: math.Round(float64(x.Seconds)/60*100) / 100,
			Done: x.Completed, URL: titleURL(x.ItemID), Image: "/api/v1/img/" + x.ItemID + "/Primary"}
		if x.Year > 0 {
			it.Sub = strconv.Itoa(x.Year)
		}
		if x.Kind == "episode" {
			it.ID, it.Kind = "episode:"+x.ItemID, "episode"
			it.Sub = dots(x.Series, episodeCode(x.Season, x.Episode))
			if it.Title == "" {
				it.Title = x.Series
			}
			if x.SeriesID != "" {
				it.Image = "/api/v1/img/" + x.SeriesID + "/Primary"
			}
		}
		if t, err := time.Parse(time.RFC3339Nano, x.WatchedAt); err == nil {
			it.Time = t.In(loc).Format("15:04")
		}
		day.Items = append(day.Items, it)
	}
	if sec >= 60 {
		day.Metrics = append(day.Metrics, core.Metric{Label: "Time", Value: longDuration(sec)})
	}
	return day, nil
}

// ---------- widget ----------

// pocketWidget answers Pocket Hub with what the person is in the middle of
// and the next episode of the shows they watch, the most recently watched
// first, then what was added to the library lately. The line says what is
// playing in Pocket Watch right now, and nothing otherwise. The rows come
// from Jellyfin as GET /home reads them (Continue watching, Next up, the
// newest films and episodes), with the person's own account, and are kept
// for a few minutes per person. No account: nothing to show.
func (s *Server) pocketWidget(r *http.Request) (*core.WidgetData, error) {
	ctx, u := r.Context(), userFrom(r.Context())
	if u == nil {
		return nil, nil
	}
	rows, err := s.widgetRows(ctx, u)
	if err != nil {
		return nil, err
	}
	line := s.playingNow(u.ID)
	if len(rows) == 0 && line == "" {
		return nil, nil
	}
	return &core.WidgetData{Line: line, Items: rows, URL: "/"}, nil
}

// playingNow is "Playing now: Severance S02E05" while the person plays
// something in Pocket Watch (what GET /dashboard calls now), else nothing.
func (s *Server) playingNow(uid int64) string {
	s.playMu.Lock()
	defer s.playMu.Unlock()
	var best *playState
	for _, ps := range s.plays {
		if ps.uid == uid && !ps.paused && time.Since(ps.lastAt) < 2*time.Minute && (best == nil || ps.lastAt.After(best.lastAt)) {
			best = ps
		}
	}
	if best == nil {
		return ""
	}
	if x := best.item; x.Kind == "episode" && x.Series != "" {
		return "Playing now: " + x.Series + " " + episodeCode(x.Season, x.Episode)
	}
	return "Playing now: " + best.item.Title
}

type widgetRow struct {
	core.WidgetItem
	series string // the show a row is about, so it is listed once
	at     string // when it was last watched, or added
}

// widgetRows are the widget's rows, from the cache while it is fresh.
func (s *Server) widgetRows(ctx context.Context, u *store.User) ([]core.WidgetItem, error) {
	key := widgetCacheKV + strconv.FormatInt(u.ID, 10)
	if v, at, ok := s.store.CacheGet(ctx, key); ok && fresh(at, widgetFresh) {
		var rows []core.WidgetItem
		if json.Unmarshal([]byte(v), &rows) == nil {
			return rows, nil
		}
	}
	jc, err := s.jfFor(ctx, u)
	if noAccount(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var (
		mu                  sync.Mutex
		wg                  sync.WaitGroup
		resume, next, added []Title
		firstErr            error
		answered            int
	)
	got := func(dst *[]Title, items []jellyfin.Item, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return
		}
		answered++
		*dst = append(*dst, views(items)...)
	}
	run := func(fn func()) { wg.Add(1); go func() { defer wg.Done(); fn() }() }
	run(func() {
		res, err := jc.c.Resume(ctx, widgetRows)
		if err != nil {
			got(&resume, nil, err)
			return
		}
		got(&resume, res.Items, nil)
	})
	run(func() {
		res, err := jc.c.NextUp(ctx, "", widgetRows, false)
		if err != nil {
			got(&next, nil, err)
			return
		}
		got(&next, res.Items, nil)
	})
	run(func() {
		items, err := jc.c.Latest(ctx, "Movie", widgetRows)
		got(&added, items, err)
	})
	run(func() {
		items, err := jc.c.Latest(ctx, "Episode", widgetRows)
		got(&added, items, err)
	})
	wg.Wait()
	if answered == 0 && firstErr != nil {
		if noAccount(firstErr) {
			return nil, nil
		}
		return nil, firstErr
	}

	// when each show was last watched here, to put Next up among Continue watching
	lastSeen := map[string]string{}
	if shows, err := s.store.WatchedSeries(ctx, u.ID, store.AddDays(store.Today(), -365)); err == nil {
		for _, sh := range shows {
			lastSeen[sh.SeriesID] = sh.WatchedAt
		}
	}
	var going []widgetRow
	for _, t := range resume {
		row := widgetRow{WidgetItem: core.WidgetItem{ID: capKind(t.Type) + ":" + t.ID, Title: t.Name, Detail: timeLeft(t), URL: titleURL(t.ID)}, at: normTime(t.LastPlayed)}
		if t.Type == "episode" {
			row.series = t.SeriesID
			row.Title = firstNonEmpty(t.SeriesName, t.Name)
			code := ""
			if t.Season != nil && t.Episode != nil {
				code = episodeCode(*t.Season, *t.Episode)
			}
			row.Detail = dots(code, timeLeft(t))
		}
		going = append(going, row)
	}
	for _, t := range next {
		which := "Next episode"
		if t.Season != nil && t.Episode != nil {
			which = "Next: " + episodeCode(*t.Season, *t.Episode)
		}
		going = append(going, widgetRow{WidgetItem: core.WidgetItem{ID: "episode:" + t.ID, Title: firstNonEmpty(t.SeriesName, t.Name), Detail: dots(which, t.Name), URL: titleURL(t.ID)},
			series: t.SeriesID, at: lastSeen[t.SeriesID]})
	}
	// most recently watched first; rows without a time keep Jellyfin's order after them
	sort.SliceStable(going, func(i, j int) bool { return going[i].at > going[j].at })

	var lately []widgetRow
	for _, t := range added {
		row := widgetRow{WidgetItem: core.WidgetItem{ID: capKind(t.Type) + ":" + t.ID, Title: t.Name, Detail: "New film", URL: titleURL(t.ID)}, at: normTime(t.Added)}
		switch t.Type {
		case "series":
			row.series = t.ID
			row.Detail = "New episodes"
			if t.Unplayed != nil && *t.Unplayed > 0 {
				row.Detail = count(*t.Unplayed, "new episode", "new episodes")
			}
		case "episode":
			row.series = t.SeriesID
			row.Title = firstNonEmpty(t.SeriesName, t.Name)
			row.Detail = "New episode"
			if t.Season != nil && t.Episode != nil {
				row.Detail = "New: " + episodeCode(*t.Season, *t.Episode)
			}
		}
		lately = append(lately, row)
	}
	sort.SliceStable(lately, func(i, j int) bool { return lately[i].at > lately[j].at })

	rows := []core.WidgetItem{}
	seen := map[string]bool{}
	take := func(row widgetRow) bool {
		k := row.ID
		if row.series != "" {
			k = "series:" + row.series
		}
		if seen[k] || seen[row.ID] {
			return false
		}
		seen[k], seen[row.ID] = true, true
		rows = append(rows, row.WidgetItem)
		return true
	}
	room := widgetRows - min(widgetAdded, len(lately))
	for _, row := range going {
		if len(rows) < room {
			take(row)
		}
	}
	for _, row := range lately {
		if len(rows) < widgetRows {
			take(row)
		}
	}
	if raw, err := json.Marshal(rows); err == nil {
		_ = s.store.CachePut(ctx, key, string(raw))
	}
	return rows, nil
}
