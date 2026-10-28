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

	"github.com/abjelosevic88/pocket-homelab-watch/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// ---- requests and discovery through Jellyseerr ----
//
// Jellyseerr is the household's request system, set once by an admin. Pocket
// Watch acts as each person's OWN Jellyseerr user (found by their Jellyfin
// account), so Jellyseerr's rules apply as usual — who may request, quotas,
// approval — and a person only ever sees their own requests. Requests made in
// Jellyseerr itself show up here too; arrivals are pushed to the person.

type seerr struct {
	base, public, key string
	http              *http.Client
}

func (s *Server) seerrBox(ctx context.Context) *seerr {
	b, err := s.boxService(ctx, "seerr")
	if err != nil {
		return nil
	}
	return &seerr{base: b.base, public: b.public, key: b.key, http: s.http}
}

func (c *seerr) do(ctx context.Context, method, path, rawQuery string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	u := c.base + "/api/v1" + path
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Jellyseerr is not reachable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		_ = json.Unmarshal(b, &e)
		if e.Message == "" {
			e.Message = res.Status
		}
		return &seerrError{res.StatusCode, e.Message}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

type seerrError struct {
	Status int
	Msg    string
}

func (e *seerrError) Error() string { return "Jellyseerr: " + e.Msg }

// seerrQuery encodes a query the way Jellyseerr wants it (%20, never +).
func seerrQuery(v url.Values) string { return strings.ReplaceAll(v.Encode(), "+", "%20") }

type seerrUser struct {
	ID             int    `json:"id"`
	DisplayName    string `json:"displayName"`
	JellyfinUserID string `json:"jellyfinUserId"`
	Permissions    int    `json:"permissions"`
}

// seerrUserFor finds the person's Jellyseerr user by their Jellyfin account;
// with create, a missing one is imported from Jellyfin (Jellyseerr's own
// "Import Jellyfin users").
func (s *Server) seerrUserFor(ctx context.Context, c *seerr, jc *jfConn, create bool) (int, error) {
	key := "seerr-user:" + jc.c.UserID
	if v, at, ok := s.store.CacheGet(ctx, key); ok && fresh(at, 24*time.Hour) {
		if n, _ := strconv.Atoi(v); n > 0 {
			return n, nil
		}
	}
	var page struct {
		Results []seerrUser `json:"results"`
	}
	if err := c.do(ctx, http.MethodGet, "/user", "take=1000", nil, &page); err != nil {
		return 0, err
	}
	norm := func(id string) string { return strings.ToLower(strings.ReplaceAll(id, "-", "")) }
	for _, u := range page.Results {
		if norm(u.JellyfinUserID) == norm(jc.c.UserID) {
			_ = s.store.CachePut(ctx, key, strconv.Itoa(u.ID))
			return u.ID, nil
		}
	}
	if !create {
		return 0, errNoSeerrUser
	}
	var made []seerrUser
	if err := c.do(ctx, http.MethodPost, "/user/import-from-jellyfin", "", map[string]any{"jellyfinUserIds": []string{jc.c.UserID}}, &made); err != nil {
		return 0, fmt.Errorf("you have no Jellyseerr account yet and making one failed: %w", err)
	}
	for _, u := range made {
		if norm(u.JellyfinUserID) == norm(jc.c.UserID) || len(made) == 1 {
			_ = s.store.CachePut(ctx, key, strconv.Itoa(u.ID))
			return u.ID, nil
		}
	}
	return 0, errNoSeerrUser
}

var errNoSeerrUser = errors.New("your Jellyfin account has no Jellyseerr user yet")

// Found is a film or show from Jellyseerr (TMDB), with whether the library has it.
type Found struct {
	MediaType  string  `json:"media_type"` // movie | tv
	TMDBID     int     `json:"tmdb_id"`
	Title      string  `json:"title"`
	Year       int     `json:"year,omitempty"`
	Date       string  `json:"date,omitempty"`
	Overview   string  `json:"overview,omitempty"`
	Poster     string  `json:"poster,omitempty"`
	Backdrop   string  `json:"backdrop,omitempty"`
	Score      float64 `json:"score,omitempty"`
	Status     string  `json:"status"` // "" | pending | processing | partial | available
	JellyfinID string  `json:"jellyfin_id,omitempty"`
}

type seerrMedia struct {
	ID              int    `json:"id"`
	MediaType       string `json:"mediaType"`
	TMDBID          int    `json:"tmdbId"`
	TVDBID          int    `json:"tvdbId"`
	Status          int    `json:"status"`
	JellyfinMediaID string `json:"jellyfinMediaId"`
	Seasons         []struct {
		SeasonNumber int `json:"seasonNumber"`
		Status       int `json:"status"`
	} `json:"seasons"`
}

type seerrResult struct {
	ID           int         `json:"id"`
	MediaType    string      `json:"mediaType"`
	Title        string      `json:"title"`
	Name         string      `json:"name"`
	ReleaseDate  string      `json:"releaseDate"`
	FirstAirDate string      `json:"firstAirDate"`
	Overview     string      `json:"overview"`
	PosterPath   string      `json:"posterPath"`
	BackdropPath string      `json:"backdropPath"`
	VoteAverage  float64     `json:"voteAverage"`
	MediaInfo    *seerrMedia `json:"mediaInfo"`
}

func tmdbImg(path, size string) string {
	if path == "" {
		return ""
	}
	// Pocket Requests (standing in for Jellyseerr) sends shows' TheTVDB artwork as full URLs
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return "https://image.tmdb.org/t/p/" + size + path
}

func mediaStatus(m *seerrMedia) string {
	if m == nil {
		return ""
	}
	switch m.Status {
	case 2:
		return "pending"
	case 3:
		return "processing"
	case 4:
		return "partial"
	case 5:
		return "available"
	}
	return ""
}

func (r seerrResult) found() Found {
	f := Found{MediaType: r.MediaType, TMDBID: r.ID, Title: firstNonEmpty(r.Title, r.Name), Date: firstNonEmpty(r.ReleaseDate, r.FirstAirDate), Overview: r.Overview,
		Poster: tmdbImg(r.PosterPath, "w342"), Backdrop: tmdbImg(r.BackdropPath, "w1280"), Score: round1(r.VoteAverage), Status: mediaStatus(r.MediaInfo)}
	f.Year = year(f.Date)
	if r.MediaInfo != nil {
		f.JellyfinID = r.MediaInfo.JellyfinMediaID
	}
	return f
}

// seerrFor is the box's Jellyseerr or a 409 saying it isn't set up.
func (s *Server) seerrFor(w http.ResponseWriter, r *http.Request) *seerr {
	c := s.seerrBox(r.Context())
	if c == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "requests are not set up: an admin adds Jellyseerr under Settings → Services", "code": "no_seerr"})
	}
	return c
}

