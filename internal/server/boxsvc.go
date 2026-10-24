package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// ---- the box's services: Jellyseerr, Sonarr, Radarr, Maintainerr ----
//
// On a box with Pocket Core they come from Core — addresses, keys and public
// addresses all live there (the login Core keeps for the box owner, the account
// the box's services belong to; Core hands apps only a person's own login, never
// the household key). Without Core an admin types an address and key in under
// Settings → Services (Direct). Automatic = Core when the box has one, else
// Direct. Pocket Watch still acts as each person inside the service: requests
// go in as their own Jellyseerr user.

type boxSvc struct {
	base, public, key, via string // via: core | direct
}

type boxDef struct {
	typ      string // Core catalog type
	name     string
	needsKey bool
}

var boxDefs = map[string]boxDef{
	"seerr":       {"jellyseerr", "Jellyseerr", true},
	"sonarr":      {"sonarr", "Sonarr", true},
	"radarr":      {"radarr", "Radarr", true},
	"maintainerr": {"maintainerr", "Maintainerr", false},
}

var errBoxOff = errors.New("switched off under Settings → Services")

// direct is the address, public address, key and mode saved for a service.
func boxDirect(st store.Settings, name string) (base, public, key, mode string) {
	switch name {
	case "seerr":
		return st.SeerrURL, st.SeerrPublicURL, st.SeerrKey, st.SeerrMode
	case "sonarr":
		return st.SonarrURL, "", st.SonarrKey, st.SonarrMode
	case "radarr":
		return st.RadarrURL, "", st.RadarrKey, st.RadarrMode
	case "maintainerr":
		return st.MaintainerrURL, "", "", st.MaintainerrMode
	}
	return "", "", "", "off"
}

type boxCached struct {
	svc *boxSvc
	err error
	at  time.Time
}

var (
	boxCacheMu sync.Mutex
	boxCache   = map[string]boxCached{}
)

func forgetBoxServices() {
	boxCacheMu.Lock()
	boxCache = map[string]boxCached{}
	boxCacheMu.Unlock()
}

// boxService reaches one of the box's services (answers cached for a minute).
func (s *Server) boxService(ctx context.Context, name string) (*boxSvc, error) {
	boxCacheMu.Lock()
	if c, ok := boxCache[name]; ok && time.Since(c.at) < time.Minute {
		boxCacheMu.Unlock()
		return c.svc, c.err
	}
	boxCacheMu.Unlock()
	svc, err := s.resolveBoxService(ctx, name)
	boxCacheMu.Lock()
	boxCache[name] = boxCached{svc, err, time.Now()}
	boxCacheMu.Unlock()
	return svc, err
}

func (s *Server) resolveBoxService(ctx context.Context, name string) (*boxSvc, error) {
	def, ok := boxDefs[name]
	if !ok {
		return nil, errors.New("unknown service")
	}
	st, _ := s.store.GetSettings(ctx, 0)
	base, public, key, mode := boxDirect(st, name)
	direct := func() (*boxSvc, error) {
		if base == "" || (def.needsKey && key == "") {
			return nil, errors.New("not set up: add it in Pocket Core or enter its address" + map[bool]string{true: " and key", false: ""}[def.needsKey] + " under Settings → Services")
		}
		return &boxSvc{base: strings.TrimRight(base, "/"), public: strings.TrimRight(firstNonEmpty(public, base), "/"), key: key, via: "direct"}, nil
	}
	viaCore := func() (*boxSvc, error) {
		if !s.core.Configured() {
			return nil, errors.New("there is no Pocket Core on this box")
		}
		owner, err := s.store.GetUser(ctx, s.store.OwnerID(ctx))
		if err != nil {
			return nil, errors.New("no box owner yet")
		}
		cred, err := s.core.ServiceFor(ctx, owner.Email, def.typ)
		if err != nil {
			return nil, errors.New(coreReason(err, def.name))
		}
		if cred.URL == "" || (def.needsKey && cred.Key == "") {
			return nil, errors.New("Pocket Core has " + def.name + " without a login for the box owner (Pocket Core → Settings → My logins)")
		}
		return &boxSvc{base: strings.TrimRight(cred.URL, "/"), public: strings.TrimRight(firstNonEmpty(cred.PublicURL, cred.URL), "/"), key: cred.Key, via: "core"}, nil
	}
	switch mode {
	case "off":
		return nil, errBoxOff
	case "direct":
		return direct()
	case "core":
		return viaCore()
	}
	if s.core.Configured() {
		return viaCore()
	}
	return direct()
}

// handleServicesStatus says how each of the box's services is reached right now (admins).
func (s *Server) handleServicesStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	if r.URL.Query().Get("fresh") == "1" {
		forgetBoxServices()
	}
	st, _ := s.store.GetSettings(ctx, 0)
	out := map[string]any{}
	for name := range boxDefs {
		_, _, _, mode := boxDirect(st, name)
		v := map[string]any{"mode": firstNonEmpty(mode, "auto")}
		if svc, err := s.boxService(ctx, name); err != nil {
			v["error"] = err.Error()
		} else {
			v["via"], v["url"], v["public"] = svc.via, svc.base, svc.public
		}
		out[name] = v
	}
	sv := map[string]any{"mode": firstNonEmpty(st.SubtitlesMode, "auto")}
	if c, err := s.subsFor(ctx, userFrom(ctx)); err != nil {
		sv["error"] = err.Error()
	} else {
		sv["via"], sv["url"], sv["public"] = c.via, c.base, firstNonEmpty(c.public, st.SubtitlesURL)
	}
	out["subtitles"] = sv
	writeJSON(w, http.StatusOK, out)
}

// boxUp reports whether a service is reachable for Watch right now (cached).
func (s *Server) boxUp(ctx context.Context, name string) bool {
	_, err := s.boxService(ctx, name)
	return err == nil
}
