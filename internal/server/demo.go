package server

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/url"
	"sort"
	"time"

	"github.com/abjelosevic88/pocket-homelab-watch/internal/jellyfin"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// ---- demo data (DEMO_DATA=true) ----
//
// The library comes from whatever Jellyfin the owner connects (the README
// screenshots use a throwaway Jellyfin with public-domain and Creative
// Commons films, see scripts/demo-library). What the demo adds is a
// believable past: three months of evenings — a show watched in order, films
// at the weekend — written to Jellyfin too, so "continue watching" and "next
// up" have something to show, plus a watchlist. It happens once, after the
// owner's history was first read.

func (s *Server) seedDemo(ctx context.Context, owner int64) error {
	if _, _, done := s.store.CacheGet(ctx, "demo:watchlist"); done {
		return nil
	}
	for _, x := range []store.WatchlistItem{
		{MediaType: "movie", TMDBID: 693134, Title: "Dune: Part Two", Year: 2024},
		{MediaType: "tv", TMDBID: 95396, Title: "Severance", Year: 2022},
	} {
		x := x
		if err := s.store.AddToWatchlist(ctx, owner, &x); err != nil {
			return err
		}
	}
	return s.store.CachePut(ctx, "demo:watchlist", "1")
}

// demoHistory invents the owner's evenings once the library is readable.
func (s *Server) demoHistory(ctx context.Context, owner int64, jc *jfConn) {
	if _, _, done := s.store.CacheGet(ctx, "demo:history"); done {
		return
	}
	movies, err := jc.c.Items(ctx, url.Values{"IncludeItemTypes": {"Movie"}, "Recursive": {"true"}, "Fields": {"Genres,ProviderIds,ProductionYear"}, "SortBy": {"SortName"}})
	if err != nil || len(movies.Items) == 0 {
		return
	}
	shows, _ := jc.c.Items(ctx, url.Values{"IncludeItemTypes": {"Series"}, "Recursive": {"true"}, "SortBy": {"SortName"}})
	rng := rand.New(rand.NewPCG(7, 11))
	var queues [][]jellyfin.Item
	for _, sh := range shows.Items {
		eps, err := jc.c.Episodes(ctx, sh.ID, "")
		if err == nil && len(eps.Items) > 0 {
			queues = append(queues, eps.Items)
		}
	}
	today := time.Now()
	played := map[string]string{} // item → when
	mi := rng.Perm(len(movies.Items))
	used := 0
	for d := 90; d >= 1; d-- {
		day := today.AddDate(0, 0, -d)
		wd := day.Weekday()
		if rng.Float64() < 0.35 {
			continue // a night off
		}
		at := time.Date(day.Year(), day.Month(), day.Day(), 20+rng.IntN(2), rng.IntN(60), 0, 0, time.Local)
		weekend := wd == time.Friday || wd == time.Saturday
		if weekend && used < len(mi)-3 {
			it := movies.Items[mi[used]]
			used++
			x := watchFromItem(&it, at.UTC().Format(time.RFC3339Nano), "pocket")
			x.Completed, x.Position, x.Seconds, x.Device = true, x.Runtime, x.Runtime, "Living room TV"
			_, _ = s.store.UpsertWatch(ctx, owner, &x)
			played[it.ID] = x.WatchedAt
			continue
		}
		if len(queues) == 0 {
			continue
		}
		qi := rng.IntN(len(queues))
		n := 1 + rng.IntN(3)
		for i := 0; i < n && len(queues[qi]) > 1; i++ {
			it := queues[qi][0]
			queues[qi] = queues[qi][1:]
			x := watchFromItem(&it, at.Add(time.Duration(i)*50*time.Minute).UTC().Format(time.RFC3339Nano), "pocket")
			x.Genres = s.seriesGenres(ctx, jc, it.SeriesID)
			x.Completed, x.Position, x.Seconds, x.Device = true, x.Runtime, x.Runtime, "MacBook"
			_, _ = s.store.UpsertWatch(ctx, owner, &x)
			played[it.ID] = x.WatchedAt
		}
	}
	// Jellyfin learns the same evenings (marking played would stamp them all today)
	for id, at := range played {
		_ = jc.c.SaveUserData(ctx, id, map[string]any{"Played": true, "PlayCount": 1, "LastPlayedDate": at, "PlaybackPositionTicks": 0})
	}
	// one film and one episode half-watched last night
	resume := func(it jellyfin.Item) {
		pos := it.RunTimeTicks * int64(30+rng.IntN(40)) / 100
		_ = jc.c.SaveUserData(ctx, it.ID, map[string]any{"PlaybackPositionTicks": pos, "LastPlayedDate": today.Add(-20 * time.Hour).UTC().Format(time.RFC3339)})
		x := watchFromItem(&it, today.Add(-20*time.Hour).UTC().Format(time.RFC3339Nano), "pocket")
		x.Position = int(pos / jellyfin.Ticks)
		x.Seconds = x.Position
		_, _ = s.store.UpsertWatch(ctx, owner, &x)
	}
	if used < len(mi) {
		resume(movies.Items[mi[used]])
	}
	sort.Slice(queues, func(i, j int) bool { return len(queues[i]) > len(queues[j]) })
	if len(queues) > 0 && len(queues[0]) > 0 {
		resume(queues[0][0])
	}
	_ = s.store.CachePut(ctx, "demo:history", "1")
	s.log.Info("demo history written", "titles", fmt.Sprint(len(played)))
}
