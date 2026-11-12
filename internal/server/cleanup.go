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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-watch/internal/jellyfin"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// ---- clean-up rules ----
//
// An admin writes rules ("films everyone has watched, added more than 90
// days ago"). Every few hours Pocket Watch reads the library and what each
// person on the box has watched, puts what a rule finds on Leaving soon for
// the rule's grace days — where anyone can keep it — and then removes it
// through Radarr or Sonarr (files and all, or just the files with the title
// left unmonitored), clearing it in Jellyseerr so it can be asked for again.
// Titles Radarr or Sonarr don't have are deleted through Jellyfin.

// arrHTTP allows for Radarr/Sonarr's slow answers (the whole library, deletes with files).
var arrHTTP = &http.Client{Timeout: 3 * time.Minute}

const (
	cleanupJob     = "cleanup"
	cleanupLastKey = "cleanup:last"
	maxAttempts    = 5
	inProgressDays = 183 // "in the middle of it" = played in the last six months
)

// cleanupFields are the tests a rule can use, with the ops each allows.
var cleanupFields = map[string][]string{
	"added_days":        {"gt", "lt"},
	"last_watched_days": {"gt", "lt"},
	"watched_by":        {"is"}, // everyone | anyone | nobody | not_everyone
	"requested":         {"is"}, // yes | no
	"requester_watched": {"is"}, // yes | no
	"rating":            {"gt", "lt"},
	"size_gb":           {"gt", "lt"},
	"year":              {"gt", "lt"},
	"genre":             {"is", "not"},
	"status":            {"is"}, // ended | continuing
}

var cleanupProtect = map[string]bool{"watchlist": true, "in_progress": true, "favorite": true}

// unit is one film, show or season as the rules see it.
type unit struct {
	ID, Kind, Title, SeriesID string
	Season                    int
	TMDB, TVDB                int
	Added                     time.Time
	Year                      int
	Rating                    float64
	Genres                    []string
	Status                    string // Continuing | Ended (shows and their seasons)
	Size                      int64

	watchedBy, people int
	startedRecently   bool
	favorite          bool
	lastPlayed        time.Time
	requesters        map[int64]bool
	requesterWatched  bool // every requester watched it all
	watchlisted       bool
	kept              bool
	watchedSet        map[int64]bool
}

// library is everything one clean-up run looks at.
type library struct {
	units  map[string]*unit
	people int
	owner  *jfConn
	radarr *boxSvc
	sonarr *boxSvc
	movies map[int]arrMovie  // Radarr, by TMDB id
	series map[int]arrSeries // Sonarr, by TVDB id
	notes  []string
}

type arrMovie struct {
	ID         int   `json:"id"`
	TMDB       int   `json:"tmdbId"`
	SizeOnDisk int64 `json:"sizeOnDisk"`
	MovieFile  *struct {
		ID int `json:"id"`
	} `json:"movieFile"`
}

type arrSeries struct {
	ID         int `json:"id"`
	TVDB       int `json:"tvdbId"`
	TMDB       int `json:"tmdbId"`
	Statistics struct {
		SizeOnDisk int64 `json:"sizeOnDisk"`
	} `json:"statistics"`
	Seasons []struct {
		SeasonNumber int `json:"seasonNumber"`
		Statistics   *struct {
			SizeOnDisk int64 `json:"sizeOnDisk"`
		} `json:"statistics"`
	} `json:"seasons"`
}

func parseJFTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, _ = time.Parse("2006-01-02T15:04:05", strings.TrimSuffix(s, "Z"))
	}
	return t
}

// ownerJF is the box owner's Jellyfin: the library the rules look at.
func (s *Server) ownerJF(ctx context.Context) (*jfConn, *store.User, error) {
	owner, err := s.store.GetUser(ctx, s.store.OwnerID(ctx))
	if err != nil {
		return nil, nil, errors.New("there is no box owner yet")
	}
	jc, err := s.jfFor(ctx, owner)
	if err != nil {
		return nil, owner, fmt.Errorf("the owner's Jellyfin: %w", err)
	}
	return jc, owner, nil
}

// cleanupItems reads films, shows and seasons with one person's state.
func cleanupItems(ctx context.Context, jc *jfConn, full bool) ([]jellyfin.Item, error) {
	fields := "DateCreated"
	if full {
		fields = "ProviderIds,DateCreated,Genres,ProductionYear,CommunityRating,ChildCount"
	}
	var out []jellyfin.Item
	for start := 0; ; start += 2000 {
		res, err := jc.c.Items(ctx, url.Values{"IncludeItemTypes": {"Movie,Series,Season"}, "Recursive": {"true"}, "Fields": {fields},
			"EnableImages": {"false"}, "EnableImageTypes": {"Primary"}, "Limit": {"2000"}, "StartIndex": {strconv.Itoa(start)}})
		if err != nil {
			return nil, err
		}
		for _, it := range res.Items {
			// seasons without a folder of their own are "Virtual" but still hold episodes
			if it.LocationType != "Virtual" || it.Type == "Season" {
				out = append(out, it)
			}
		}
		if len(res.Items) < 2000 {
			return out, nil
		}
	}
}

// playedEpisodes lists the episodes a person watched or started.
func playedEpisodes(ctx context.Context, jc *jfConn) ([]jellyfin.Item, error) {
	var out []jellyfin.Item
	for _, f := range []string{"IsPlayed", "IsResumable"} {
		for start := 0; ; start += 2000 {
			res, err := jc.c.Items(ctx, url.Values{"IncludeItemTypes": {"Episode"}, "Recursive": {"true"}, "Filters": {f}, "Fields": {"DateCreated"},
				"EnableImages": {"false"}, "EnableImageTypes": {"Primary"}, "Limit": {"2000"}, "StartIndex": {strconv.Itoa(start)}})
			if err != nil {
				return nil, err
			}
			for _, it := range res.Items {
				if it.UserData != nil {
					out = append(out, it)
				}
			}
			if len(res.Items) < 2000 {
				break
			}
		}
	}
	return out, nil
}

