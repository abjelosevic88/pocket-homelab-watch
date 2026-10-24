package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-watch/internal/jellyfin"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// ---- home ----

// handleHome is the start page: what to continue, what's next, what's new,
// and what the person keeps for later.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	jc, err := s.jfFor(ctx, u)
	if err != nil {
		if isNoJellyfin(err) {
			writeJSON(w, http.StatusOK, map[string]any{"not_connected": err.Error()})
			return
		}
		s.fail(w, err, "jellyfin")
		return
	}
	out := map[string]any{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	put := func(k string, v any, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return
		}
		out[k] = v
	}
	run := func(fn func()) {
		wg.Add(1)
		go func() { defer wg.Done(); fn() }()
	}
	run(func() {
		res, err := jc.c.Resume(ctx, 16)
		if err != nil {
			put("continue", nil, err)
			return
		}
		put("continue", views(res.Items), nil)
	})
	run(func() {
		res, err := jc.c.NextUp(ctx, "", 20, false)
		if err != nil {
			put("next_up", nil, err)
			return
		}
		put("next_up", views(res.Items), nil)
	})
	run(func() {
		items, err := jc.c.Latest(ctx, "Movie", 20)
		put("latest_movies", views(items), err)
	})
	run(func() {
		items, err := jc.c.Latest(ctx, "Episode", 20)
		put("latest_shows", views(items), err)
	})
	run(func() {
		// films you haven't seen, a different handful every day
		res, err := jc.c.Items(ctx, url.Values{"IncludeItemTypes": {"Movie"}, "Recursive": {"true"}, "Filters": {"IsUnplayed"}, "SortBy": {"Random"}, "Limit": {"60"}})
		if err != nil {
			put("picks", nil, err)
			return
		}
		items := res.Items
		seed := int(time.Now().Unix() / 86400)
		sort.SliceStable(items, func(i, j int) bool { return hashMod(items[i].ID, seed) < hashMod(items[j].ID, seed) })
		if len(items) > 16 {
			items = items[:16]
		}
		put("picks", views(items), nil)
	})
	run(func() {
		// "because you watched": the last film finished, and things like it
		ws, _, _ := s.store.Watches(ctx, u.ID, store.WatchFilter{Kind: "movie", Limit: 5})
		for _, x := range ws {
			if !x.Completed {
				continue
			}
			res, err := jc.c.Similar(ctx, x.ItemID, 16)
			if err != nil || len(res.Items) == 0 {
				continue
			}
			put("because", map[string]any{"title": x.Title, "id": x.ItemID, "items": views(res.Items)}, nil)
			return
		}
	})
	wg.Wait()
	if len(out) == 0 && firstErr != nil {
		s.jfErr(w, firstErr)
		return
	}
	// what's on the watchlist and already here
	if wl, err := s.store.Watchlist(ctx, u.ID); err == nil {
		here := []store.WatchlistItem{}
		for _, x := range wl {
			if x.ItemID != "" {
				here = append(here, x)
			}
		}
		out["watchlist"] = here
	}
	if reqs, err := s.store.Requests(ctx, u.ID); err == nil {
		arrived := []store.Request{}
		cut := time.Now().AddDate(0, 0, -14).UTC().Format(time.RFC3339)
		for _, rq := range reqs {
			if rq.Status == "available" && rq.ArrivedAt >= cut {
				arrived = append(arrived, rq)
			}
		}
		out["arrived"] = arrived
	}
	today := store.Today()
	if ws, _, err := s.store.Watches(ctx, u.ID, store.WatchFilter{From: today, To: today, Limit: -1}); err == nil {
		sec := 0
		for _, x := range ws {
			sec += x.Seconds
		}
		out["today"] = map[string]any{"titles": len(ws), "seconds": sec}
	}
	if l := s.leavingSoon(ctx, jc); len(l) > 0 {
		ids := make([]string, 0, len(l))
		when := map[string]string{}
		for _, x := range l {
			ids, when[x.ID] = append(ids, x.ID), x.Leaving
		}
		if res, err := jc.c.Items(ctx, url.Values{"Ids": {strings.Join(ids, ",")}}); err == nil {
			ts := views(res.Items)
			for i := range ts {
				ts[i].Leaving = when[ts[i].ID]
			}
			sort.Slice(ts, func(i, j int) bool { return ts[i].Leaving < ts[j].Leaving })
			out["leaving"] = ts
		}
	}
	out["sync"] = s.store.GetSync(ctx, u.ID)
	writeJSON(w, http.StatusOK, out)
}

