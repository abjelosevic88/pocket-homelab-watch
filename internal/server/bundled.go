package server

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abjelosevic88/pocket-homelab-watch/internal/jellyfin"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// ---- The Jellyfin that comes with Pocket Watch (JELLYFIN_BUNDLED) ----
//
// The sample docker-compose.yml can start Jellyfin next to Pocket Watch (the
// "jellyfin" profile, on in the sample .env and in Pocket Core's installer),
// with no port of its own, so installing Pocket Watch is all it takes. On its first
// start Pocket Watch does what a person would do in Jellyfin's setup wizard:
// an administrator ("pocket-watch", a random password sealed with
// secret.key), an API key for itself, and Films and Shows libraries over the
// folders the compose file mounts. Everyone who uses Pocket Watch gets their
// own Jellyfin account the first time (connection kind "bundled"), so resume
// points and watched marks stay theirs; Pocket Watch acts for them with its
// key. A Jellyfin someone already set up by hand is left alone: people sign
// in to it under Settings → Jellyfin like to any other. That is how
// JELLYFIN_BUNDLED=auto (the sample's) tells the two apart, so the same compose
// file runs with the bundled Jellyfin or pointed at your own.

const (
	bundledAdmin   = "pocket-watch"
	kvBundledAdmin = "bundled:admin"     // sealed password of the administrator
	kvBundledKey   = "bundled:key"       // sealed API key
	kvBundledLibs  = "bundled:libraries" // the first libraries were added
	kvBundledUser  = "bundled:user:"     // + person id → Jellyfin user id NUL name
)

// bundledLibraries are made on a fresh server, over the compose file's mounts.
var bundledLibraries = []struct{ name, kind, path string }{
	{"Films", "movies", "/media/movies"},
	{"Shows", "tvshows", "/media/shows"},
}

type bundledState struct {
	on    atomic.Bool // this app sets up and runs JELLYFIN_URL's accounts
	mu    sync.Mutex
	key   string // API key once the server is set up
	state string // why it isn't, for Settings
	users sync.Mutex
}

// bundledOn: everyone gets an account on the Jellyfin that came with Pocket Watch.
func (s *Server) bundledOn() bool {
	return s.cfg.JellyfinBundled == "true" || s.bundled.on.Load()
}

func (s *Server) setBundled(key, state string) {
	s.bundled.mu.Lock()
	s.bundled.key, s.bundled.state = key, state
	s.bundled.mu.Unlock()
}

func (s *Server) bundledKey() (string, string) {
	s.bundled.mu.Lock()
	defer s.bundled.mu.Unlock()
	return s.bundled.key, s.bundled.state
}

// errBundledByHand stops the setup for good.
var errBundledByHand = errors.New("this Jellyfin was set up by hand, not by Pocket Watch: sign in to it under Settings → Jellyfin, or set JELLYFIN_API_KEY")

// errNotBundled: auto, and JELLYFIN_URL is someone's own Jellyfin.
var errNotBundled = errors.New("not bundled")