// readLibrary gathers the library, everyone's watching, watchlists, requests and Radarr/Sonarr.
func (s *Server) readLibrary(ctx context.Context) (*library, error) {
	owner, _, err := s.ownerJF(ctx)
	if err != nil {
		return nil, err
	}
	items, err := cleanupItems(ctx, owner, true)
	if err != nil {
		return nil, fmt.Errorf("reading the library: %w", err)
	}
	lib := &library{units: map[string]*unit{}, owner: owner, movies: map[int]arrMovie{}, series: map[int]arrSeries{}}
	shows := map[string]*unit{}
	var seasons []jellyfin.Item
	for i := range items {
		it := &items[i]
		u := &unit{ID: it.ID, Title: it.Name, Added: parseJFTime(it.DateCreated), Year: it.ProductionYear, Rating: it.CommunityRating, Genres: it.Genres,
			Status: it.Status, requesters: map[int64]bool{}, watchedSet: map[int64]bool{}}
		u.TMDB, _ = strconv.Atoi(it.ProviderIDs["Tmdb"])
		u.TVDB, _ = strconv.Atoi(it.ProviderIDs["Tvdb"])
		switch it.Type {
		case "Movie":
			u.Kind = "movie"
		case "Series":
			u.Kind = "series"
			shows[it.ID] = u
		case "Season":
			seasons = append(seasons, *it)
			continue
		default:
			continue
		}
		lib.units[it.ID] = u
	}
	for _, it := range seasons {
		show := shows[it.SeriesID]
		if show == nil || it.IndexNumber == nil {
			continue
		}
		n := *it.IndexNumber
		name := show.Title + " · " + it.Name
		if n == 0 {
			name = show.Title + " · Specials"
		}
		if it.ChildCount == 0 && it.LocationType == "Virtual" {
			continue // only missing episodes
		}
		lib.units[it.ID] = &unit{ID: it.ID, Kind: "season", Title: name, SeriesID: show.ID, Season: n, TMDB: show.TMDB, TVDB: show.TVDB,
			Added: parseJFTime(it.DateCreated), Year: firstInt(it.ProductionYear, show.Year), Rating: show.Rating, Genres: show.Genres, Status: show.Status,
			requesters: map[int64]bool{}, watchedSet: map[int64]bool{}}
	}

	// everyone's watching, each Jellyfin account once
	ids, err := s.store.ConnectedUsers(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	recent := time.Now().AddDate(0, 0, -inProgressDays)
	for _, id := range ids {
		p, err := s.store.GetUser(ctx, id)
		if err != nil {
			continue
		}
		jc, err := s.jfFor(ctx, p)
		if err != nil {
			if isNoJellyfin(err) {
				continue // doesn't watch here
			}
			return nil, fmt.Errorf("reading what %s watched: %w", firstNonEmpty(p.Name, p.Email), err)
		}
		if seen[jc.key] {
			continue
		}
		seen[jc.key] = true
		mine, err := cleanupItems(ctx, jc, false)
		if err != nil {
			return nil, fmt.Errorf("reading what %s watched: %w", firstNonEmpty(p.Name, p.Email), err)
		}
		lib.people++
		for i := range mine {
			it := &mine[i]
			u := lib.units[it.ID]
			if u == nil || it.UserData == nil {
				continue
			}
			d := it.UserData
			last := parseJFTime(d.LastPlayedDate)
			if last.After(u.lastPlayed) {
				u.lastPlayed = last
			}
			if d.Played {
				u.watchedBy++
				u.watchedSet[id] = true
			} else if (d.PlaybackPositionTicks > 0 || !last.IsZero()) && last.After(recent) {
				u.startedRecently = true
			}
			if d.IsFavorite {
				u.favorite = true
			}
		}
		// Jellyfin keeps no last-played day for shows and seasons: their episodes tell
		eps, err := playedEpisodes(ctx, jc)
		if err != nil {
			return nil, fmt.Errorf("reading what %s watched: %w", firstNonEmpty(p.Name, p.Email), err)
		}
		for _, e := range eps {
			last := parseJFTime(e.UserData.LastPlayedDate)
			for _, pid := range []string{e.SeriesID, e.SeasonID} {
				u := lib.units[pid]
				if u == nil {
					continue
				}
				if last.After(u.lastPlayed) {
					u.lastPlayed = last
				}
				if !u.watchedSet[id] && last.After(recent) {
					u.startedRecently = true
				}
			}
		}
	}
	for _, u := range lib.units {
		u.people = lib.people
	}

	// watchlists and requests (seasons go with their show)
	byTMDB := map[string]*unit{}
	for _, u := range lib.units {
		if u.TMDB > 0 && u.Kind != "season" {
			byTMDB[mediaKind(u.Kind)+":"+strconv.Itoa(u.TMDB)] = u
		}
	}
	find := func(w store.Wanted) *unit {
		if w.ItemID != "" {
			if u := lib.units[w.ItemID]; u != nil {
				return u
			}
		}
		if w.TMDB > 0 {
			return byTMDB[w.MediaType+":"+strconv.Itoa(w.TMDB)]
		}
		return nil
	}
	if wl, err := s.store.AllWatchlists(ctx); err == nil {
		for _, w := range wl {
			if u := find(w); u != nil {
				u.watchlisted = true
			}
		}
	}
	if rq, err := s.store.AllRequests(ctx); err == nil {
		for _, w := range rq {
			if u := find(w); u != nil {
				u.requesters[w.UserID] = true
			}
		}
	}
	for _, u := range lib.units {
		if u.Kind == "season" {
			if show := lib.units[u.SeriesID]; show != nil {
				u.watchlisted = u.watchlisted || show.watchlisted
				u.favorite = u.favorite || show.favorite
				for id := range show.requesters {
					u.requesters[id] = true
				}
			}
		}
		u.requesterWatched = len(u.requesters) > 0
		for id := range u.requesters {
			if !u.watchedSet[id] {
				u.requesterWatched = false
			}
		}
	}
	if keeps, err := s.store.Keeps(ctx); err == nil {
		kept := map[string]bool{}
		for _, k := range keeps {
			kept[k.ItemID] = true
		}
		for _, u := range lib.units {
			u.kept = kept[u.ID] || (u.SeriesID != "" && kept[u.SeriesID])
		}
	}

	// Radarr and Sonarr: what they hold, and how big it is
	if rad, err := s.boxService(ctx, "radarr"); err == nil {
		var ms []arrMovie
		if err := s.arrDo(ctx, rad, http.MethodGet, "/movie", nil, nil, &ms); err != nil {
			lib.notes = append(lib.notes, "Radarr: "+err.Error())
		} else {
			lib.radarr = rad
			for _, m := range ms {
				lib.movies[m.TMDB] = m
			}
		}
	}
	if son, err := s.boxService(ctx, "sonarr"); err == nil {
		var ss []arrSeries
		if err := s.arrDo(ctx, son, http.MethodGet, "/series", nil, nil, &ss); err != nil {
			lib.notes = append(lib.notes, "Sonarr: "+err.Error())
		} else {
			lib.sonarr = son
			for _, x := range ss {
				lib.series[x.TVDB] = x
			}
		}
	}
	for _, u := range lib.units {
		switch u.Kind {
		case "movie":
			u.Size = lib.movies[u.TMDB].SizeOnDisk
		case "series":
			u.Size = lib.series[u.TVDB].Statistics.SizeOnDisk
		case "season":
			for _, sn := range lib.series[u.TVDB].Seasons {
				if sn.SeasonNumber == u.Season && sn.Statistics != nil {
					u.Size = sn.Statistics.SizeOnDisk
				}
			}
			if _, known := lib.series[u.TVDB]; known && u.Size == 0 {
				delete(lib.units, u.ID) // nothing on disk to remove
			}
		}
	}
	return lib, nil
}

func firstInt(v ...int) int {
	for _, x := range v {
		if x != 0 {
			return x
		}
	}
	return 0
}

func mediaKind(kind string) string {
	if kind == "movie" {
		return "movie"
	}
	return "tv"
}

// ---- matching ----

func days(t time.Time) float64 { return time.Since(t).Hours() / 24 }

func cmp(op string, have, want float64) bool {
	if op == "lt" {
		return have < want
	}
	return have > want
}

// test says whether one title passes one condition.
func test(u *unit, c store.Condition) bool {
	n, _ := strconv.ParseFloat(strings.TrimSpace(c.Value), 64)
	v := strings.ToLower(strings.TrimSpace(c.Value))
	switch c.Field {
	case "added_days":
		return !u.Added.IsZero() && cmp(c.Op, days(u.Added), n)
	case "last_watched_days":
		return !u.lastPlayed.IsZero() && cmp(c.Op, days(u.lastPlayed), n)
	case "watched_by":
		switch v {
		case "everyone":
			return u.people > 0 && u.watchedBy >= u.people
		case "anyone":
			return u.watchedBy > 0
		case "nobody":
			return u.watchedBy == 0 && u.lastPlayed.IsZero()
		case "not_everyone":
			return u.watchedBy < u.people
		}
	case "requested":
		return (len(u.requesters) > 0) == (v == "yes")
	case "requester_watched":
		if len(u.requesters) == 0 {
			return false
		}
		return u.requesterWatched == (v == "yes")
	case "rating":
		return u.Rating > 0 && cmp(c.Op, u.Rating, n)
	case "size_gb":
		return u.Size > 0 && cmp(c.Op, float64(u.Size)/1e9, n)
	case "year":
		return u.Year > 0 && cmp(c.Op, float64(u.Year), n)
	case "genre":
		has := false
		for _, g := range u.Genres {
			if strings.EqualFold(g, strings.TrimSpace(c.Value)) {
				has = true
			}
		}
		return has == (c.Op != "not")
	case "status":
		return strings.EqualFold(u.Status, v)
	}
	return false
}

// matches says whether a title is what the rule is after (protections aside).
func matches(r *store.CleanupRule, u *unit) bool {
	if u.Kind != r.Target || len(r.Conditions) == 0 {
		return false
	}
	anyOf := r.Match == "any"
	for _, c := range r.Conditions {
		ok := test(u, c)
		if anyOf && ok {
			return true
		}
		if !anyOf && !ok {
			return false
		}
	}
	return !anyOf
}

// protectedBy names what keeps a matching title in the library ("" = nothing).
func protectedBy(r *store.CleanupRule, u *unit) string {
	if u.kept {
		return "kept"
	}
	for _, p := range r.Protect {
		switch {
		case p == "watchlist" && u.watchlisted:
			return "on someone's watchlist"
		case p == "in_progress" && u.startedRecently:
			return "someone is in the middle of it"
		case p == "favorite" && u.favorite:
			return "someone's favourite"
		}
	}
	return ""
}

// describe turns a rule's conditions into words for Leaving soon.
func describe(r *store.CleanupRule) string {
	var parts []string
	for _, c := range r.Conditions {
		more := c.Op != "lt"
		num := func(over, under string) string {
			if more {
				return fmt.Sprintf(over, c.Value)
			}
			return fmt.Sprintf(under, c.Value)
		}
		switch c.Field {
		case "added_days":
			parts = append(parts, num("added over %s days ago", "added in the last %s days"))
		case "last_watched_days":
			parts = append(parts, num("nobody watched it for %s days", "watched in the last %s days"))
		case "watched_by":
			parts = append(parts, map[string]string{"everyone": "watched by everyone", "anyone": "watched by someone", "nobody": "nobody watched it", "not_everyone": "not watched by everyone"}[c.Value])
		case "requested":
			parts = append(parts, map[string]string{"yes": "someone asked for it", "no": "nobody asked for it"}[c.Value])
		case "requester_watched":
			parts = append(parts, map[string]string{"yes": "whoever asked for it watched it", "no": "whoever asked for it hasn't watched it"}[c.Value])
		case "rating":
			parts = append(parts, num("rated above %s", "rated below %s"))
		case "size_gb":
			parts = append(parts, num("bigger than %s GB", "smaller than %s GB"))
		case "year":
			parts = append(parts, num("from after %s", "from before %s"))
		case "genre":
			if c.Op == "not" {
				parts = append(parts, "not "+c.Value)
			} else {
				parts = append(parts, c.Value)
			}
		case "status":
			parts = append(parts, map[string]string{"ended": "the show has ended", "continuing": "the show is still running"}[c.Value])
		}
	}
	sep := ", "
	if r.Match == "any" {
		sep = " or "
	}
	out := strings.Join(parts, sep)
	if out != "" {
		out = strings.ToUpper(out[:1]) + out[1:]
	}
	return out
}

// ---- the run ----

type cleanupResult struct {
	At       string   `json:"at"`
	Matched  int      `json:"matched"`
	Added    int      `json:"added"`
	Removed  int      `json:"removed"`
	Failed   int      `json:"failed"`
	Freed    int64    `json:"freed"`
	People   int      `json:"people"`
	Error    string   `json:"error,omitempty"`
	Notes    []string `json:"notes,omitempty"`
	Duration float64  `json:"seconds"`
}

func (s *Server) cleanupLast(ctx context.Context) *cleanupResult {
	v, _, ok := s.store.CacheGet(ctx, cleanupLastKey)
	if !ok {
		return nil
	}
	var r cleanupResult
	if json.Unmarshal([]byte(v), &r) != nil {
		return nil
	}
	return &r
}

// cleanupDue says whether there is anything for clean-up to do at all.
func (s *Server) cleanupDue(ctx context.Context) bool {
	rules, _ := s.store.CleanupRules(ctx)
	for _, r := range rules {
		if r.Active {
			return true
		}
	}
	l, _ := s.store.Leaving(ctx)
	return len(l) > 0
}

// runCleanup reads the library, refreshes Leaving soon and removes what is due.
func (s *Server) runCleanup(ctx context.Context) cleanupResult {
	start := time.Now()
	res := cleanupResult{At: store.Now()}
	defer func() {
		res.Duration = time.Since(start).Seconds()
		raw, _ := json.Marshal(res)
		_ = s.store.CachePut(context.WithoutCancel(ctx), cleanupLastKey, string(raw))
	}()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	rules, err := s.store.CleanupRules(ctx)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	lib, err := s.readLibrary(ctx)
	if err != nil {
		res.Error = err.Error() + " — nothing was changed"
		for _, r := range rules {
			if r.Active {
				s.store.RuleRan(ctx, r.ID, r.LastMatched, res.Error)
			}
		}
		return res
	}
	res.People, res.Notes = lib.people, lib.notes
	current, _ := s.store.Leaving(ctx)
	byRule := map[int64]map[string]bool{}
	ruleByID := map[int64]*store.CleanupRule{}
	today := store.Today()
	for i := range rules {
		r := &rules[i]
		ruleByID[r.ID] = r
		if !r.Active {
			continue
		}
		hit := map[string]bool{}
		reason := describe(r)
		for _, u := range lib.units {
			if !matches(r, u) || protectedBy(r, u) != "" {
				continue
			}
			hit[u.ID] = true
			l := &store.LeavingItem{ItemID: u.ID, RuleID: r.ID, Kind: u.Kind, Title: u.Title, SeriesID: u.SeriesID, Season: u.Season, TMDB: u.TMDB, TVDB: u.TVDB,
				Size: u.Size, Reason: reason, RemoveOn: store.AddDays(today, r.GraceDays)}
			added, err := s.store.PutLeaving(ctx, l, false)
			if err != nil {
				continue
			}
			if added {
				res.Added++
			}
		}
		byRule[r.ID] = hit
		res.Matched += len(hit)
		s.store.RuleRan(ctx, r.ID, len(hit), "")
	}
	// what no longer qualifies comes off; so does what left the library some other way
	for _, l := range current {
		gone := lib.units[l.ItemID] == nil
		if l.RuleID > 0 {
			hit, active := byRule[l.RuleID]
			if !active || !hit[l.ItemID] || gone {
				_ = s.store.DropLeaving(ctx, l.ItemID)
			}
			continue
		}
		if gone || (lib.units[l.ItemID] != nil && lib.units[l.ItemID].kept) {
			_ = s.store.DropLeaving(ctx, l.ItemID)
		}
	}
	leaving, _ := s.store.Leaving(ctx)
	s.tellLeaving(ctx, leaving, lib)
	for i := range leaving {
		l := &leaving[i]
		if l.RemoveOn > today || l.Attempts >= maxAttempts {
			continue
		}
		var rule *store.CleanupRule
		if l.RuleID > 0 {
			if rule = ruleByID[l.RuleID]; rule == nil {
				continue
			}
		}
		if err := s.removeTitle(ctx, lib, l, rule); err != nil {
			res.Failed++
		} else {
			res.Removed++
			res.Freed += l.Size
		}
	}
	if res.Removed > 0 {
		if err := lib.owner.c.ScanLibrary(ctx); err != nil {
			s.log.Debug("cleanup: library scan", "err", err)
		}
		s.tellOwner(ctx, res)
	}
	s.log.Info("cleanup run", "matched", res.Matched, "added", res.Added, "removed", res.Removed, "failed", res.Failed, "freed", res.Freed)
	return res
}

// tellLeaving pushes each new title on Leaving soon to whoever asked for it.
func (s *Server) tellLeaving(ctx context.Context, leaving []store.LeavingItem, lib *library) {
	for _, l := range leaving {
		if l.Notified {
			continue
		}
		s.store.MarkLeavingNotified(ctx, l.ItemID)
		u := lib.units[l.ItemID]
		if u == nil {
			continue
		}
		for id := range u.requesters {
			p, err := s.store.GetUser(ctx, id)
			if err != nil {
				continue
			}
			st, _ := s.store.GetSettings(ctx, id)
			if !st.NotifyLeaving || st.NotifyURL == "" {
				continue
			}
			day, _ := store.ParseDate(l.RemoveOn)
			n := notify.New(st.NotifyURL, st.NotifyFormat)
			if err := n.Send(ctx, notify.Message{Title: "Leaving soon", Body: fmt.Sprintf("%s, which you asked for, leaves the library on %s. Open it to keep it.", l.Title, day.Format("Mon 2 Jan")),
				Priority: "info", Tags: []string{"hourglass"}, Link: s.cfg.BaseURL + "/title/" + l.ItemID}); err != nil {
				s.log.Warn("leaving push", "user", p.ID, "err", err)
			}
		}
	}
}

// tellOwner sends the owner what a run removed.
func (s *Server) tellOwner(ctx context.Context, res cleanupResult) {
	id := s.store.OwnerID(ctx)
	st, _ := s.store.GetSettings(ctx, id)
	if !st.NotifyLeaving || st.NotifyURL == "" {
		return
	}
	body := fmt.Sprintf("Removed %d title%s", res.Removed, map[bool]string{true: "", false: "s"}[res.Removed == 1])
	if res.Freed > 0 {
		body += fmt.Sprintf(", %.1f GB freed", float64(res.Freed)/1e9)
	}
	if res.Failed > 0 {
		body += fmt.Sprintf("; %d could not be removed", res.Failed)
	}
	_ = notify.New(st.NotifyURL, st.NotifyFormat).Send(ctx, notify.Message{Title: "Clean-up", Body: body + ".", Priority: "info", Tags: []string{"broom"}, Link: s.cfg.BaseURL + "/settings/cleanup"})
}

// ---- removing ----

func (s *Server) arrDo(ctx context.Context, svc *boxSvc, method, path string, q url.Values, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	u := svc.base + "/api/v3" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", svc.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := arrHTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("HTTP %d %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// removeTitle takes one title out of the library and logs it.
func (s *Server) removeTitle(ctx context.Context, lib *library, l *store.LeavingItem, rule *store.CleanupRule) error {
	action, exclude, clear, ruleName := "delete", false, true, ""
	if rule != nil {
		action, exclude, clear, ruleName = rule.Action, rule.ExcludeLists, rule.ClearRequest, rule.Name
	}
	via, detail, err := s.removeVia(ctx, lib, l, action, exclude)
	if err == nil && clear && l.Kind != "season" && l.TMDB > 0 {
		if e := s.clearSeerr(ctx, mediaKind(l.Kind), l.TMDB); e != nil {
			detail = strings.TrimPrefix(detail+"; Jellyseerr: "+e.Error(), "; ")
		} else {
			detail = strings.TrimPrefix(detail+"; cleared in Jellyseerr", "; ")
		}
	}
	entry := store.CleanupLog{ItemID: l.ItemID, Kind: l.Kind, Title: l.Title, Rule: ruleName, Action: action, Via: via, Size: l.Size, OK: err == nil, Detail: detail}
	if err != nil {
		entry.Detail = err.Error()
		s.store.LeavingFailed(ctx, l.ItemID, err.Error())
		s.store.AddCleanupLog(ctx, entry)
		s.log.Warn("cleanup: removing failed", "title", l.Title, "err", err)
		return err
	}
	_ = s.store.DropLeaving(ctx, l.ItemID)
	s.store.AddCleanupLog(ctx, entry)
	s.log.Info("cleanup: removed", "title", l.Title, "via", via, "action", action)
	return nil
}

func (s *Server) removeVia(ctx context.Context, lib *library, l *store.LeavingItem, action string, exclude bool) (via, detail string, err error) {
	switch l.Kind {
	case "movie":
		if m, ok := lib.movies[l.TMDB]; ok && lib.radarr != nil && l.TMDB > 0 {
			if action == "unmonitor" {
				if m.MovieFile != nil {
					if err := s.arrDo(ctx, lib.radarr, http.MethodDelete, fmt.Sprintf("/moviefile/%d", m.MovieFile.ID), nil, nil, nil); err != nil {
						return "radarr", "", err
					}
				}
				return "radarr", "files deleted, left in Radarr unmonitored", s.arrDo(ctx, lib.radarr, http.MethodPut, "/movie/editor", nil, map[string]any{"movieIds": []int{m.ID}, "monitored": false}, nil)
			}
			q := url.Values{"deleteFiles": {"true"}, "addImportExclusion": {strconv.FormatBool(exclude)}}
			return "radarr", "deleted with its files", s.arrDo(ctx, lib.radarr, http.MethodDelete, fmt.Sprintf("/movie/%d", m.ID), q, nil, nil)
		}
	case "series", "season":
		if sr, ok := lib.series[l.TVDB]; ok && lib.sonarr != nil && l.TVDB > 0 {
			if l.Kind == "series" && action != "unmonitor" {
				q := url.Values{"deleteFiles": {"true"}, "addImportListExclusion": {strconv.FormatBool(exclude)}}
				return "sonarr", "deleted with its files", s.arrDo(ctx, lib.sonarr, http.MethodDelete, fmt.Sprintf("/series/%d", sr.ID), q, nil, nil)
			}
			season := -1
			if l.Kind == "season" {
				season = l.Season
			}
			return "sonarr", "files deleted, left in Sonarr unmonitored", s.sonarrEmpty(ctx, lib.sonarr, sr.ID, season)
		}
	}
	// neither Radarr nor Sonarr has it: Jellyfin deletes the files itself
	if err := lib.owner.c.DeleteItem(ctx, l.ItemID); err != nil {
		if jellyfin.IsAuth(err) {
			return "jellyfin", "", errors.New("not in Radarr/Sonarr, and the owner's Jellyfin account may not delete (Jellyfin → Users → allow media deletion)")
		}
		return "jellyfin", "", err
	}
	return "jellyfin", "deleted through Jellyfin", nil
}

// sonarrEmpty deletes a show's (or one season's, season >= 0) files and stops Sonarr fetching them again.
func (s *Server) sonarrEmpty(ctx context.Context, son *boxSvc, seriesID, season int) error {
	q := url.Values{"seriesId": {strconv.Itoa(seriesID)}}
	var files []struct {
		ID           int `json:"id"`
		SeasonNumber int `json:"seasonNumber"`
	}
	if err := s.arrDo(ctx, son, http.MethodGet, "/episodefile", q, nil, &files); err != nil {
		return err
	}
	var ids []int
	for _, f := range files {
		if season < 0 || f.SeasonNumber == season {
			ids = append(ids, f.ID)
		}
	}
	if len(ids) > 0 {
		if err := s.arrDo(ctx, son, http.MethodDelete, "/episodefile/bulk", nil, map[string]any{"episodeFileIds": ids}, nil); err != nil {
			return err
		}
	}
	if season < 0 {
		return s.arrDo(ctx, son, http.MethodPut, "/series/editor", nil, map[string]any{"seriesIds": []int{seriesID}, "monitored": false}, nil)
	}
	q.Set("seasonNumber", strconv.Itoa(season))
	var eps []struct {
		ID int `json:"id"`
	}
	if err := s.arrDo(ctx, son, http.MethodGet, "/episode", q, nil, &eps); err != nil {
		return err
	}
	epIDs := make([]int, 0, len(eps))
	for _, e := range eps {
		epIDs = append(epIDs, e.ID)
	}
	if len(epIDs) > 0 {
		if err := s.arrDo(ctx, son, http.MethodPut, "/episode/monitor", nil, map[string]any{"episodeIds": epIDs, "monitored": false}, nil); err != nil {
			return err
		}
	}
	// the season itself, so a season search skips it too
	var sr map[string]any
	if err := s.arrDo(ctx, son, http.MethodGet, fmt.Sprintf("/series/%d", seriesID), nil, nil, &sr); err != nil {
		return err
	}
	if ss, ok := sr["seasons"].([]any); ok {
		for _, x := range ss {
			if m, ok := x.(map[string]any); ok {
				if n, _ := m["seasonNumber"].(float64); int(n) == season {
					m["monitored"] = false
				}
			}
		}
	}
	return s.arrDo(ctx, son, http.MethodPut, fmt.Sprintf("/series/%d", seriesID), nil, sr, nil)
}

// clearSeerr forgets a title in Jellyseerr so it shows as not there and can be asked for again.
func (s *Server) clearSeerr(ctx context.Context, mediaType string, tmdb int) error {
	c := s.seerrBox(ctx)
	if c == nil {
		return errors.New("not connected")
	}
	var info struct {
		MediaInfo *struct {
			ID int `json:"id"`
		} `json:"mediaInfo"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/%s/%d", mediaType, tmdb), "", nil, &info); err != nil {
		return err
	}
	if info.MediaInfo == nil || info.MediaInfo.ID == 0 {
		return nil
	}
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/media/%d", info.MediaInfo.ID), "", nil, nil)
}

// ---- leaving soon, for everyone ----

type leaving struct {
	ID      string `json:"id"`
	Leaving string `json:"leaving"`
}

// leavingSoon lists what is on its way out with the day it goes.
func (s *Server) leavingSoon(ctx context.Context, _ *jfConn) []leaving {
	ls, err := s.store.Leaving(ctx)
	if err != nil {
		return nil
	}
	out := make([]leaving, 0, len(ls))
	for _, l := range ls {
		out = append(out, leaving{ID: l.ItemID, Leaving: l.RemoveOn})
	}
	return out
}

// canKeep says whether this person may keep a title.
func (s *Server) canKeep(ctx context.Context, u *store.User) bool {
	if u == nil {
		return false
	}
	if u.Role == "admin" {
		return true
	}
	st, _ := s.store.GetSettings(ctx, 0)
	return st.CleanupKeep != "admins"
}

type leavingOut struct {
	Title    Title  `json:"title"`
	Reason   string `json:"reason"`
	Rule     string `json:"rule,omitempty"`
	Manual   bool   `json:"manual"`
	Size     int64  `json:"size"`
	RemoveOn string `json:"remove_on"`
	Error    string `json:"error,omitempty"`
}

// handleLeaving lists what is leaving the library, with why and when.
func (s *Server) handleLeaving(w http.ResponseWriter, r *http.Request) {
	jc := s.jf(w, r)
	if jc == nil {
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	ls, err := s.store.Leaving(ctx)
	if err != nil {
		s.fail(w, err, "leaving")
		return
	}
	resp := map[string]any{"items": []leavingOut{}, "can_keep": s.canKeep(ctx, u), "admin": u != nil && u.Role == "admin"}
	if len(ls) == 0 {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	ids := make([]string, 0, len(ls))
	for _, l := range ls {
		ids = append(ids, l.ItemID)
	}
	res, err := jc.c.Items(ctx, url.Values{"Ids": {strings.Join(ids, ",")}})
	if err != nil {
		s.jfErr(w, err)
		return
	}
	titles := map[string]Title{}
	for _, t := range views(res.Items) {
		titles[t.ID] = t
	}
	rules, _ := s.store.CleanupRules(ctx)
	names := map[int64]string{}
	for _, x := range rules {
		names[x.ID] = x.Name
	}
	out := make([]leavingOut, 0, len(ls))
	for _, l := range ls {
		t, ok := titles[l.ItemID]
		if !ok {
			continue
		}
		t.Leaving = l.RemoveOn
		o := leavingOut{Title: t, Reason: l.Reason, Rule: names[l.RuleID], Manual: l.RuleID == 0, Size: l.Size, RemoveOn: l.RemoveOn}
		if u != nil && u.Role == "admin" {
			o.Error = l.Error
		}
		out = append(out, o)
	}
	resp["items"] = out
	writeJSON(w, http.StatusOK, resp)
}

// handleKeep keeps a title: off Leaving soon, and no rule picks it again.
func (s *Server) handleKeep(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	if !s.canKeep(ctx, u) {
		writeErr(w, http.StatusForbidden, "only an admin can keep titles here")
		return
	}
	id := chi.URLParam(r, "id")
	k := store.Keep{ItemID: id, UserID: u.ID}
	if l, err := s.store.LeavingOne(ctx, id); err == nil {
		k.Kind, k.Title = l.Kind, l.Title
	} else if jc, err := s.jfFor(ctx, u); err == nil {
		if it, err := jc.c.Item(ctx, id); err == nil {
			k.Kind, k.Title = lowerType(it.Type), it.Name
			if it.Type == "Season" {
				k.Title = it.SeriesName + " · " + it.Name
			}
		}
	}
	if err := s.store.AddKeep(ctx, k); err != nil {
		s.fail(w, err, "keep")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleUnkeep lets the rules see a title again (admins).
func (s *Server) handleUnkeep(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.store.DeleteKeep(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, err, "keep")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleLeaveByHand puts a title on Leaving soon (days > 0) or removes it now (days = 0). Admins.
func (s *Server) handleLeaveByHand(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	var in struct {
		Days *int `json:"days"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	id := chi.URLParam(r, "id")
	jc, _, err := s.ownerJF(ctx)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	it, err := jc.c.Item(ctx, id)
	if err != nil {
		s.jfErr(w, err)
		return
	}
	l := &store.LeavingItem{ItemID: it.ID, Kind: lowerType(it.Type), Title: it.Name, AddedBy: actorID(ctx), Reason: "Picked by hand"}
	l.TMDB, _ = strconv.Atoi(it.ProviderIDs["Tmdb"])
	l.TVDB, _ = strconv.Atoi(it.ProviderIDs["Tvdb"])
	switch it.Type {
	case "Movie", "Series":
	case "Season":
		show, err := jc.c.Item(ctx, it.SeriesID)
		if err != nil || it.IndexNumber == nil {
			writeErr(w, http.StatusConflict, "can't find the show this season belongs to")
			return
		}
		l.Title, l.SeriesID, l.Season = show.Name+" · "+it.Name, show.ID, *it.IndexNumber
		l.TMDB, _ = strconv.Atoi(show.ProviderIDs["Tmdb"])
		l.TVDB, _ = strconv.Atoi(show.ProviderIDs["Tvdb"])
	default:
		writeErr(w, http.StatusBadRequest, "only films, shows and seasons can leave the library")
		return
	}
	st, _ := s.store.GetSettings(ctx, 0)
	d := st.CleanupManualDays
	if in.Days != nil {
		d = *in.Days
	}
	if d < 0 || d > 365 {
		writeErr(w, http.StatusBadRequest, "between 0 and 365 days")
		return
	}
	l.RemoveOn = store.AddDays(store.Today(), d)
	_ = s.store.DeleteKeep(ctx, l.ItemID)
	if _, err := s.store.PutLeaving(ctx, l, true); err != nil {
		s.fail(w, err, "leaving")
		return
	}
	if d > 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "remove_on": l.RemoveOn})
		return
	}
	// now: needs Radarr/Sonarr's view of it
	lib, err := s.readArr(ctx, jc)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	cur, _ := s.store.LeavingOne(ctx, l.ItemID)
	if cur == nil {
		cur = l
	}
	cur.Size = sizeOf(lib, cur)
	if err := s.removeTitle(ctx, lib, cur, nil); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = jc.c.ScanLibrary(ctx)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": true})
}

