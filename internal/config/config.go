// Package config loads runtime configuration from environment variables.
//
// Every option has a sensible default so that `docker run` with a single
// volume works out of the box. See docs/configuration.md for the full list.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings.
type Config struct {
	Host           string
	Port           int
	BaseURL        string
	DataDir        string
	DBPath         string
	FilesDir       string
	LogLevel       string
	LogFormat      string // text | json
	TrustProxy     bool
	MetricsEnabled bool

	MaxUploadBytes int64

	AuthMode             string // local | proxy | none
	AuthProxyHeader      string
	AuthProxyEmailHeader string
	SessionTTL           time.Duration
	SecureCookies        bool
	AdminEmail           string
	AdminPassword        string
	AdminName            string

	// Sign in with an OpenID Connect provider. On a box with Pocket Core none
	// of these are needed: the app uses the client Core issues on registration.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCName         string // button label
	OIDCAutoCreate   bool   // unknown emails get a member account

	// Jellyfin. Each person signs in with their own account (password or
	// Quick Connect); with Pocket Core the account can come from Core instead.
	JellyfinURL       string
	JellyfinPublicURL string // what a browser opens ("Open in Jellyfin")
	// The owner's account, copied once into the owner's own connection: an API
	// key plus the Jellyfin user (name or id) it acts as, or a password.
	JellyfinAPIKey   string
	JellyfinUser     string
	JellyfinPassword string

	// Box-wide services an admin sets once (also editable under Settings):
	// requests go to Jellyseerr as each person's own Jellyseerr user, upcoming
	// episodes and films come from Sonarr and Radarr, "leaving soon" from Maintainerr.
	JellyseerrURL       string
	JellyseerrPublicURL string
	JellyseerrKey       string
	SonarrURL           string
	SonarrKey           string
	RadarrURL           string
	RadarrKey           string
	MaintainerrURL      string
	// Pocket Subtitles (admins): "subtitles out of sync?" in the player opens its nudge page.
	SubtitlesPublicURL string

	// The owner's other integrations, copied once into the owner's own
	// settings; the values saved under Settings win.
	NotifyURL     string
	NotifyFormat  string // apprise | ntfy | json
	OpenAIKey     string
	OpenAIModel   string
	OpenAIBaseURL string

	// How often each person's watch history is read from Jellyfin.
	SyncEvery time.Duration

	// Outbound lookups identify as this.
	UserAgent string

	// Pocket Core: when CORE_URL is set the app registers itself on start,
	// publishes events and accepts the tokens Core hands to paired phones.
	CoreURL             string
	CoreRegistrationKey string
	CoreAppURL          string // how Core reaches this app; defaults to BASE_URL

	DemoData bool
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envBool(key string, def bool) bool {
	switch strings.ToLower(env(key, "")) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envInt(key string, def int) int {
	if v := env(key, ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v := env(key, ""); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := env(key, ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// Load reads configuration from the environment.
func Load() (*Config, error) {
	dataDir := env("DATA_DIR", "./data")
	c := &Config{
		Host:           env("HOST", "0.0.0.0"),
		Port:           envInt("PORT", 8080),
		BaseURL:        strings.TrimRight(env("BASE_URL", ""), "/"),
		DataDir:        dataDir,
		DBPath:         env("DB_PATH", dataDir+"/pocket-watch.db"),
		FilesDir:       env("FILES_DIR", dataDir+"/files"),
		LogLevel:       strings.ToLower(env("LOG_LEVEL", "info")),
		LogFormat:      strings.ToLower(env("LOG_FORMAT", "text")),
		TrustProxy:     envBool("TRUST_PROXY", true),
		MetricsEnabled: envBool("METRICS_ENABLED", true),

		MaxUploadBytes: envInt64("MAX_UPLOAD_BYTES", 20<<20),

		AuthMode:             strings.ToLower(env("AUTH_MODE", "local")),
		AuthProxyHeader:      env("AUTH_PROXY_HEADER", "Remote-User"),
		AuthProxyEmailHeader: env("AUTH_PROXY_EMAIL_HEADER", "Remote-Email"),
		SessionTTL:           envDuration("SESSION_TTL", 720*time.Hour),
		SecureCookies:        envBool("SECURE_COOKIES", false),
		AdminEmail:           strings.ToLower(env("ADMIN_EMAIL", "")),
		AdminPassword:        env("ADMIN_PASSWORD", ""),
		AdminName:            env("ADMIN_NAME", "Admin"),
		OIDCIssuer:           strings.TrimRight(env("OIDC_ISSUER", ""), "/"),
		OIDCClientID:         env("OIDC_CLIENT_ID", ""),
		OIDCClientSecret:     env("OIDC_CLIENT_SECRET", ""),
		OIDCName:             env("OIDC_NAME", "single sign-on"),
		OIDCAutoCreate:       envBool("OIDC_AUTO_CREATE", true),

		JellyfinURL:       strings.TrimRight(env("JELLYFIN_URL", ""), "/"),
		JellyfinPublicURL: strings.TrimRight(env("JELLYFIN_PUBLIC_URL", ""), "/"),
		JellyfinAPIKey:    env("JELLYFIN_API_KEY", ""),
		JellyfinUser:      env("JELLYFIN_USER", ""),
		JellyfinPassword:  env("JELLYFIN_PASSWORD", ""),

		JellyseerrURL:       strings.TrimRight(env("JELLYSEERR_URL", ""), "/"),
		JellyseerrPublicURL: strings.TrimRight(env("JELLYSEERR_PUBLIC_URL", ""), "/"),
		JellyseerrKey:       env("JELLYSEERR_API_KEY", ""),
		SonarrURL:           strings.TrimRight(env("SONARR_URL", ""), "/"),
		SonarrKey:           env("SONARR_API_KEY", ""),
		RadarrURL:           strings.TrimRight(env("RADARR_URL", ""), "/"),
		RadarrKey:           env("RADARR_API_KEY", ""),
		MaintainerrURL:      strings.TrimRight(env("MAINTAINERR_URL", ""), "/"),
		SubtitlesPublicURL:  strings.TrimRight(env("SUBTITLES_PUBLIC_URL", ""), "/"),

		NotifyURL:     env("NOTIFY_URL", ""),
		NotifyFormat:  strings.ToLower(env("NOTIFY_FORMAT", "")),
		OpenAIKey:     env("OPENAI_API_KEY", ""),
		OpenAIModel:   env("OPENAI_MODEL", ""),
		OpenAIBaseURL: strings.TrimRight(env("OPENAI_BASE_URL", ""), "/"),
		SyncEvery:     envDuration("SYNC_EVERY", 10*time.Minute),

		UserAgent: env("USER_AGENT", "PocketWatch/1.0 (+https://github.com/abjelosevic88/pocket-homelab-watch)"),

		CoreURL:             strings.TrimRight(env("CORE_URL", ""), "/"),
		CoreRegistrationKey: env("CORE_REGISTRATION_KEY", ""),
		CoreAppURL:          strings.TrimRight(env("CORE_APP_URL", ""), "/"),

		DemoData: envBool("DEMO_DATA", false),
	}
	if c.CoreAppURL == "" {
		c.CoreAppURL = c.BaseURL
	}
	switch c.AuthMode {
	case "local", "proxy", "none":
	default:
		return nil, fmt.Errorf("AUTH_MODE must be one of local, proxy, none (got %q)", c.AuthMode)
	}
	if c.Port <= 0 || c.Port > 65535 {
		return nil, fmt.Errorf("PORT out of range: %d", c.Port)
	}
	if c.OIDCIssuer != "" && (c.OIDCClientID == "" || c.BaseURL == "") {
		return nil, fmt.Errorf("OIDC_ISSUER needs OIDC_CLIENT_ID and BASE_URL (the callback is BASE_URL/api/v1/auth/oidc/callback)")
	}
	if c.SyncEvery < time.Minute {
		c.SyncEvery = time.Minute
	}
	return c, nil
}

// Addr returns host:port.
func (c *Config) Addr() string { return fmt.Sprintf("%s:%d", c.Host, c.Port) }
