package server

import (
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

func cond(f, op, v string) store.Condition { return store.Condition{Field: f, Op: op, Value: v} }

func TestCleanupMatching(t *testing.T) {
	old := time.Now().AddDate(0, 0, -200)
	fresh := time.Now().AddDate(0, 0, -5)
	mk := func(kind string, added time.Time, watched, people int) *unit {
		return &unit{ID: "x", Kind: kind, Added: added, watchedBy: watched, people: people, requesters: map[int64]bool{}, watchedSet: map[int64]bool{}}
	}
	rule := &store.CleanupRule{Target: "movie", Match: "all", Conditions: []store.Condition{cond("watched_by", "is", "everyone"), cond("added_days", "gt", "90")},
		Protect: []string{"watchlist", "in_progress", "favorite"}}

	if !matches(rule, mk("movie", old, 2, 2)) {
		t.Fatal("old film watched by both should match")
	}
	if matches(rule, mk("movie", old, 1, 2)) {
		t.Fatal("watched by one of two should not match 'everyone'")
	}
	if matches(rule, mk("movie", fresh, 2, 2)) {
		t.Fatal("added 5 days ago should not pass 'added over 90 days ago'")
	}
	if matches(rule, mk("series", old, 2, 2)) {
		t.Fatal("a film rule never picks a show")
	}
	if matches(rule, mk("movie", old, 0, 0)) {
		t.Fatal("with nobody watching here, 'everyone' must not be true")
	}

	anyRule := *rule
	anyRule.Match = "any"
	if !matches(&anyRule, mk("movie", fresh, 2, 2)) {
		t.Fatal("any: one passing condition is enough")
	}

	u := mk("movie", old, 2, 2)
	u.watchlisted = true
	if protectedBy(rule, u) == "" {
		t.Fatal("a watchlisted title is protected")
	}
	u.watchlisted, u.kept = false, true
	if protectedBy(rule, u) != "kept" {
		t.Fatal("a kept title is protected even without protections")
	}

	nobody := &store.CleanupRule{Target: "movie", Conditions: []store.Condition{cond("watched_by", "is", "nobody"), cond("added_days", "gt", "365")}}
	never := mk("movie", time.Now().AddDate(-2, 0, 0), 0, 3)
	if !matches(nobody, never) {
		t.Fatal("never played for two years should match")
	}
	never.lastPlayed = time.Now().AddDate(0, -1, 0)
	if matches(nobody, never) {
		t.Fatal("someone half-watched it a month ago: not 'nobody'")
	}

	req := mk("movie", old, 1, 2)
	req.requesters[7] = true
	req.watchedSet[7] = true
	req.requesterWatched = true
	rq := &store.CleanupRule{Target: "movie", Conditions: []store.Condition{cond("requester_watched", "is", "yes")}}
	if !matches(rq, req) {
		t.Fatal("the requester watched it")
	}
	if matches(rq, mk("movie", old, 2, 2)) {
		t.Fatal("not requested: 'requester watched' is false")
	}
}

func TestCleanRule(t *testing.T) {
	r := &store.CleanupRule{Name: " x ", Target: "season", Conditions: []store.Condition{cond("added_days", "zz", "30")}, Protect: []string{"watchlist", "bogus"}, GraceDays: 14}
	if err := cleanRule(r); err != nil {
		t.Fatal(err)
	}
	if r.Name != "x" || r.Conditions[0].Op != "gt" || len(r.Protect) != 1 || r.Action != "delete" || r.Match != "all" {
		t.Fatalf("not tidied: %+v", r)
	}
	bad := &store.CleanupRule{Name: "x", Target: "movie", Conditions: []store.Condition{cond("added_days", "gt", "soon")}, GraceDays: 14}
	if cleanRule(bad) == nil {
		t.Fatal("a number field needs a number")
	}
	if cleanRule(&store.CleanupRule{Name: "x", Target: "movie", Conditions: []store.Condition{cond("year", "lt", "2000")}, GraceDays: 0}) == nil {
		t.Fatal("grace days must be at least 1")
	}
}

func TestDescribe(t *testing.T) {
	r := &store.CleanupRule{Conditions: []store.Condition{cond("watched_by", "is", "everyone"), cond("added_days", "gt", "90")}}
	if got := describe(r); got != "Watched by everyone, added over 90 days ago" {
		t.Fatal(got)
	}
}