// readArr is the Radarr/Sonarr half of readLibrary (for removing one title by hand).
func (s *Server) readArr(ctx context.Context, owner *jfConn) (*library, error) {
	lib := &library{owner: owner, movies: map[int]arrMovie{}, series: map[int]arrSeries{}}
	if rad, err := s.boxService(ctx, "radarr"); err == nil {
		var ms []arrMovie
		if err := s.arrDo(ctx, rad, http.MethodGet, "/movie", nil, nil, &ms); err != nil {
			return nil, errors.New("Radarr: " + err.Error())
		}
		lib.radarr = rad
		for _, m := range ms {
			lib.movies[m.TMDB] = m
		}
	}
	if son, err := s.boxService(ctx, "sonarr"); err == nil {
		var ss []arrSeries
		if err := s.arrDo(ctx, son, http.MethodGet, "/series", nil, nil, &ss); err != nil {
			return nil, errors.New("Sonarr: " + err.Error())
		}
		lib.sonarr = son
		for _, x := range ss {
			lib.series[x.TVDB] = x
		}
	}
	return lib, nil
}

func sizeOf(lib *library, l *store.LeavingItem) int64 {
	switch l.Kind {
	case "movie":
		return lib.movies[l.TMDB].SizeOnDisk
	case "series":
		return lib.series[l.TVDB].Statistics.SizeOnDisk
	}
	for _, sn := range lib.series[l.TVDB].Seasons {
		if sn.SeasonNumber == l.Season && sn.Statistics != nil {
			return sn.Statistics.SizeOnDisk
		}
	}
	return 0
}

