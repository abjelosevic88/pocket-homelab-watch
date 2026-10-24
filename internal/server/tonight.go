package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/jellyfin"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// ---- "What should we watch?" ----
//
// A sentence ("something funny under two hours", "a scary film for two",
// "a show to start") becomes a handful of picks from what the person hasn't
// watched yet. The AI is shown titles, years, genres, lengths and ratings of
// unwatched films and shows in the library — never the person's history —
// and answers with ids from that list and a line on each. Without AI the
// words pick genres, length and era, and the day decides the rest.

// aiFor is the person's AI: Pocket AI through Core, their own OpenAI-compatible key, or nil with why.
func (s *Server) aiFor(ctx context.Context, u *store.User, st store.Settings) (*ai.Client, string, string) {
	pocket, status := s.core.SelectAI(ctx, u.Email, st.AIProvider, st.OpenAIKey != "")
	if pocket != nil {
		if !status.Ready {
			return nil, "", "Pocket AI: " + status.Detail
		}
		c := ai.New("", "", "", 120*time.Second)
		c.Pocket = pocket.WithModel(st.AIPocketModel)
		return c, "pocket", ""
	}
	if st.OpenAIKey == "" {
		return nil, "", status.Detail
	}
	return ai.New(st.OpenAIBaseURL, st.OpenAIKey, st.OpenAIModel, 120*time.Second), "openai", ""
}

func validAIProvider(v string) bool { return core.ValidAIProvider(v) }

func validPocketModel(v string) bool { return core.ValidInferenceModel(v) }

func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	st, _ := s.store.GetSettings(ctx, u.ID)
	cl, used, why := s.aiFor(ctx, u, st)
	writeJSON(w, http.StatusOK, map[string]any{"ready": cl != nil, "provider": used, "detail": why})
}

