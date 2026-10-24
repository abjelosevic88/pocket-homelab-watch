package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core), Watch as the
// target: another Pocket app the person allowed in Core may act here for them
// with a pcx_ token, only within these scopes. Every route not listed in
// linkRoutes stays closed to it (403).

var linkOffers = []core.Offer{
	{Scope: "watching:read", Name: "See what you watch", Description: "Read what you watched (per day, history and stats), what is playing now and the episodes coming up, for your journal, calendar and dashboards."},
	{Scope: "requests:write", Name: "Ask for films and shows", Description: "Look titles up, put them on your watchlist and request them through Jellyseerr as you (a wishlist, a trip's reading list, an assistant)."},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(dashboard|history|stats|upcoming)$`), "watching:read"},
	{"GET", regexp.MustCompile(`^/api/v1/day/\d{4}-\d{2}-\d{2}$`), "watching:read"},
	{"GET", regexp.MustCompile(`^/api/v1/(search|watchlist|requests)$`), "requests:write"},
	{"GET", regexp.MustCompile(`^/api/v1/discover(/(movie|tv)/\d+)?$`), "requests:write"},
	{"POST", regexp.MustCompile(`^/api/v1/(requests|watchlist)$`), "requests:write"},
	{"GET", regexp.MustCompile(`^/api/v1/img/[0-9a-fA-F]+/(Primary|Backdrop|Thumb)$`), "watching:read"},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token, checks the route against its scopes and
// maps it to the person it acts for. The second value is the acting app's slug.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, string, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, "", err
	}
	if !in.Active || !in.Delegated() || in.User == nil || in.User.Email == "" || in.App == nil || in.App.Slug == "" {
		return nil, "", http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, "", errLinkScope
	}
	u, err := s.coreIdentityUser(ctx, in)
	return u, in.App.Slug, err
}

const linkAppKey ctxKey = 2

// linkApp is the acting app's slug when the request came in on a link token, "" otherwise.
func linkApp(ctx context.Context) string {
	a, _ := ctx.Value(linkAppKey).(string)
	return a
}
