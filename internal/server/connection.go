package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/jellyfin"
	"github.com/abjelosevic88/pocket-homelab-watch/internal/store"
)

// ---- Jellyfin, per person ----
//
// Every person watches with their OWN Jellyfin account, so resume points,
// watched marks and favourites stay theirs and every other Jellyfin app
// (TV, phone, Infuse) agrees with Pocket Watch. Ways in:
//
//   - direct: they sign in here once, with their password or Quick Connect;
//     only the access token Jellyfin hands out is kept (sealed with
//     DATA_DIR/secret.key), never the password. It shows under
//     Dashboard → Devices as "Pocket Watch".
//   - key: an API key plus the account it acts for (the owner's, from
//     JELLYFIN_API_KEY + JELLYFIN_USER).
//   - core: the Jellyfin login Pocket Core holds for them (Core → My logins:
//     a token, or an API key with their user id). Nothing is stored here.
//
// With none of these the library stays empty and Settings → Jellyfin says why.

// jfConn is how one request reaches the person's Jellyfin.
type jfConn struct {
	c      *jellyfin.Client
	via    string // direct | key | core
	public string // what a browser opens ("Open in Jellyfin")
	key    string // server URL + Jellyfin user: the history belongs to exactly this
	user   string // the Jellyfin account's name
	apiKey bool   // an API key acts as an administrator: user data is written explicitly
}

// errNoJellyfin means the person has no working way to Jellyfin.
type errNoJellyfin struct{ msg string }

func (e *errNoJellyfin) Error() string { return e.msg }

func isNoJellyfin(err error) bool {
	var e *errNoJellyfin
	return errors.As(err, &e)
}

// serverBase is the address used for a direct account.
func (s *Server) serverBase(c *store.Connection) string {
	if c != nil && c.ServerURL != "" {
		return c.ServerURL
	}
	return s.cfg.JellyfinURL
}

func validServerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return errors.New("the Jellyfin address must be http(s)://host[:port]")
	}
	return nil
}

func coreReason(err error, service string) string {
	var le *core.LinkError
	if errors.As(err, &le) {
		switch le.Reason {
		case "off_person":
			return "you switched Pocket Watch's access to " + service + " off in Pocket Core (Settings → Apps acting for me)"
		case "off_admin":
			return "Pocket Watch's access to " + service + " is switched off in Pocket Core (Links)"
		case "no_account":
			return "Pocket Core has no " + service + " login for you yet: add it under Settings → My logins in Pocket Core, or sign in here"
		case "not_in_audience":
			return "you do not have access to " + service + " in Pocket Core"
		case "target_missing":
			return service + " is not set up in Pocket Core"
		case "not_declared":
			return "Pocket Core has not seen this Pocket Watch version yet; it re-registers within a minute of starting"
		case "unknown_user":
			return "your email is not a person in Pocket Core"
		}
		return le.Message
	}
	return "Pocket Core did not answer: " + err.Error()
}