func hashMod(id string, seed int) uint32 {
	h := uint32(2166136261) ^ uint32(seed)
	for i := 0; i < len(id); i++ {
		h ^= uint32(id[i])
		h *= 16777619
	}
	return h
}

// ---- browse ----

var sortBy = map[string][2]string{
	"added":   {"DateCreated,SortName", "Descending"},
	"title":   {"SortName", "Ascending"},
	"year":    {"ProductionYear,PremiereDate,SortName", "Descending"},
	"score":   {"CommunityRating,SortName", "Descending"},
	"critics": {"CriticRating,SortName", "Descending"},
	"runtime": {"Runtime,SortName", "Ascending"},
	"played":  {"DatePlayed,SortName", "Descending"},
	"random":  {"Random", "Ascending"},
}

// handleBrowse lists films or shows with filters.
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	jc := s.jf(w, r)
	if jc == nil {
		return
	}
	qp := r.URL.Query()
	typ := "Movie"
	if qp.Get("type") == "series" {
		typ = "Series"
	}
	q := url.Values{"IncludeItemTypes": {typ}, "Recursive": {"true"}, "EnableTotalRecordCount": {"true"}}
	sb, ok := sortBy[qp.Get("sort")]
	if !ok {
		sb = sortBy["added"]
	}
	q.Set("SortBy", sb[0])
	q.Set("SortOrder", sb[1])
	if qp.Get("order") == "asc" {
		q.Set("SortOrder", "Ascending")
	} else if qp.Get("order") == "desc" {
		q.Set("SortOrder", "Descending")
	}
	var filters []string
	switch qp.Get("show") {
	case "unwatched":
		filters = append(filters, "IsUnplayed")
	case "watched":
		filters = append(filters, "IsPlayed")
	case "favorites":
		filters = append(filters, "IsFavorite")
	case "resumable":
		filters = append(filters, "IsResumable")
	}
	if len(filters) > 0 {
		q.Set("Filters", strings.Join(filters, ","))
	}
	if g := qp.Get("genre"); g != "" {
		q.Set("Genres", g)
	}
	if y := qp.Get("decade"); len(y) == 4 {
		if d, err := strconv.Atoi(y); err == nil {
			var ys []string
			for i := 0; i < 10; i++ {
				ys = append(ys, strconv.Itoa(d+i))
			}
			q.Set("Years", strings.Join(ys, ","))
		}
	}
	if t := strings.TrimSpace(qp.Get("q")); t != "" {
		q.Set("SearchTerm", t)
	}
	if l := qp.Get("letter"); l != "" {
		if l == "#" {
			q.Set("NameLessThan", "A")
		} else {
			q.Set("NameStartsWith", l)
		}
	}
	start, limit := queryInt(r, "start"), queryInt(r, "limit")
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	q.Set("StartIndex", strconv.Itoa(start))
	q.Set("Limit", strconv.Itoa(limit))
	res, err := jc.c.Items(r.Context(), q)
	if err != nil {
		s.jfErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": views(res.Items), "total": res.TotalRecordCount, "start": start})
}

// handleGenres lists the genres of films or shows.
func (s *Server) handleGenres(w http.ResponseWriter, r *http.Request) {
	jc := s.jf(w, r)
	if jc == nil {
		return
	}
	typ := "Movie"
	if r.URL.Query().Get("type") == "series" {
		typ = "Series"
	}
	res, err := jc.c.Genres(r.Context(), typ)
	if err != nil {
		s.jfErr(w, err)
		return
	}
	names := make([]string, 0, len(res.Items))
	for _, g := range res.Items {
		names = append(names, g.Name)
	}
	writeJSON(w, http.StatusOK, names)
}

