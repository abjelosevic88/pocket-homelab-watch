// Package server wires the HTTP API, auth and the embedded SPA.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/config"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/jellyfin"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// Version is injected at build time.
var Version = "dev"

// Server holds dependencies.
type Server struct {
	cfg      *config.Config
	store    *store.Store
	log      *slog.Logger
	webFS    fs.FS
	started  time.Time
	requests atomic.Int64
	errors   atomic.Int64
	http     *http.Client // Jellyseerr, Sonarr, Radarr, Maintainerr

	oidcMu sync.Mutex
	oidc   *oidcProvider // for the issuer in use; rebuilt when it changes

	core    *core.Client
	coreMu  sync.Mutex
	coreOK  bool
	coreErr string

	syncMu  sync.Mutex
	syncing map[int64]bool // people whose history is being read

	qcMu sync.Mutex
	qc   map[int64]*quickConnect // Quick Connect sign-ins waiting for approval

	coreUIDMu sync.Mutex
	coreUID   map[string]string // Core-held token → the Jellyfin user it belongs to

	playMu sync.Mutex
	plays  map[string]*playState // play session → what is being watched

	jobsMu sync.Mutex
	jobs   map[string]bool // background jobs running
}

// New creates a server.
func New(cfg *config.Config, st *store.Store, log *slog.Logger, webFS fs.FS) *Server {
	return &Server{cfg: cfg, store: st, log: log, webFS: webFS, started: time.Now(), oidc: &oidcProvider{},
		http:    &http.Client{Timeout: 20 * time.Second},
		core:    core.New(cfg.CoreURL, core.Options{DB: st.DB, TokenFile: cfg.DataDir + "/core.token", UserAgent: cfg.UserAgent}),
		syncing: map[int64]bool{}, qc: map[int64]*quickConnect{}, coreUID: map[string]string{}, plays: map[string]*playState{}, jobs: map[string]bool{}}
}

// Bootstrap seeds the first admin (if configured), the box's services and
// the owner's connections from the environment and demo data, then starts
// the background work.
func (s *Server) Bootstrap(ctx context.Context) error {
	if err := s.core.Start(ctx); err != nil {
		return err
	}
	if err := s.loadSecretKey(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.cfg.FilesDir, 0o755); err != nil {
		return fmt.Errorf("files dir: %w", err)
	}
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n == 0 && s.cfg.AuthMode == "local" && s.cfg.AdminEmail != "" && s.cfg.AdminPassword != "" {
		if len(s.cfg.AdminPassword) < 8 {
			return errors.New("ADMIN_PASSWORD must be at least 8 characters")
		}
		h, _ := bcrypt.GenerateFromPassword([]byte(s.cfg.AdminPassword), bcrypt.DefaultCost)
		u := &store.User{Email: s.cfg.AdminEmail, Name: s.cfg.AdminName, PasswordHash: string(h), Role: "admin"}
		if err := s.store.CreateUser(ctx, u); err != nil {
			return err
		}
		st, _ := s.store.GetSettings(ctx, 0)
		st.SetupComplete = true
		if err := s.store.SaveBoxSettings(ctx, st); err != nil {
			return err
		}
		s.log.Info("admin user created from environment", "email", u.Email)
	}
	if err := s.seedBox(ctx); err != nil {
		return err
	}
	if owner := s.store.OwnerID(ctx); owner > 0 {
		if err := s.ownerReady(ctx, owner); err != nil {
			return err
		}
	}
	_ = s.store.PurgeSessions(ctx)
	go s.janitor(ctx)
	if s.core.Configured() {
		go s.coreLoop(ctx)
	}
	return nil
}

// seedBox copies the box's services from the environment into the box
// settings, each once: clearing one under Settings must not bring it back.
func (s *Server) seedBox(ctx context.Context) error {
	st, err := s.store.GetSettings(ctx, 0)
	if err != nil {
		return err
	}
	changed := false
	set := func(name string, dst *string, v string) {
		key := "seeded:box:" + name
		if _, _, done := s.store.CacheGet(ctx, key); done || v == "" {
			return
		}
		if *dst == "" {
			*dst = v
			changed = true
		}
		_ = s.store.CachePut(ctx, key, "1")
	}
	set("seerr_url", &st.SeerrURL, s.cfg.JellyseerrURL)
	set("seerr_public_url", &st.SeerrPublicURL, s.cfg.JellyseerrPublicURL)
	set("seerr_key", &st.SeerrKey, s.cfg.JellyseerrKey)
	set("sonarr_url", &st.SonarrURL, s.cfg.SonarrURL)
	set("sonarr_key", &st.SonarrKey, s.cfg.SonarrKey)
	set("radarr_url", &st.RadarrURL, s.cfg.RadarrURL)
	set("radarr_key", &st.RadarrKey, s.cfg.RadarrKey)
	set("maintainerr_url", &st.MaintainerrURL, s.cfg.MaintainerrURL)
	set("subtitles_url", &st.SubtitlesURL, s.cfg.SubtitlesPublicURL)
	if changed {
		return s.store.SaveBoxSettings(ctx, st)
	}
	return nil
}

