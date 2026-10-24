package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// ---- the watchlist: things a person means to watch ----
//
// A title can be on it whether the library has it (a Jellyfin id) or not yet
// (a TMDB id, one click from a request). When a requested title arrives the
// entry links itself to the library; a film drops off once watched.

type watchlistOut struct {
	store.WatchlistItem
	Library *Title `json:"library,omitempty"`
	Request string `json:"request,omitempty"` // the request's status, when there is one
}

func (s *Server) handleWatchlist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	list, err := s.store.Watchlist(ctx, uid)
	if err != nil {
		s.fail(w, err, "watchlist")
		return
	}
	out := make([]watchlistOut, 0, len(list))
	var ids []string
	for _, x := range list {
		if x.ItemID != "" {
			ids = append(ids, x.ItemID)
		}
	}
	lib := map[string]Title{}
	if len(ids) > 0 {
		if jc, err := s.jfFor(ctx, userFrom(ctx)); err == nil {
			if res, err := jc.c.Items(ctx, url.Values{"Ids": {strings.Join(ids, ",")}}); err == nil {
				for _, t := range views(res.Items) {
					lib[t.ID] = t
				}
			}
		}
	}
	reqs, _ := s.store.Requests(ctx, uid)
	for _, x := range list {
		o := watchlistOut{WatchlistItem: x}
		if t, ok := lib[x.ItemID]; ok {
			o.Library = &t
		}
		for _, rq := range reqs {
			if x.TMDBID > 0 && rq.TMDBID == x.TMDBID && rq.MediaType == x.MediaType {
				o.Request = rq.Status
			}
		}
		out = append(out, o)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAddWatchlist puts a library title (item_id) or a TMDB title on the list.
func (s *Server) handleAddWatchlist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in store.WatchlistItem
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.ID = 0
	if in.ItemID != "" {
		jc := s.jf(w, r)
		if jc == nil {
			return
		}
		it, err := jc.c.Item(ctx, in.ItemID)
		if err != nil {
			s.jfErr(w, err)
			return
		}
		if it.Type == "Episode" || it.Type == "Season" {
			it, err = jc.c.Item(ctx, it.SeriesID)
			if err != nil {
				s.jfErr(w, err)
				return
			}
		}
		if it.Type != "Movie" && it.Type != "Series" {
			writeErr(w, http.StatusBadRequest, "only films and shows go on the watchlist")
			return
		}
		t := view(it, false)
		in.ItemID, in.Title, in.Year, in.MediaType, in.Poster = it.ID, it.Name, t.Year, mediaTypeOf(it.Type), t.Poster
		in.TMDBID, _ = strconv.Atoi(t.TMDB)
	} else {
		in.Title = strings.TrimSpace(in.Title)
		if (in.MediaType != "movie" && in.MediaType != "tv") || in.TMDBID <= 0 || in.Title == "" {
			writeErr(w, http.StatusBadRequest, "item_id, or media_type + tmdb_id + title")
			return
		}
		if !strings.HasPrefix(in.Poster, "https://image.tmdb.org/") {
			in.Poster = ""
		}
	}
	in.Note = trim(strings.TrimSpace(in.Note), 500)
	in.AddedAt = ""
	if err := s.store.AddToWatchlist(ctx, u.ID, &in); err != nil {
		s.fail(w, err, "watchlist")
		return
	}
	s.publish(ctx, u, "watch.watchlist_added", strconv.FormatInt(in.ID, 10), map[string]any{"media_type": in.MediaType, "title": in.Title, "year": in.Year, "tmdb_id": in.TMDBID, "in_library": in.ItemID != ""})
	writeJSON(w, http.StatusCreated, in)
}

func (s *Server) handleRemoveWatchlist(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RemoveFromWatchlist(r.Context(), actorID(r.Context()), idParam(r, "id")); err != nil {
		s.fail(w, err, "watchlist")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