// jfFor connects to the person's Jellyfin account.
func (s *Server) jfFor(ctx context.Context, u *store.User) (*jfConn, error) {
	if u == nil {
		return nil, &errNoJellyfin{"nobody signed in"}
	}
	c, err := s.store.GetConnection(ctx, u.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if c != nil && c.Kind == "off" {
		return nil, &errNoJellyfin{"Jellyfin is switched off in Settings → Jellyfin"}
	}
	if c != nil && (c.Kind == "direct" || c.Kind == "key") {
		base := s.serverBase(c)
		if base == "" {
			return nil, &errNoJellyfin{"no Jellyfin address: set JELLYFIN_URL on the server or enter one in Settings → Jellyfin"}
		}
		tok, err := s.open(c.Secret)
		if err != nil {
			return nil, &errNoJellyfin{err.Error()}
		}
		if tok == "" || c.JFUserID == "" {
			return nil, &errNoJellyfin{"sign in to Jellyfin again in Settings → Jellyfin"}
		}
		public := s.cfg.JellyfinPublicURL
		if c.ServerURL != "" {
			public = c.ServerURL
		}
		jc := jellyfin.New(base, tok, c.JFUserID, firstNonEmpty(c.DeviceID, "pocket-watch"))
		jc.Version = Version
		return &jfConn{c: jc, via: c.Kind, public: firstNonEmpty(public, base), key: base + "|" + c.JFUserID, user: c.Username, apiKey: c.Kind == "key"}, nil
	}
	// kind core, or nothing chosen yet: Pocket Core when the box has one
	if !s.core.Configured() {
		return nil, &errNoJellyfin{"sign in to Jellyfin in Settings → Jellyfin"}
	}
	cred, err := s.core.ServiceFor(ctx, u.Email, "jellyfin")
	if err != nil {
		return nil, &errNoJellyfin{coreReason(err, "Jellyfin")}
	}
	tok := firstNonEmpty(cred.Key, cred.Fields["API_KEY"])
	if cred.URL == "" || tok == "" {
		return nil, &errNoJellyfin{"Pocket Core knows your Jellyfin account but holds no token for it: sign in here once (password or Quick Connect), or add a token under Settings → My logins in Pocket Core"}
	}
	base := cred.URL
	// Core may hand out the public address; inside the box the direct one is faster and needs no TLS
	if s.cfg.JellyfinURL != "" && cred.PublicURL != "" && strings.EqualFold(strings.TrimRight(cred.URL, "/"), strings.TrimRight(cred.PublicURL, "/")) {
		base = s.cfg.JellyfinURL
	}
	jc := jellyfin.New(base, tok, cred.Fields["USER_ID"], "pocket-watch-core-"+store.HashToken(u.Email)[:10])
	jc.Version = Version
	name := ""
	if jc.UserID == "" {
		// a person's own token knows whose it is
		id, n, err := s.coreTokenUser(ctx, jc, tok)
		if err != nil {
			return nil, &errNoJellyfin{"Pocket Core's Jellyfin login for you has no user id and the token doesn't say whose it is: add USER_ID under My logins in Pocket Core, or sign in here"}
		}
		jc.UserID, name = id, n
	}
	return &jfConn{c: jc, via: "core", public: firstNonEmpty(cred.PublicURL, s.cfg.JellyfinPublicURL, cred.URL), key: strings.TrimRight(cred.URL, "/") + "|" + jc.UserID, user: name, apiKey: jc.UserID != "" && cred.Fields["USER_ID"] != ""}, nil
}

// coreTokenUser finds the Jellyfin account a Core-held token belongs to (cached).
func (s *Server) coreTokenUser(ctx context.Context, jc *jellyfin.Client, tok string) (string, string, error) {
	k := store.HashToken(tok)
	s.coreUIDMu.Lock()
	v, ok := s.coreUID[k]
	s.coreUIDMu.Unlock()
	if ok {
		id, name, _ := strings.Cut(v, "\x00")
		return id, name, nil
	}
	me, err := jc.Me(ctx)
	if err != nil || me.ID == "" {
		if err == nil {
			err = errors.New("no user")
		}
		return "", "", err
	}
	s.coreUIDMu.Lock()
	s.coreUID[k] = me.ID + "\x00" + me.Name
	s.coreUIDMu.Unlock()
	return me.ID, me.Name, nil
}

// jf is jfFor for the signed-in person, writing the error itself.
func (s *Server) jf(w http.ResponseWriter, r *http.Request) *jfConn {
	jc, err := s.jfFor(r.Context(), userFrom(r.Context()))
	if err != nil {
		if isNoJellyfin(err) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "not_connected"})
			return nil
		}
		s.fail(w, err, "jellyfin")
		return nil
	}
	return jc
}

// jfErr writes a failed Jellyfin call.
func (s *Server) jfErr(w http.ResponseWriter, err error) {
	switch {
	case jellyfin.IsNotFound(err):
		writeErr(w, http.StatusNotFound, "not found")
	case jellyfin.IsAuth(err):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "not_connected"})
	case errors.Is(err, context.Canceled):
	default:
		s.log.Warn("jellyfin", "err", err)
		writeErr(w, http.StatusBadGateway, err.Error())
	}
}

// ---- Settings → Jellyfin ----