// ownerReady runs once the box owner (the first admin) exists: the
// connections from the environment are the owner's — seeded into the owner's
// own settings only, never into anyone else's — and demo data lands in the
// owner's account.
func (s *Server) ownerReady(ctx context.Context, owner int64) error {
	st, err := s.store.GetSettings(ctx, owner)
	if err != nil {
		return err
	}
	changed := false
	set := func(name string, dst *string, v string) {
		key := fmt.Sprintf("seeded:%d:%s", owner, name)
		if _, _, done := s.store.CacheGet(ctx, key); done || v == "" {
			return
		}
		if *dst == "" {
			*dst = v
			changed = true
		}
		_ = s.store.CachePut(ctx, key, "1")
	}
	set("NotifyURL", &st.NotifyURL, s.cfg.NotifyURL)
	set("OpenAIKey", &st.OpenAIKey, s.cfg.OpenAIKey)
	set("OpenAIModel", &st.OpenAIModel, s.cfg.OpenAIModel)
	set("OpenAIBaseURL", &st.OpenAIBaseURL, s.cfg.OpenAIBaseURL)
	if s.cfg.NotifyFormat != "" && st.NotifyURL == s.cfg.NotifyURL && st.NotifyFormat != s.cfg.NotifyFormat {
		st.NotifyFormat = s.cfg.NotifyFormat
		changed = true
	}
	if changed {
		if err := s.store.SaveUserSettings(ctx, owner, st); err != nil {
			return err
		}
	}
	// the owner's Jellyfin account: JELLYFIN_API_KEY + JELLYFIN_USER, or JELLYFIN_USER + JELLYFIN_PASSWORD
	if s.cfg.JellyfinURL != "" && s.cfg.JellyfinUser != "" && (s.cfg.JellyfinAPIKey != "" || s.cfg.JellyfinPassword != "") {
		key := fmt.Sprintf("seeded:%d:jellyfin", owner)
		if _, _, done := s.store.CacheGet(ctx, key); !done {
			if _, err := s.store.GetConnection(ctx, owner); errors.Is(err, store.ErrNotFound) {
				if err := s.seedOwnerJellyfin(ctx, owner); err != nil {
					// Jellyfin may still be starting: try again at the next start
					s.log.Warn("jellyfin account from the environment", "err", err)
					return nil
				}
			}
			_ = s.store.CachePut(ctx, key, "1")
		}
	}
	if s.cfg.DemoData {
		if err := s.seedDemo(ctx, owner); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) seedOwnerJellyfin(ctx context.Context, owner int64) error {
	dev := "pocket-watch-" + store.RandomToken(6)
	c := jellyfin.New(s.cfg.JellyfinURL, "", "", dev)
	conn := &store.Connection{UserID: owner, DeviceID: dev}
	if s.cfg.JellyfinAPIKey != "" {
		c.Token = s.cfg.JellyfinAPIKey
		users, err := c.Users(ctx)
		if err != nil {
			return err
		}
		for _, u := range users {
			if strings.EqualFold(u.Name, s.cfg.JellyfinUser) || u.ID == s.cfg.JellyfinUser {
				conn.Kind, conn.Username, conn.JFUserID = "key", u.Name, u.ID
			}
		}
		if conn.Kind == "" {
			return fmt.Errorf("JELLYFIN_USER %q is not an account on this Jellyfin", s.cfg.JellyfinUser)
		}
		sealed, err := s.seal(s.cfg.JellyfinAPIKey)
		if err != nil {
			return err
		}
		conn.Secret = sealed
	} else {
		res, err := c.AuthenticateByName(ctx, s.cfg.JellyfinUser, s.cfg.JellyfinPassword)
		if err != nil {
			return err
		}
		sealed, err := s.seal(res.AccessToken)
		if err != nil {
			return err
		}
		conn.Kind, conn.Username, conn.JFUserID, conn.Secret = "direct", res.User.Name, res.User.ID, sealed
	}
	if p, err := c.Public(ctx); err == nil {
		conn.ServerName, conn.ServerVersion = p.ServerName, p.Version
	}
	if err := s.store.SaveConnection(ctx, conn); err != nil {
		return err
	}
	s.log.Info("jellyfin account seeded from the environment", "user", conn.Username, "kind", conn.Kind)
	return nil
}

// forgetUser drops in-memory state of a deleted account.
func (s *Server) forgetUser(id int64) {
	s.qcMu.Lock()
	delete(s.qc, id)
	s.qcMu.Unlock()
}

// janitor runs the background work: every SYNC_EVERY each connected person's
// history is read from Jellyfin; every 15 minutes requests are followed in
// Jellyseerr; on Monday mornings the weekly recaps go out.
func (s *Server) janitor(ctx context.Context) {
	every := s.cfg.SyncEvery
	if every <= 0 {
		every = 10 * time.Minute
	}
	first := time.NewTimer(20 * time.Second)
	tick := time.NewTicker(every)
	slow := time.NewTicker(15 * time.Minute)
	defer tick.Stop()
	defer slow.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			s.syncAll(ctx)
			s.followRequests(ctx)
		case <-tick.C:
			s.syncAll(ctx)
			s.sweepPlays(ctx)
		case <-slow.C:
			s.followRequests(ctx)
			s.weeklyRecaps(ctx)
		}
	}
}