// bundledLoop sets the server up, waiting for it to start first.
func (s *Server) bundledLoop(ctx context.Context) {
	s.setBundled("", "the Jellyfin that comes with Pocket Watch is starting")
	wait := 2 * time.Second
	for {
		err := s.setupBundled(ctx)
		if err == nil {
			return
		}
		if errors.Is(err, errNotBundled) {
			s.setBundled("", "")
			s.log.Info("jellyfin was set up before; people sign in to it under Settings → Jellyfin", "url", s.cfg.JellyfinURL)
			return
		}
		if errors.Is(err, errBundledByHand) {
			s.setBundled("", err.Error())
			s.log.Warn("bundled jellyfin", "err", err)
			return
		}
		s.setBundled("", "the Jellyfin that comes with Pocket Watch is not ready: "+err.Error())
		if wait >= 30*time.Second && s.bundledOn() {
			s.log.Warn("bundled jellyfin not ready", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait < time.Minute {
			wait *= 2
		}
	}
}

func (s *Server) setupBundled(ctx context.Context) error {
	jc := jellyfin.New(s.cfg.JellyfinURL, "", "", "pocket-watch-setup")
	jc.Version = Version
	pub, err := jc.Public(ctx)
	if err != nil {
		return err
	}
	if pub.StartupWizardCompleted && s.cfg.JellyfinBundled == "auto" && s.openKV(ctx, kvBundledAdmin) == "" {
		return errNotBundled
	}
	key := s.cfg.JellyfinAPIKey
	if key == "" {
		key = s.openKV(ctx, kvBundledKey)
	}
	if !pub.StartupWizardCompleted {
		// A starting Jellyfin can say "not set up" before it has read its
		// settings: only a wizard that lets us in proves it is fresh.
		if err := jc.StartupBegin(ctx); err != nil {
			return err
		}
		s.bundled.on.Store(true)
		pw := s.openKV(ctx, kvBundledAdmin)
		if pw == "" {
			// kept before the password is set, so a restart halfway signs in with the same one
			pw = store.RandomToken(24)
			if err := s.sealKV(ctx, kvBundledAdmin, pw); err != nil {
				return err
			}
		}
		if err := jc.StartupFinish(ctx, bundledAdmin, pw); err != nil {
			return err
		}
		s.log.Info("bundled jellyfin set up", "admin", bundledAdmin)
	}
	if key == "" {
		pw := s.openKV(ctx, kvBundledAdmin)
		if pw == "" {
			return errBundledByHand
		}
		res, err := jc.AuthenticateByName(ctx, bundledAdmin, pw)
		if err != nil {
			return fmt.Errorf("signing in as %s: %w", bundledAdmin, err)
		}
		jc.Token = res.AccessToken
		key, err = jc.CreateAPIKey(ctx, jellyfin.ClientName)
		_ = jc.Logout(ctx)
		if err != nil {
			return fmt.Errorf("API key: %w", err)
		}
		if err := s.sealKV(ctx, kvBundledKey, key); err != nil {
			return err
		}
	}
	jc.Token = key
	if _, _, done := s.store.CacheGet(ctx, kvBundledLibs); !done {
		folders, err := jc.VirtualFolders(ctx)
		if err != nil {
			return err
		}
		if len(folders) == 0 { // never over libraries someone made
			for _, l := range bundledLibraries {
				if err := jc.AddVirtualFolder(ctx, l.name, l.kind, l.path); err != nil {
					s.log.Warn("bundled jellyfin library", "name", l.name, "path", l.path, "err", err)
					continue
				}
				s.log.Info("bundled jellyfin library added", "name", l.name, "path", l.path)
			}
		}
		_ = s.store.CachePut(ctx, kvBundledLibs, "1")
	}
	s.setBundled(key, "")
	return nil
}

func (s *Server) openKV(ctx context.Context, k string) string {
	v, _, ok := s.store.CacheGet(ctx, k)
	if !ok {
		return ""
	}
	plain, err := s.open(v)
	if err != nil {
		return ""
	}
	return plain
}

func (s *Server) sealKV(ctx context.Context, k, v string) error {
	sealed, err := s.seal(v)
	if err != nil {
		return err
	}
	return s.store.CachePut(ctx, k, sealed)
}

// jfBundled is the person's own account on the bundled Jellyfin, made the
// first time they need it.
func (s *Server) jfBundled(ctx context.Context, u *store.User) (*jfConn, error) {
	key, state := s.bundledKey()
	if key == "" {
		return nil, &errNoJellyfin{firstNonEmpty(state, "the Jellyfin that comes with Pocket Watch is starting")}
	}
	id, name, err := s.bundledUser(ctx, u, key)
	if err != nil {
		return nil, &errNoJellyfin{"could not make your account on the Jellyfin that comes with Pocket Watch: " + err.Error()}
	}
	jc := jellyfin.New(s.cfg.JellyfinURL, key, id, "pocket-watch")
	jc.Version = Version
	return &jfConn{c: jc, via: "bundled", public: s.cfg.JellyfinPublicURL, key: s.cfg.JellyfinURL + "|" + id, user: name, apiKey: true}, nil
}

func (s *Server) bundledUser(ctx context.Context, u *store.User, key string) (string, string, error) {
	k := kvBundledUser + strconv.FormatInt(u.ID, 10)
	if v, _, ok := s.store.CacheGet(ctx, k); ok {
		id, name, _ := strings.Cut(v, "\x00")
		return id, name, nil
	}
	s.bundled.users.Lock()
	defer s.bundled.users.Unlock()
	if v, _, ok := s.store.CacheGet(ctx, k); ok {
		id, name, _ := strings.Cut(v, "\x00")
		return id, name, nil
	}
	jc := jellyfin.New(s.cfg.JellyfinURL, key, "", "pocket-watch")
	jc.Version = Version
	users, err := jc.Users(ctx)
	if err != nil {
		return "", "", err
	}
	taken := map[string]bool{strings.ToLower(bundledAdmin): true}
	for _, x := range users {
		taken[strings.ToLower(x.Name)] = true
	}
	base := bundledName(u)
	name := base
	for i := 2; taken[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%s %d", base, i)
	}
	// Nobody signs in to it with a password: Pocket Watch acts with its key.
	ju, err := jc.CreateUser(ctx, name, store.RandomToken(24))
	if err != nil {
		return "", "", err
	}
	if err := s.store.CachePut(ctx, k, ju.ID+"\x00"+ju.Name); err != nil {
		return "", "", err
	}
	s.log.Info("bundled jellyfin account made", "user", u.Email, "jellyfin", ju.Name)
	return ju.ID, ju.Name, nil
}

// bundledName is a Jellyfin user name for the person.
func bundledName(u *store.User) string {
	n := strings.TrimSpace(u.Name)
	if n == "" {
		n, _, _ = strings.Cut(u.Email, "@")
	}
	n = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>"'\/`, r) {
			return -1
		}
		return r
	}, n)
	if n == "" {
		n = "Person " + strconv.FormatInt(u.ID, 10)
	}
	return n
}