func (s *Server) handleConnection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	c, _ := s.store.GetConnection(ctx, u.ID)
	out := map[string]any{
		"server_url":      s.cfg.JellyfinURL != "",
		"core":            s.coreConnected(),
		"core_configured": s.core.Configured(),
		"sync":            s.store.GetSync(ctx, u.ID),
		"syncing":         s.isSyncing(u.ID),
	}
	mode := "core"
	if c != nil {
		mode = c.Kind
		if mode == "key" {
			mode = "direct"
		}
		out["username"] = c.Username
		out["custom_url"] = c.ServerURL
	} else if !s.core.Configured() {
		mode = "direct"
	}
	out["mode"] = mode
	if base := firstNonEmpty(func() string {
		if c != nil {
			return c.ServerURL
		}
		return ""
	}(), s.cfg.JellyfinURL); base != "" {
		qc := jellyfin.New(base, "", "", "pocket-watch")
		qctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		out["quick_connect"] = qc.QuickConnectEnabled(qctx)
		cancel()
	}
	jc, err := s.jfFor(ctx, u)
	if err != nil {
		out["connected"] = false
		out["message"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	me, err := jc.c.Me(ctx)
	if err != nil {
		out["connected"] = false
		out["message"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["connected"] = true
	out["via"] = jc.via
	out["api_key"] = jc.apiKey
	out["account"] = me.Name
	out["admin"] = me.Policy.IsAdministrator
	if p, err := jc.c.Public(ctx); err == nil {
		out["server_name"], out["server_version"] = p.ServerName, p.Version
	}
	if n, err := jc.c.Counts(ctx); err == nil {
		out["counts"] = n
	}
	out["public_url"] = jc.public
	writeJSON(w, http.StatusOK, out)
}

// handleConnect signs the person in with a password (keeping only the token)
// or switches to Pocket Core / off.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		Mode     string `json:"mode"` // direct | core | off
		URL      string `json:"url"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	old, _ := s.store.GetConnection(ctx, u.ID)
	switch in.Mode {
	case "off", "core":
		if in.Mode == "core" && !s.core.Configured() {
			writeErr(w, http.StatusBadRequest, "there is no Pocket Core on this box")
			return
		}
		s.signOutOld(ctx, old)
		if err := s.store.SaveConnection(ctx, &store.Connection{UserID: u.ID, Kind: in.Mode}); err != nil {
			s.fail(w, err, "save connection")
			return
		}
	case "direct":
		base, custom, ok := s.pickBase(w, in.URL)
		if !ok {
			return
		}
		in.Username = strings.TrimSpace(in.Username)
		if in.Username == "" {
			writeErr(w, http.StatusBadRequest, "enter your Jellyfin username")
			return
		}
		dev := "pocket-watch-" + store.RandomToken(6)
		jc := jellyfin.New(base, "", "", dev)
		jc.Version = Version
		res, err := jc.AuthenticateByName(ctx, in.Username, in.Password)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.saveSignIn(ctx, u.ID, old, base, custom, dev, res); err != nil {
			s.fail(w, err, "save connection")
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "mode is direct, core or off")
		return
	}
	s.historySoon(u.ID)
	s.handleConnection(w, r)
}

// pickBase is the Jellyfin address a sign-in goes to (the box's, or one typed in).
func (s *Server) pickBase(w http.ResponseWriter, raw string) (base, custom string, ok bool) {
	custom = strings.TrimRight(strings.TrimSpace(raw), "/")
	if custom != "" {
		if err := validServerURL(custom); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return "", "", false
		}
	}
	base = firstNonEmpty(custom, s.cfg.JellyfinURL)
	if base == "" {
		writeErr(w, http.StatusBadRequest, "enter the Jellyfin address")
		return "", "", false
	}
	return base, custom, true
}

func (s *Server) saveSignIn(ctx context.Context, uid int64, old *store.Connection, base, custom, dev string, res *jellyfin.AuthResult) error {
	sealed, err := s.seal(res.AccessToken)
	if err != nil {
		return err
	}
	conn := &store.Connection{UserID: uid, Kind: "direct", ServerURL: custom, Username: res.User.Name, JFUserID: res.User.ID, Secret: sealed, DeviceID: dev}
	if p, err := jellyfin.New(base, "", "", dev).Public(ctx); err == nil {
		conn.ServerName, conn.ServerVersion = p.ServerName, p.Version
	}
	s.signOutOld(ctx, old)
	return s.store.SaveConnection(ctx, conn)
}

// signOutOld ends the session of a token this app received, so Jellyfin's
// device list doesn't fill up with stale Pocket Watch entries.
func (s *Server) signOutOld(ctx context.Context, old *store.Connection) {
	if old == nil || old.Kind != "direct" || old.Secret == "" {
		return
	}
	tok, err := s.open(old.Secret)
	if err != nil || tok == "" {
		return
	}
	jc := jellyfin.New(s.serverBase(old), tok, old.JFUserID, old.DeviceID)
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = jc.Logout(cctx)
}

// ---- Quick Connect: sign in by approving a code in another Jellyfin app ----

type quickConnect struct {
	base, custom, device, secret, code string
	started                            time.Time
}

// handleQuickConnectStart asks Jellyfin for a code to approve.
func (s *Server) handleQuickConnectStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		URL string `json:"url"`
	}
	_ = decode(r, &in)
	base, custom, ok := s.pickBase(w, in.URL)
	if !ok {
		return
	}
	dev := "pocket-watch-" + store.RandomToken(6)
	jc := jellyfin.New(base, "", "", dev)
	jc.Version = Version
	q, err := jc.QuickConnectInitiate(ctx)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Quick Connect: "+err.Error()+" (it may be switched off under Dashboard → General)")
		return
	}
	s.qcMu.Lock()
	s.qc[u.ID] = &quickConnect{base: base, custom: custom, device: dev, secret: q.Secret, code: q.Code, started: time.Now()}
	s.qcMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"code": q.Code, "done": false})
}

// handleQuickConnectPoll finishes the sign-in once the code was approved.
func (s *Server) handleQuickConnectPoll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	s.qcMu.Lock()
	q := s.qc[u.ID]
	s.qcMu.Unlock()
	if q == nil || time.Since(q.started) > 10*time.Minute {
		writeErr(w, http.StatusGone, "the code expired: start again")
		return
	}
	jc := jellyfin.New(q.base, "", "", q.device)
	jc.Version = Version
	st, err := jc.QuickConnectState(ctx, q.secret)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if !st.Authenticated {
		writeJSON(w, http.StatusOK, map[string]any{"code": q.code, "done": false})
		return
	}
	res, err := jc.AuthenticateWithQuickConnect(ctx, q.secret)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	old, _ := s.store.GetConnection(ctx, u.ID)
	if err := s.saveSignIn(ctx, u.ID, old, q.base, q.custom, q.device, res); err != nil {
		s.fail(w, err, "save connection")
		return
	}
	s.qcMu.Lock()
	delete(s.qc, u.ID)
	s.qcMu.Unlock()
	s.historySoon(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"done": true, "account": res.User.Name})
}