// job runs fn in the background unless a job with that key is running.
func (s *Server) job(key string, fn func(ctx context.Context)) bool {
	s.jobsMu.Lock()
	if s.jobs[key] {
		s.jobsMu.Unlock()
		return false
	}
	s.jobs[key] = true
	s.jobsMu.Unlock()
	go func() {
		defer func() {
			s.jobsMu.Lock()
			delete(s.jobs, key)
			s.jobsMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		fn(ctx)
	}()
	return true
}

// ---- Pocket Core ----

// coreLoop registers with Core on start (retrying until it answers) and
// re-registers every 30 minutes so version and last-seen stay fresh.
func (s *Server) coreLoop(ctx context.Context) {
	wait := 5 * time.Second
	for {
		err := s.registerWithCore(ctx)
		s.coreMu.Lock()
		s.coreOK, s.coreErr = err == nil, ""
		if err != nil {
			s.coreErr = err.Error()
		}
		s.coreMu.Unlock()
		if err != nil {
			s.log.Warn("pocket core: registration failed, retrying", "err", err, "in", wait.String())
		} else {
			wait = 30 * time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err != nil && wait < 5*time.Minute {
			wait *= 2
		}
	}
}

// eventTopics are what Watch publishes on the Core bus.
var eventTopics = []string{"watch.watched", "watch.requested", "watch.request_available", "watch.watchlist_added"}

func (s *Server) registerWithCore(ctx context.Context) error {
	m := core.Manifest{
		Slug: "watch", Name: "Pocket Watch",
		Description: "Films and shows for the whole household: Jellyfin underneath, with a fast player, continue watching and next up, a watch history it owns with stats, a watchlist, requests through Jellyseerr that tell you when they arrive, upcoming episodes, and \"what should we watch tonight?\" with AI.",
		Icon:        "/favicon.svg", Version: Version,
		URL: s.cfg.CoreAppURL, PublicURL: s.cfg.BaseURL, HealthPath: "/healthz", EnvPrefix: "WATCH",
		Publishes: eventTopics,
		Offers:    linkOffers,
		Uses:      []core.Use{core.InferenceUse()},
		Services: []core.ServiceUse{
			{Type: "jellyfin", Reason: "Your films and shows: Pocket Watch plays them with your own Jellyfin account, so resume points and watched marks stay yours"},
		},
	}
	if s.cfg.BaseURL != "" {
		// ask Core for a relying-party client so "Sign in with Pocket Core" needs no configuration
		m.OIDC = &core.OIDCRequest{RedirectURIs: []string{s.oidcCallbackURL()}}
	}
	reg, err := s.core.Register(ctx, s.cfg.CoreRegistrationKey, m)
	if err != nil {
		return err
	}
	if reg.Token != "" {
		s.log.Info("pocket core: registered, app token stored", "core", s.cfg.CoreURL)
	} else {
		s.log.Debug("pocket core: re-registered", "core", s.cfg.CoreURL)
	}
	return nil
}

// coreStatus summarises the Pocket Core link for the About page.
func (s *Server) coreStatus() map[string]any {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	return map[string]any{"configured": s.core.Configured(), "url": s.cfg.CoreURL, "registered": s.coreOK, "error": s.coreErr}
}

// coreConnected is "configured AND registered": only then are Core-only choices live.
func (s *Server) coreConnected() bool {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	return s.core.Configured() && s.coreOK
}

// publish puts an event on the Core bus for one person (no-op without Core).
func (s *Server) publish(ctx context.Context, u *store.User, topic, subject string, payload map[string]any) {
	if !s.core.Configured() || u == nil {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["user_email"] = u.Email
	if err := s.core.Publish(ctx, topic, subject, payload); err != nil {
		s.log.Debug("pocket core: publish failed", "topic", topic, "err", err)
	}
}
