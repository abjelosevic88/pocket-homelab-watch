package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// ---- upcoming: Sonarr and Radarr calendars, for the shows you watch ----
//
// Sonarr and Radarr are the box's (an admin sets them); only their calendars
// are read. "Mine" narrows them to the shows the person watched in the last
// year or starred, and the films they requested or put on their watchlist.

// Upcoming is one episode airing or one film being released.
type Upcoming struct {
	Kind      string `json:"kind"` // episode | movie
	At        string `json:"at"`   // UTC
	Date      string `json:"date"` // local day
	Title     string `json:"title"`
	Series    string `json:"series,omitempty"`
	Season    int    `json:"season,omitempty"`
	Episode   int    `json:"episode,omitempty"`
	Release   string `json:"release,omitempty"` // cinema | digital | physical (films)
	HasFile   bool   `json:"has_file"`
	Poster    string `json:"poster,omitempty"`
	TMDB      int    `json:"tmdb,omitempty"`
	TVDB      int    `json:"tvdb,omitempty"`
	LibraryID string `json:"library_id,omitempty"` // the show or film in Jellyfin
	Mine      bool   `json:"mine"`
}

type arrImage struct {
	CoverType string `json:"coverType"`
	RemoteURL string `json:"remoteUrl"`
}

func poster(imgs []arrImage) string {
	for _, i := range imgs {
		if i.CoverType == "poster" && i.RemoteURL != "" {
			return strings.Replace(i.RemoteURL, "/original/", "/w342/", 1)
		}
	}
	return ""
}

func (s *Server) arrGet(ctx context.Context, base, key, path string, q url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/v3"+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", key)
	res, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// handleUpcoming lists the next days' episodes and films.
func (s *Server) handleUpcoming(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	st, _ := s.store.GetSettings(ctx, u.ID)
	son, sonErr := s.boxService(ctx, "sonarr")
	rad, radErr := s.boxService(ctx, "radarr")
	if son == nil && rad == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []Upcoming{}, "configured": false})
		return
	}
	days := queryInt(r, "days")
	if days <= 0 || days > 90 {
		days = 21
	}
	scope := firstNonEmpty(r.URL.Query().Get("scope"), st.UpcomingScope, "mine")
	from := time.Now().AddDate(0, 0, -1)
	to := time.Now().AddDate(0, 0, days)
	q := url.Values{"start": {from.UTC().Format("2006-01-02")}, "end": {to.UTC().Format("2006-01-02")}}
	jc, _ := s.jfFor(ctx, u)

	// the person's shows (by TVDB / TMDB id) and the library's ids for links
	tvdbLib, tmdbLib := map[int]string{}, map[int]string{}
	mineShows := map[string]bool{}
	if jc != nil {
		if res, err := jc.c.Items(ctx, url.Values{"IncludeItemTypes": {"Series,Movie"}, "Recursive": {"true"}, "Fields": {"ProviderIds"}, "EnableImageTypes": {"Primary"}, "Limit": {"5000"}}); err == nil {
			for _, it := range res.Items {
				if n, _ := strconv.Atoi(it.ProviderIDs["Tvdb"]); n > 0 && it.Type == "Series" {
					tvdbLib[n] = it.ID
				}
				if n, _ := strconv.Atoi(it.ProviderIDs["Tmdb"]); n > 0 {
					tmdbLib[n] = it.ID
				}
				if it.Type == "Series" && it.UserData != nil && it.UserData.IsFavorite {
					mineShows[it.ID] = true
				}
			}
		}
	}
	if ws, err := s.store.WatchedSeries(ctx, u.ID, store.AddDays(store.Today(), -365)); err == nil {
		for _, x := range ws {
			mineShows[x.SeriesID] = true
		}
	}
	mineMovies := map[int]bool{}
	if reqs, err := s.store.Requests(ctx, u.ID); err == nil {
		for _, rq := range reqs {
			mineMovies[rq.TMDBID] = true
		}
	}
	if wl, err := s.store.Watchlist(ctx, u.ID); err == nil {
		for _, x := range wl {
			if x.TMDBID > 0 {
				mineMovies[x.TMDBID] = true
			}
		}
	}
	var out []Upcoming
	var problems []string
	if sonErr != nil && !errors.Is(sonErr, errBoxOff) && !strings.HasPrefix(sonErr.Error(), "not set up") {
		problems = append(problems, "Sonarr: "+sonErr.Error())
	}
	if radErr != nil && !errors.Is(radErr, errBoxOff) && !strings.HasPrefix(radErr.Error(), "not set up") {
		problems = append(problems, "Radarr: "+radErr.Error())
	}
	if son != nil {
		var eps []struct {
			Title         string `json:"title"`
			SeasonNumber  int    `json:"seasonNumber"`
			EpisodeNumber int    `json:"episodeNumber"`
			AirDateUTC    string `json:"airDateUtc"`
			HasFile       bool   `json:"hasFile"`
			Series        struct {
				Title  string     `json:"title"`
				TVDBID int        `json:"tvdbId"`
				TMDBID int        `json:"tmdbId"`
				Images []arrImage `json:"images"`
			} `json:"series"`
		}
		qs := url.Values{"start": q["start"], "end": q["end"], "includeSeries": {"true"}, "unmonitored": {"false"}}
		if err := s.arrGet(ctx, son.base, son.key, "/calendar", qs, &eps); err != nil {
			problems = append(problems, "Sonarr: "+err.Error())
		}
		for _, e := range eps {
			lib := tvdbLib[e.Series.TVDBID]
			if lib == "" {
				lib = tmdbLib[e.Series.TMDBID]
			}
			x := Upcoming{Kind: "episode", At: e.AirDateUTC, Date: store.LocalDay(e.AirDateUTC), Title: e.Title, Series: e.Series.Title, Season: e.SeasonNumber, Episode: e.EpisodeNumber,
				HasFile: e.HasFile, Poster: poster(e.Series.Images), TVDB: e.Series.TVDBID, TMDB: e.Series.TMDBID, LibraryID: lib, Mine: lib != "" && mineShows[lib]}
			if scope == "all" || x.Mine {
				out = append(out, x)
			}
		}
	}
	if rad != nil {
		var films []struct {
			Title           string     `json:"title"`
			Year            int        `json:"year"`
			TMDBID          int        `json:"tmdbId"`
			InCinemas       string     `json:"inCinemas"`
			DigitalRelease  string     `json:"digitalRelease"`
			PhysicalRelease string     `json:"physicalRelease"`
			HasFile         bool       `json:"hasFile"`
			Images          []arrImage `json:"images"`
		}
		if err := s.arrGet(ctx, rad.base, rad.key, "/calendar", url.Values{"start": q["start"], "end": q["end"], "unmonitored": {"false"}}, &films); err != nil {
			problems = append(problems, "Radarr: "+err.Error())
		}
		for _, f := range films {
			for _, rel := range []struct{ kind, at string }{{"cinema", f.InCinemas}, {"digital", f.DigitalRelease}, {"physical", f.PhysicalRelease}} {
				if rel.at == "" {
					continue
				}
				t, err := time.Parse(time.RFC3339, rel.at)
				if err != nil || t.Before(from) || t.After(to) {
					continue
				}
				x := Upcoming{Kind: "movie", At: rel.at, Date: store.LocalDay(rel.at), Title: f.Title, Release: rel.kind, HasFile: f.HasFile, Poster: poster(f.Images), TMDB: f.TMDBID,
					LibraryID: tmdbLib[f.TMDBID], Mine: mineMovies[f.TMDBID]}
				if scope == "all" || x.Mine {
					out = append(out, x)
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At < out[j].At
		}
		return out[i].Series+out[i].Title < out[j].Series+out[j].Title
	})
	writeJSON(w, http.StatusOK, map[string]any{"items": nz(out), "configured": true, "scope": scope, "problems": nz(problems)})
}