// handleCancelLeaving takes a title put there by hand off Leaving soon (admins).
func (s *Server) handleCancelLeaving(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.store.DropLeaving(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, err, "leaving")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- admin: rules, preview, run, log ----

func (s *Server) handleCleanup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	rules, err := s.store.CleanupRules(ctx)
	if err != nil {
		s.fail(w, err, "cleanup")
		return
	}
	keeps, _ := s.store.Keeps(ctx)
	logs, _ := s.store.CleanupLogs(ctx, 100)
	people, _ := s.store.ListUsers(ctx)
	who := map[int64]string{}
	for _, p := range people {
		who[p.ID] = firstNonEmpty(p.Name, p.Email)
	}
	type keepOut struct {
		store.Keep
		By string `json:"by"`
	}
	ko := make([]keepOut, 0, len(keeps))
	for _, k := range keeps {
		ko = append(ko, keepOut{k, who[k.UserID]})
	}
	st, _ := s.store.GetSettings(ctx, 0)
	s.jobsMu.Lock()
	running := s.jobs[cleanupJob]
	s.jobsMu.Unlock()
	type ruleOut struct {
		store.CleanupRule
		Reason string `json:"reason"`
	}
	ro := make([]ruleOut, 0, len(rules))
	for i := range rules {
		ro = append(ro, ruleOut{rules[i], describe(&rules[i])})
	}
	_, radErr := s.boxService(ctx, "radarr")
	_, sonErr := s.boxService(ctx, "sonarr")
	setupURL := "" // neither is set up yet: where to finish the box's downloads
	if notSetUp(radErr) && notSetUp(sonErr) {
		setupURL, _ = s.downloadsSetup(ctx, "radarr", "sonarr")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rules": ro, "keeps": ko, "log": logs, "last": s.cleanupLast(ctx), "running": running,
		"every_hours": s.cleanupEvery().Hours(),
		"settings":    map[string]any{"manual_days": st.CleanupManualDays, "keep": firstNonEmpty(st.CleanupKeep, "anyone")},
		"services":    map[string]bool{"radarr": radErr == nil, "sonarr": sonErr == nil, "seerr": s.boxUp(ctx, "seerr")},
		"setup_url":   setupURL,
	})
}