var discoverPaths = map[string]string{
	"trending": "/discover/trending", "movies": "/discover/movies", "tv": "/discover/tv",
	"upcoming-movies": "/discover/movies/upcoming", "upcoming-tv": "/discover/tv/upcoming",
}

// handleDiscover lists a Jellyseerr discovery shelf, or searches TMDB.
func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	c := s.seerrFor(w, r)
	if c == nil {
		return
	}
	page := queryInt(r, "page")
	if page <= 0 {
		page = 1
	}
	var path string
	q := url.Values{"page": {strconv.Itoa(page)}}
	if term := strings.TrimSpace(r.URL.Query().Get("q")); term != "" {
		path = "/search"
		q.Set("query", term)
	} else if p, ok := discoverPaths[firstNonEmpty(r.URL.Query().Get("section"), "trending")]; ok {
		path = p
	} else {
		writeErr(w, http.StatusBadRequest, "unknown section")
		return
	}
	var res struct {
		Page       int           `json:"page"`
		TotalPages int           `json:"totalPages"`
		Results    []seerrResult `json:"results"`
	}
	if err := c.do(r.Context(), http.MethodGet, path, seerrQuery(q), nil, &res); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	out := []Found{}
	for _, x := range res.Results {
		if x.MediaType == "movie" || x.MediaType == "tv" || (x.MediaType == "" && path != "/search") {
			if x.MediaType == "" {
				x.MediaType = map[bool]string{true: "tv", false: "movie"}[strings.Contains(path, "/tv")]
			}
			out = append(out, x.found())
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "page": res.Page, "pages": res.TotalPages})
}