// handleTitle is one film, show, season or episode with what goes around it.
func (s *Server) handleTitle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jc := s.jf(w, r)
	if jc == nil {
		return
	}
	id := chi.URLParam(r, "id")
	it, err := jc.c.Item(ctx, id)
	if err != nil {
		s.jfErr(w, err)
		return
	}
	t := view(it, true)
	out := map[string]any{"title": t}
	var mu sync.Mutex
	var wg sync.WaitGroup
	set := func(k string, v any) { mu.Lock(); out[k] = v; mu.Unlock() }
	run := func(fn func()) { wg.Add(1); go func() { defer wg.Done(); fn() }() }
	switch it.Type {
	case "Series":
		run(func() {
			if res, err := jc.c.Seasons(ctx, it.ID); err == nil {
				set("seasons", views(res.Items))
			}
		})
		run(func() {
			if res, err := jc.c.NextUp(ctx, it.ID, 1, true); err == nil && len(res.Items) > 0 {
				set("next", view(&res.Items[0], false))
				return
			}
			// all watched (or Next Up has nothing): from the first episode again
			if res, err := jc.c.Episodes(ctx, it.ID, ""); err == nil {
				for i := range res.Items {
					if e := &res.Items[i]; e.LocationType != "Virtual" && (e.ParentIndexNumber == nil || *e.ParentIndexNumber > 0) {
						set("next", view(e, false))
						return
					}
				}
			}
		})
		run(func() {
			if res, err := jc.c.Similar(ctx, it.ID, 16); err == nil {
				set("similar", views(res.Items))
			}
		})
	case "Season":
		run(func() {
			if res, err := jc.c.Episodes(ctx, it.SeriesID, it.ID); err == nil {
				set("episodes", views(res.Items))
			}
		})
	case "Episode":
		run(func() {
			if res, err := jc.c.Episodes(ctx, it.SeriesID, it.SeasonID); err == nil {
				set("episodes", views(res.Items))
			}
		})
	case "Movie":
		run(func() {
			if res, err := jc.c.Similar(ctx, it.ID, 16); err == nil {
				set("similar", views(res.Items))
			}
		})
	case "Person":
		run(func() {
			if res, err := jc.c.Items(ctx, url.Values{"PersonIds": {it.ID}, "Recursive": {"true"}, "IncludeItemTypes": {"Movie,Series"}, "SortBy": {"ProductionYear,SortName"}, "SortOrder": {"Descending"}, "Limit": {"100"}}); err == nil {
				set("titles", views(res.Items))
			}
		})
	}
	wg.Wait()
	if wl, err := s.store.Watchlist(ctx, actorID(ctx)); err == nil {
		for _, x := range wl {
			tm, _ := strconv.Atoi(t.TMDB)
			if x.ItemID == it.ID || (tm > 0 && x.TMDBID == tm && x.MediaType == mediaTypeOf(it.Type)) {
				out["watchlist_id"] = x.ID
			}
		}
	}
	if it.Type == "Movie" || it.Type == "Series" {
		if l := s.leavingSoon(ctx, jc); l != nil {
			for _, x := range l {
				if x.ID == it.ID {
					t.Leaving = x.Leaving
					out["title"] = t
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func mediaTypeOf(jfType string) string {
	if jfType == "Series" || jfType == "Season" || jfType == "Episode" {
		return "tv"
	}
	return "movie"
}

// handleEpisodes lists one season's episodes (the show page switches seasons with it).
func (s *Server) handleEpisodes(w http.ResponseWriter, r *http.Request) {
	jc := s.jf(w, r)
	if jc == nil {
		return
	}
	res, err := jc.c.Episodes(r.Context(), chi.URLParam(r, "id"), r.URL.Query().Get("season"))
	if err != nil {
		s.jfErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views(res.Items))
}

// handlePlayed marks a title (a show or season: every episode) watched or not.
func (s *Server) handlePlayed(w http.ResponseWriter, r *http.Request) {
	jc := s.jf(w, r)
	if jc == nil {
		return
	}
	var in struct {
		Played bool `json:"played"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	id := chi.URLParam(r, "id")
	if err := jc.c.SetPlayed(r.Context(), id, in.Played); err != nil {
		s.jfErr(w, err)
		return
	}
	it, err := jc.c.Item(r.Context(), id)
	if err != nil {
		s.jfErr(w, err)
		return
	}
	if in.Played && (it.Type == "Movie" || it.Type == "Episode") {
		// marking by hand counts as having watched it today
		x := watchFromItem(it, store.Now(), "pocket")
		x.Completed, x.Position = true, x.Runtime
		_, _ = s.store.UpsertWatch(r.Context(), actorID(r.Context()), &x)
	}
	writeJSON(w, http.StatusOK, view(it, false))
}

// handleFavorite stars a title or takes the star away.
func (s *Server) handleFavorite(w http.ResponseWriter, r *http.Request) {
	jc := s.jf(w, r)
	if jc == nil {
		return
	}
	var in struct {
		Favorite bool `json:"favorite"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	id := chi.URLParam(r, "id")
	if err := jc.c.SetFavorite(r.Context(), id, in.Favorite); err != nil {
		s.jfErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"favorite": in.Favorite})
}

// handleSearch finds films, shows, episodes and people in the library.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	jc := s.jf(w, r)
	if jc == nil {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"movies": []Title{}, "series": []Title{}, "episodes": []Title{}, "people": []Title{}})
		return
	}
	ctx := r.Context()
	out := map[string]any{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for key, typ := range map[string]string{"movies": "Movie", "series": "Series", "episodes": "Episode", "people": "Person"} {
		wg.Add(1)
		go func(key, typ string) {
			defer wg.Done()
			lim := "12"
			if typ == "Episode" || typ == "Person" {
				lim = "8"
			}
			res, err := jc.c.Items(ctx, url.Values{"SearchTerm": {q}, "IncludeItemTypes": {typ}, "Recursive": {"true"}, "Limit": {lim}})
			v := []Title{}
			if err == nil {
				v = views(res.Items)
			}
			mu.Lock()
			out[key] = v
			mu.Unlock()
		}(key, typ)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, out)
}

// ---- images ----

var imgTypes = map[string]bool{"Primary": true, "Backdrop": true, "Thumb": true, "Logo": true, "Banner": true, "Art": true}

// handleImage passes one Jellyfin image through, resized; images carry their
// tag in the URL so the browser may keep them for a long time.
func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	jc, err := s.jfFor(r.Context(), userFrom(r.Context()))
	if err != nil {
		http.Error(w, "not connected", http.StatusConflict)
		return
	}
	id, typ := chi.URLParam(r, "id"), chi.URLParam(r, "type")
	if !imgTypes[typ] || strings.ContainsAny(id, "/.?") {
		http.NotFound(w, r)
		return
	}
	q := url.Values{"quality": {"90"}}
	if t := r.URL.Query().Get("tag"); t != "" {
		q.Set("tag", t)
	}
	wd := queryInt(r, "w")
	if wd <= 0 || wd > 3840 {
		wd = 600
	}
	q.Set("maxWidth", strconv.Itoa(wd))
	if typ == "Primary" || typ == "Thumb" || typ == "Backdrop" {
		q.Set("format", "Webp")
	}
	hdr := http.Header{}
	if v := r.Header.Get("If-None-Match"); v != "" {
		hdr.Set("If-None-Match", v)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := jc.c.Raw(ctx, http.MethodGet, "/Items/"+url.PathEscape(id)+"/Images/"+typ, q.Encode(), hdr)
	if err != nil {
		http.Error(w, "jellyfin unreachable", http.StatusBadGateway)
		return
	}
	defer res.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "ETag", "Last-Modified"} {
		if v := res.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	if res.StatusCode == http.StatusOK && r.URL.Query().Get("tag") != "" {
		w.Header().Set("Cache-Control", "private, max-age=2592000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=3600")
	}
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, res.Body)
}

// ---- helpers ----

// watchFromItem copies what history keeps about a film or an episode.
func watchFromItem(it *jellyfin.Item, at, source string) store.Watch {
	x := store.Watch{ItemID: it.ID, Kind: "movie", Title: it.Name, Year: it.ProductionYear, Genres: it.Genres,
		Runtime: int(it.RunTimeTicks / jellyfin.Ticks), WatchedAt: at, Source: source}
	if it.Type == "Episode" {
		x.Kind, x.SeriesID, x.Series = "episode", it.SeriesID, it.SeriesName
		if it.ParentIndexNumber != nil {
			x.Season = *it.ParentIndexNumber
		}
		if it.IndexNumber != nil {
			x.Episode = *it.IndexNumber
		}
	}
	if x.Year == 0 {
		x.Year = year(it.PremiereDate)
	}
	if it.ProviderIDs != nil {
		x.TMDB = it.ProviderIDs["Tmdb"]
	}
	if x.Genres == nil {
		x.Genres = []string{}
	}
	return x
}

func episodeCode(season, episode int) string {
	return fmt.Sprintf("S%02dE%02d", season, episode)
}