// cleanRule checks and tidies a rule from the editor.
func cleanRule(in *store.CleanupRule) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return errors.New("give the rule a name")
	}
	if in.Target != "movie" && in.Target != "series" && in.Target != "season" {
		return errors.New("a rule is for films, shows or seasons")
	}
	if in.Match != "any" {
		in.Match = "all"
	}
	if len(in.Conditions) == 0 {
		return errors.New("add at least one condition")
	}
	for i, c := range in.Conditions {
		ops, ok := cleanupFields[c.Field]
		if !ok {
			return fmt.Errorf("unknown condition %q", c.Field)
		}
		valid := false
		for _, o := range ops {
			valid = valid || o == c.Op
		}
		if !valid {
			in.Conditions[i].Op = ops[0]
		}
		in.Conditions[i].Value = strings.TrimSpace(c.Value)
		if in.Conditions[i].Value == "" {
			return errors.New("every condition needs a value")
		}
		if ops[0] == "gt" {
			if _, err := strconv.ParseFloat(in.Conditions[i].Value, 64); err != nil {
				return fmt.Errorf("%q needs a number", c.Field)
			}
		}
	}
	prot := []string{}
	for _, p := range in.Protect {
		if cleanupProtect[p] {
			prot = append(prot, p)
		}
	}
	in.Protect = prot
	if in.GraceDays < 1 || in.GraceDays > 365 {
		return errors.New("titles stay on Leaving soon for 1 to 365 days")
	}
	if in.Action != "unmonitor" {
		in.Action = "delete"
	}
	return nil
}