// handleDiscoverTitle is one film or show from TMDB with its seasons and
// what the library already has.
func (s *Server) handleDiscoverTitle(w http.ResponseWriter, r *http.Request) {
	c := s.seerrFor(w, r)
	if c == nil {
		return
	}
	mt, id := chi.URLParam(r, "type"), chi.URLParam(r, "tmdb")
	if (mt != "movie" && mt != "tv") || !isDigits(id) {
		writeErr(w, http.StatusBadRequest, "type is movie or tv, the id a TMDB number")
		return
	}
	var d struct {
		seerrResult
		Runtime         int   `json:"runtime"`
		EpisodeRunTime  []int `json:"episodeRunTime"`
		NumberOfSeasons int   `json:"numberOfSeasons"`
		Tagline         string
		Genres          []struct{ Name string } `json:"genres"`
		Seasons         []struct {
			SeasonNumber int    `json:"seasonNumber"`
			EpisodeCount int    `json:"episodeCount"`
			Name         string `json:"name"`
			AirDate      string `json:"airDate"`
		} `json:"seasons"`
		Credits struct {
			Cast []struct {
				Name        string `json:"name"`
				Character   string `json:"character"`
				ProfilePath string `json:"profilePath"`
			} `json:"cast"`
		} `json:"credits"`
		RelatedVideos []struct {
			Site string `json:"site"`
			Key  string `json:"key"`
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"relatedVideos"`
	}
	if err := c.do(r.Context(), http.MethodGet, "/"+mt+"/"+id, "", nil, &d); err != nil {
		var se *seerrError
		if errors.As(err, &se) && se.Status == http.StatusNotFound {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	d.MediaType = mt
	f := d.found()
	out := map[string]any{"title": f, "tagline": d.Tagline, "runtime": d.Runtime}
	if len(d.EpisodeRunTime) > 0 {
		out["runtime"] = d.EpisodeRunTime[0]
	}
	var genres []string
	for _, g := range d.Genres {
		genres = append(genres, g.Name)
	}
	out["genres"] = nz(genres)
	type seasonOut struct {
		Number   int    `json:"number"`
		Name     string `json:"name"`
		Episodes int    `json:"episodes"`
		AirDate  string `json:"air_date,omitempty"`
		Status   string `json:"status"`
	}
	var seasons []seasonOut
	for _, x := range d.Seasons {
		if x.SeasonNumber == 0 {
			continue
		}
		so := seasonOut{Number: x.SeasonNumber, Name: x.Name, Episodes: x.EpisodeCount, AirDate: x.AirDate}
		if d.MediaInfo != nil {
			for _, ms := range d.MediaInfo.Seasons {
				if ms.SeasonNumber == x.SeasonNumber {
					so.Status = mediaStatus(&seerrMedia{Status: ms.Status})
				}
			}
		}
		seasons = append(seasons, so)
	}
	out["seasons"] = nz(seasons)
	var cast []Cast
	for i, p := range d.Credits.Cast {
		if i >= 15 {
			break
		}
		cast = append(cast, Cast{Name: p.Name, Role: p.Character, Kind: "Actor", Photo: tmdbImg(p.ProfilePath, "w185")})
	}
	out["cast"] = nz(cast)
	for _, v := range d.RelatedVideos {
		if v.Site == "YouTube" && v.Type == "Trailer" {
			out["trailer"] = "https://www.youtube.com/watch?v=" + v.Key
			break
		}
	}
	// mine?
	if reqs, err := s.store.Requests(r.Context(), actorID(r.Context())); err == nil {
		n, _ := strconv.Atoi(id)
		for _, rq := range reqs {
			if rq.TMDBID == n && rq.MediaType == mt {
				out["request"] = rq
			}
		}
	}
	out["seerr_url"] = c.public + "/" + mt + "/" + id
	writeJSON(w, http.StatusOK, out)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ---- requests ----

type seerrRequest struct {
	ID        int        `json:"id"`
	Status    int        `json:"status"` // 1 pending approval, 2 approved, 3 declined, 4 failed, 5 completed
	CreatedAt string     `json:"createdAt"`
	Media     seerrMedia `json:"media"`
	Seasons   []struct {
		SeasonNumber int `json:"seasonNumber"`
	} `json:"seasons"`
}

// requestStatus folds the request's and the media's state into one word.
func requestStatus(rq seerrRequest) string {
	switch rq.Status {
	case 3:
		return "declined"
	case 4:
		return "failed"
	case 1:
		return "pending"
	}
	switch rq.Media.Status {
	case 5:
		return "available"
	case 4:
		return "partial"
	case 3:
		return "processing"
	}
	return "approved"
}

type tmdbBrief struct {
	Title  string `json:"title"`
	Year   int    `json:"year"`
	Poster string `json:"poster"`
}

// brief is a title's name, year and poster from Jellyseerr, cached for a week.
func (s *Server) brief(ctx context.Context, c *seerr, mt string, id int) tmdbBrief {
	key := fmt.Sprintf("tmdb:%s:%d", mt, id)
	var b tmdbBrief
	if v, at, ok := s.store.CacheGet(ctx, key); ok && fresh(at, 7*24*time.Hour) && json.Unmarshal([]byte(v), &b) == nil {
		return b
	}
	var d seerrResult
	if err := c.do(ctx, http.MethodGet, "/"+mt+"/"+strconv.Itoa(id), "", nil, &d); err != nil {
		return b
	}
	b = tmdbBrief{Title: firstNonEmpty(d.Title, d.Name), Year: year(firstNonEmpty(d.ReleaseDate, d.FirstAirDate)), Poster: tmdbImg(d.PosterPath, "w342")}
	raw, _ := json.Marshal(b)
	_ = s.store.CachePut(ctx, key, string(raw))
	return b
}

// syncRequests reads the person's requests from Jellyseerr into the table,
// pushing arrivals; it returns the requests, newest first.
func (s *Server) syncRequests(ctx context.Context, u *store.User, c *seerr, jc *jfConn) ([]store.Request, error) {
	sid, err := s.seerrUserFor(ctx, c, jc, false)
	if errors.Is(err, errNoSeerrUser) {
		return s.store.Requests(ctx, u.ID)
	}
	if err != nil {
		return nil, err
	}
	var page struct {
		Results []seerrRequest `json:"results"`
	}
	if err := c.do(ctx, http.MethodGet, "/request", seerrQuery(url.Values{"take": {"100"}, "skip": {"0"}, "filter": {"all"}, "sort": {"added"}, "requestedBy": {strconv.Itoa(sid)}}), nil, &page); err != nil {
		return nil, err
	}
	st, _ := s.store.GetSettings(ctx, u.ID)
	seen := map[int]bool{}
	for _, x := range page.Results {
		seen[x.ID] = true
		mt := firstNonEmpty(x.Media.MediaType, "movie")
		b := s.brief(ctx, c, mt, x.Media.TMDBID)
		var seasons []string
		for _, sn := range x.Seasons {
			seasons = append(seasons, strconv.Itoa(sn.SeasonNumber))
		}
		rq := store.Request{SeerrID: x.ID, MediaType: mt, TMDBID: x.Media.TMDBID, Title: b.Title, Year: b.Year, Poster: b.Poster, Seasons: strings.Join(seasons, ","),
			Status: requestStatus(x), JellyfinID: x.Media.JellyfinMediaID, CreatedAt: x.CreatedAt}
		prev, notified, err := s.store.SaveRequest(ctx, u.ID, &rq)
		if err != nil {
			continue
		}
		if rq.Status == "available" && prev != "" && prev != "available" && !notified {
			s.arrived(ctx, u, st, &rq)
		} else if rq.Status == "available" && prev == "" {
			// already there when first seen: nothing to announce
			_ = s.store.MarkRequestNotified(ctx, u.ID, rq.ID)
		}
		if rq.JellyfinID != "" {
			s.store.LinkWatchlist(ctx, u.ID, mt, rq.TMDBID, rq.JellyfinID)
		}
	}
	// requests deleted in Jellyseerr stop being followed
	mine, err := s.store.Requests(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	out := mine[:0]
	for _, rq := range mine {
		if !seen[rq.SeerrID] && len(page.Results) < 100 {
			_ = s.store.ForgetRequest(ctx, u.ID, rq.SeerrID)
			continue
		}
		out = append(out, rq)
	}
	return out, nil
}

// arrived tells the person a request is ready to watch.
func (s *Server) arrived(ctx context.Context, u *store.User, st store.Settings, rq *store.Request) {
	_ = s.store.MarkRequestNotified(ctx, u.ID, rq.ID)
	s.publish(ctx, u, "watch.request_available", strconv.Itoa(rq.TMDBID), map[string]any{"media_type": rq.MediaType, "tmdb_id": rq.TMDBID, "title": rq.Title, "year": rq.Year, "jellyfin_id": rq.JellyfinID})
	if !st.NotifyAvailable || st.NotifyURL == "" {
		return
	}
	link := s.cfg.BaseURL
	if rq.JellyfinID != "" {
		link += "/title/" + rq.JellyfinID
	}
	name := rq.Title
	if rq.Year > 0 {
		name += fmt.Sprintf(" (%d)", rq.Year)
	}
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: "Ready to watch", Body: name + " is in the library.", Priority: "success", Tags: []string{"tv"}, Link: link}); err != nil {
		s.log.Warn("arrival push", "user", u.ID, "err", err)
	}
}

// followRequests checks everyone's requests in Jellyseerr every 15 minutes.
func (s *Server) followRequests(ctx context.Context) {
	c := s.seerrBox(ctx)
	if c == nil {
		return
	}
	ids, err := s.store.ConnectedUsers(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		u, err := s.store.GetUser(ctx, id)
		if err != nil {
			continue
		}
		jc, err := s.jfFor(ctx, u)
		if err != nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		if _, err := s.syncRequests(cctx, u, c, jc); err != nil {
			s.log.Debug("requests", "user", id, "err", err)
		}
		cancel()
	}
}

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c := s.seerrBox(ctx)
	if c == nil {
		mine, _ := s.store.Requests(ctx, actorID(ctx))
		writeJSON(w, http.StatusOK, map[string]any{"items": mine, "configured": false})
		return
	}
	jc := s.jf(w, r)
	if jc == nil {
		return
	}
	list, err := s.syncRequests(ctx, userFrom(ctx), c, jc)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "configured": true, "seerr_url": c.public})
}

// handleRequest asks Jellyseerr for a film or (seasons of) a show, as the person.
func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	c := s.seerrFor(w, r)
	if c == nil {
		return
	}
	jc := s.jf(w, r)
	if jc == nil {
		return
	}
	var in struct {
		MediaType string `json:"media_type"`
		TMDBID    int    `json:"tmdb_id"`
		Seasons   []int  `json:"seasons"` // empty = all
	}
	if err := decode(r, &in); err != nil || (in.MediaType != "movie" && in.MediaType != "tv") || in.TMDBID <= 0 {
		writeErr(w, http.StatusBadRequest, "media_type (movie | tv) and tmdb_id are required")
		return
	}
	sid, err := s.seerrUserFor(ctx, c, jc, true)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	body := map[string]any{"mediaType": in.MediaType, "mediaId": in.TMDBID, "userId": sid, "is4k": false}
	if in.MediaType == "tv" {
		if len(in.Seasons) == 0 {
			body["seasons"] = "all"
		} else {
			body["seasons"] = in.Seasons
		}
	}
	var made seerrRequest
	if err := c.do(ctx, http.MethodPost, "/request", "", body, &made); err != nil {
		var se *seerrError
		if errors.As(err, &se) && (se.Status == http.StatusForbidden || se.Status == http.StatusConflict || se.Status == http.StatusAccepted) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	b := s.brief(ctx, c, in.MediaType, in.TMDBID)
	rq := store.Request{SeerrID: made.ID, MediaType: in.MediaType, TMDBID: in.TMDBID, Title: b.Title, Year: b.Year, Poster: b.Poster, Status: requestStatus(made), CreatedAt: store.Now()}
	if made.ID > 0 {
		_, _, _ = s.store.SaveRequest(ctx, u.ID, &rq)
	}
	s.publish(ctx, u, "watch.requested", strconv.Itoa(in.TMDBID), map[string]any{"media_type": in.MediaType, "tmdb_id": in.TMDBID, "title": b.Title, "year": b.Year, "status": rq.Status})
	writeJSON(w, http.StatusCreated, rq)
}

// handleCancelRequest withdraws one of the person's own requests that is still waiting.
func (s *Server) handleCancelRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	c := s.seerrFor(w, r)
	if c == nil {
		return
	}
	id := int(idParam(r, "id"))
	mine, _ := s.store.Requests(ctx, uid)
	var rq *store.Request
	for i := range mine {
		if mine[i].SeerrID == id {
			rq = &mine[i]
		}
	}
	if rq == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if rq.Status != "pending" {
		writeErr(w, http.StatusConflict, "only a request still waiting for approval can be withdrawn")
		return
	}
	if err := c.do(ctx, http.MethodDelete, "/request/"+strconv.Itoa(id), "", nil, nil); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = s.store.ForgetRequest(ctx, uid, id)
	w.WriteHeader(http.StatusNoContent)
}