func (s *Server) handleAIModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, status := s.core.SelectAI(ctx, userFrom(ctx).Email, "pocket", false)
	if c == nil || !status.Ready {
		writeErr(w, http.StatusServiceUnavailable, status.Detail)
		return
	}
	models, err := c.Models(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

// aiJSON asks for one JSON object and tolerates code fences and chatter around it.
func aiJSON(ctx context.Context, c *ai.Client, system, prompt string, v any) error {
	txt, err := c.Complete(ctx, system, prompt, true)
	if err != nil {
		return err
	}
	if i, j := strings.Index(txt, "{"), strings.LastIndex(txt, "}"); i >= 0 && j > i {
		txt = txt[i : j+1]
	}
	if err := json.Unmarshal([]byte(txt), v); err != nil {
		return fmt.Errorf("the AI's answer was not valid JSON: %w", err)
	}
	return nil
}

// Pick is one suggestion with the reason for it.
type Pick struct {
	Title Title  `json:"title"`
	Why   string `json:"why"`
}

// moods maps everyday words to genres, for picks without AI.
var moods = map[string][]string{
	"funny": {"Comedy"}, "laugh": {"Comedy"}, "comedy": {"Comedy"}, "light": {"Comedy", "Family", "Animation", "Romance"},
	"scary": {"Horror", "Thriller"}, "horror": {"Horror"}, "creepy": {"Horror", "Mystery"}, "spooky": {"Horror"},
	"tense": {"Thriller", "Crime"}, "thriller": {"Thriller"}, "crime": {"Crime"}, "mystery": {"Mystery"},
	"action": {"Action", "Adventure"}, "exciting": {"Action", "Adventure", "Thriller"}, "epic": {"Adventure", "Fantasy", "War", "History"},
	"romantic": {"Romance"}, "romance": {"Romance"}, "date": {"Romance", "Comedy"}, "love": {"Romance"},
	"sad": {"Drama"}, "cry": {"Drama", "Romance"}, "drama": {"Drama"}, "serious": {"Drama", "History"},
	"kids": {"Family", "Animation"}, "family": {"Family", "Animation"}, "animated": {"Animation"}, "cartoon": {"Animation"},
	"space": {"Science Fiction"}, "sci-fi": {"Science Fiction"}, "scifi": {"Science Fiction"}, "future": {"Science Fiction"},
	"magic": {"Fantasy"}, "fantasy": {"Fantasy"}, "war": {"War"}, "history": {"History"}, "true": {"Documentary", "History"},
	"documentary": {"Documentary"}, "learn": {"Documentary"}, "music": {"Music"}, "musical": {"Music"}, "western": {"Western"},
}

type candidate struct {
	it  jellyfin.Item
	sum string
}

// handleTonight suggests what to watch.
func (s *Server) handleTonight(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	jc := s.jf(w, r)
	if jc == nil {
		return
	}
	var in struct {
		Prompt     string `json:"prompt"`
		Kind       string `json:"kind"` // any | movie | series
		MaxMinutes int    `json:"max_minutes"`
		NoAI       bool   `json:"no_ai"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.Prompt = trim(strings.TrimSpace(in.Prompt), 500)
	types := "Movie,Series"
	switch in.Kind {
	case "movie":
		types = "Movie"
	case "series":
		types = "Series"
	}
	lower := strings.ToLower(in.Prompt)
	if in.MaxMinutes == 0 {
		switch {
		case strings.Contains(lower, "short"), strings.Contains(lower, "quick"):
			in.MaxMinutes = 100
		case strings.Contains(lower, "under two hours"), strings.Contains(lower, "under 2 hours"):
			in.MaxMinutes = 120
		}
	}
	res, err := jc.c.Items(ctx, url.Values{"IncludeItemTypes": {types}, "Recursive": {"true"}, "Filters": {"IsUnplayed"}, "SortBy": {"Random"}, "Limit": {"400"},
		"Fields": {"Genres,ProductionYear,OfficialRating,CommunityRating,ProviderIds,ChildCount,RecursiveItemCount,DateCreated"}})
	if err != nil {
		s.jfErr(w, err)
		return
	}
	var cands []candidate
	for _, it := range res.Items {
		mins := int(it.RunTimeTicks / jellyfin.Ticks / 60)
		if in.MaxMinutes > 0 && it.Type == "Movie" && mins > in.MaxMinutes {
			continue
		}
		kind := "film"
		if it.Type == "Series" {
			kind = "show"
		}
		line := fmt.Sprintf("%s|%s|%s|%d|%s|%dmin|%s|%.1f", it.ID, kind, it.Name, it.ProductionYear, strings.Join(it.Genres, ","), mins, it.OfficialRating, it.CommunityRating)
		cands = append(cands, candidate{it: it, sum: line})
	}
	if len(cands) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"picks": []Pick{}, "provider": "", "intro": "Nothing unwatched fits that — try fewer limits."})
		return
	}
	st, _ := s.store.GetSettings(ctx, u.ID)
	if !in.NoAI && in.Prompt != "" {
		if cl, provider, _ := s.aiFor(ctx, u, st); cl != nil {
			picks, intro, err := s.aiPicks(ctx, cl, in.Prompt, cands)
			if err == nil && len(picks) > 0 {
				writeJSON(w, http.StatusOK, map[string]any{"picks": picks, "provider": provider, "intro": intro})
				return
			}
			if err != nil {
				s.log.Warn("tonight: AI", "err", err)
			}
		}
	}
	picks, intro := guessPicks(lower, cands)
	writeJSON(w, http.StatusOK, map[string]any{"picks": picks, "provider": "", "intro": intro})
}

func (s *Server) aiPicks(ctx context.Context, cl *ai.Client, prompt string, cands []candidate) ([]Pick, string, error) {
	if len(cands) > 300 {
		cands = cands[:300]
	}
	var b strings.Builder
	for _, c := range cands {
		b.WriteString(c.sum)
		b.WriteByte('\n')
	}
	system := `You help a household pick something to watch from their own library. You get their wish and a list of titles they have not watched, one per line: id|film or show|title|year|genres|runtime|age rating|score out of 10.
Answer with JSON only: {"intro": "one short friendly sentence", "picks": [{"id": "<id from the list>", "why": "one sentence on why it fits the wish"}]}.
Pick 3 to 6 titles, best first, ONLY ids from the list. Match the mood, length and audience asked for; prefer well-rated titles when it's a toss-up. Write in the language of the wish.`
	var out struct {
		Intro string `json:"intro"`
		Picks []struct {
			ID  string `json:"id"`
			Why string `json:"why"`
		} `json:"picks"`
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := aiJSON(cctx, cl, system, "Wish: "+prompt+"\n\nUnwatched titles:\n"+b.String(), &out); err != nil {
		return nil, "", err
	}
	by := map[string]*jellyfin.Item{}
	for i := range cands {
		by[cands[i].it.ID] = &cands[i].it
	}
	var picks []Pick
	seen := map[string]bool{}
	for _, p := range out.Picks {
		if it := by[strings.TrimSpace(p.ID)]; it != nil && !seen[it.ID] {
			seen[it.ID] = true
			picks = append(picks, Pick{Title: view(it, false), Why: trim(p.Why, 300)})
		}
		if len(picks) == 6 {
			break
		}
	}
	return picks, trim(out.Intro, 300), nil
}

// guessPicks reads genres, length and era out of the words, then lets the day choose.
func guessPicks(lower string, cands []candidate) ([]Pick, string) {
	want := map[string]bool{}
	for w, gs := range moods {
		if strings.Contains(lower, w) {
			for _, g := range gs {
				want[g] = true
			}
		}
	}
	old := strings.Contains(lower, "classic") || strings.Contains(lower, "old")
	recent := strings.Contains(lower, "new") || strings.Contains(lower, "recent")
	seed := int(time.Now().Unix() / 3600)
	type scored struct {
		c     candidate
		score float64
	}
	var list []scored
	for _, c := range cands {
		sc := c.it.CommunityRating
		match := len(want) == 0
		for _, g := range c.it.Genres {
			if want[g] {
				sc += 4
				match = true
			}
		}
		if !match {
			continue
		}
		if old && c.it.ProductionYear > 0 && c.it.ProductionYear < 1990 {
			sc += 3
		}
		if recent && c.it.ProductionYear >= time.Now().Year()-3 {
			sc += 3
		}
		sc += float64(hashMod(c.it.ID, seed)%100) / 40 // the hour's luck
		list = append(list, scored{c, sc})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].score > list[j].score })
	var picks []Pick
	for _, x := range list {
		picks = append(picks, Pick{Title: view(&x.c.it, false), Why: strings.Join(x.c.it.Genres, ", ")})
		if len(picks) == 6 {
			break
		}
	}
	intro := "A few you haven't seen yet."
	if len(want) > 0 {
		intro = "Picked by genre from what you asked for."
	}
	if picks == nil {
		picks = []Pick{}
		intro = "Nothing unwatched matches those words — try others."
	}
	return picks, intro
}