// ---- leaving soon (Maintainerr) ----

type leaving struct {
	ID      string `json:"id"`
	Leaving string `json:"leaving"`
}

// leavingSoon lists library items Maintainerr will remove, with the day (cached 10 minutes).
func (s *Server) leavingSoon(ctx context.Context, jc *jfConn) []leaving {
	mt, err := s.boxService(ctx, "maintainerr")
	if err != nil {
		return nil
	}
	const key = "maintainerr:leaving"
	var out []leaving
	if v, at, ok := s.store.CacheGet(ctx, key); ok && fresh(at, 10*time.Minute) && json.Unmarshal([]byte(v), &out) == nil {
		return out
	}
	var cols []struct {
		IsActive        bool `json:"isActive"`
		DeleteAfterDays int  `json:"deleteAfterDays"`
		Media           []struct {
			MediaServerID string `json:"mediaServerId"`
			AddDate       string `json:"addDate"`
		} `json:"media"`
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, mt.base+"/api/collections", nil)
	res, err := s.http.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&cols) != nil {
		return nil
	}
	out = []leaving{}
	for _, c := range cols {
		if !c.IsActive || c.DeleteAfterDays <= 0 {
			continue
		}
		for _, m := range c.Media {
			t, err := time.Parse("2006-01-02 15:04:05", m.AddDate)
			if err != nil {
				t, err = time.Parse(time.RFC3339, m.AddDate)
			}
			if err != nil || m.MediaServerID == "" {
				continue
			}
			out = append(out, leaving{ID: strings.ReplaceAll(m.MediaServerID, "-", ""), Leaving: t.AddDate(0, 0, c.DeleteAfterDays).Format("2006-01-02")})
		}
	}
	raw, _ := json.Marshal(out)
	_ = s.store.CachePut(ctx, key, string(raw))
	return out
}

// handleLeaving lists what is leaving the library, as titles.
func (s *Server) handleLeaving(w http.ResponseWriter, r *http.Request) {
	jc := s.jf(w, r)
	if jc == nil {
		return
	}
	l := s.leavingSoon(r.Context(), jc)
	if len(l) == 0 {
		writeJSON(w, http.StatusOK, []Title{})
		return
	}
	ids := make([]string, 0, len(l))
	when := map[string]string{}
	for _, x := range l {
		ids = append(ids, x.ID)
		when[x.ID] = x.Leaving
	}
	res, err := jc.c.Items(r.Context(), url.Values{"Ids": {strings.Join(ids, ",")}})
	if err != nil {
		s.jfErr(w, err)
		return
	}
	out := views(res.Items)
	for i := range out {
		out[i].Leaving = when[out[i].ID]
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Leaving < out[j].Leaving })
	writeJSON(w, http.StatusOK, out)
}