func (s *Server) handleSaveRule(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in store.CleanupRule
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	in.ID = 0
	if id := chi.URLParam(r, "id"); id != "" {
		in.ID, _ = strconv.ParseInt(id, 10, 64)
	}
	if err := cleanRule(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.ID > 0 {
		if old, err := s.store.CleanupRule(r.Context(), in.ID); err == nil && old.Target != in.Target {
			_, _ = s.store.DB.ExecContext(r.Context(), `DELETE FROM cleanup_items WHERE rule_id = ?`, in.ID)
		}
	}
	if err := s.store.SaveCleanupRule(r.Context(), &in); err != nil {
		s.fail(w, err, "rule")
		return
	}
	rule, _ := s.store.CleanupRule(r.Context(), in.ID)
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err := s.store.DeleteCleanupRule(r.Context(), id); err != nil {
		s.fail(w, err, "rule")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type previewHit struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Size      int64  `json:"size"`
	Poster    string `json:"poster,omitempty"`
	Protected string `json:"protected,omitempty"`
}

// handlePreview shows what a rule (saved or not) would pick right now.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in store.CleanupRule
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		in.Name = "preview"
	}
	if in.GraceDays == 0 {
		in.GraceDays = 1
	}
	if err := cleanRule(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	lib, err := s.readLibrary(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	hits, kept := []previewHit{}, []previewHit{}
	var size int64
	for _, u := range lib.units {
		if !matches(&in, u) {
			continue
		}
		h := previewHit{ID: u.ID, Title: u.Title, Kind: u.Kind, Size: u.Size, Poster: "/api/v1/img/" + firstNonEmpty(map[bool]string{true: u.SeriesID}[u.Kind == "season"], u.ID) + "/Primary"}
		if p := protectedBy(&in, u); p != "" {
			h.Protected = p
			kept = append(kept, h)
			continue
		}
		size += u.Size
		hits = append(hits, h)
	}
	sort.Slice(hits, func(i, j int) bool {
		return hits[i].Size > hits[j].Size || hits[i].Size == hits[j].Size && hits[i].Title < hits[j].Title
	})
	sort.Slice(kept, func(i, j int) bool { return kept[i].Title < kept[j].Title })
	writeJSON(w, http.StatusOK, map[string]any{"matches": hits, "protected": kept, "size": size, "people": lib.people, "reason": describe(&in), "notes": nz(lib.notes)})
}

func (s *Server) handleRunCleanup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	started := s.job(cleanupJob, func(ctx context.Context) { s.runCleanup(ctx) })
	writeJSON(w, http.StatusAccepted, map[string]any{"started": started})
}

func (s *Server) handleCleanupSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		ManualDays *int    `json:"manual_days"`
		Keep       *string `json:"keep"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	st, _ := s.store.GetSettings(r.Context(), 0)
	if in.ManualDays != nil {
		if *in.ManualDays < 1 || *in.ManualDays > 365 {
			writeErr(w, http.StatusBadRequest, "between 1 and 365 days")
			return
		}
		st.CleanupManualDays = *in.ManualDays
	}
	if in.Keep != nil {
		if *in.Keep != "anyone" && *in.Keep != "admins" {
			writeErr(w, http.StatusBadRequest, "anyone or admins")
			return
		}
		st.CleanupKeep = *in.Keep
	}
	if err := s.store.SaveBoxSettings(r.Context(), st); err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"manual_days": st.CleanupManualDays, "keep": firstNonEmpty(st.CleanupKeep, "anyone")})
}

func (s *Server) cleanupEvery() time.Duration {
	if s.cfg.CleanupEvery > 0 {
		return s.cfg.CleanupEvery
	}
	return 6 * time.Hour
}

// cleanupLoop runs the rules every CLEANUP_EVERY (first a few minutes after start).
func (s *Server) cleanupLoop(ctx context.Context) {
	t := time.NewTimer(3 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s.cleanupDue(ctx) {
				s.job(cleanupJob, func(ctx context.Context) { s.runCleanup(ctx) })
			}
			t.Reset(s.cleanupEvery())
		}
	}
}
