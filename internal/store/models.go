package store

// User is an account on this box.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Connection is how a person reaches Jellyfin.
type Connection struct {
	UserID        int64  `json:"-"`
	Kind          string `json:"kind"` // direct | key | core | off
	ServerURL     string `json:"server_url"`
	Username      string `json:"username"`
	JFUserID      string `json:"jf_user_id"`
	Secret        string `json:"-"` // sealed token or API key
	DeviceID      string `json:"-"`
	ServerName    string `json:"server_name"`
	ServerVersion string `json:"server_version"`
	UpdatedAt     string `json:"updated_at"`
}

// Watch is one title watched on one day.
type Watch struct {
	ID        int64    `json:"id"`
	ItemID    string   `json:"item_id"`
	Kind      string   `json:"kind"` // movie | episode
	Title     string   `json:"title"`
	SeriesID  string   `json:"series_id"`
	Series    string   `json:"series"`
	Season    int      `json:"season"`
	Episode   int      `json:"episode"`
	Year      int      `json:"year"`
	Genres    []string `json:"genres"`
	Runtime   int      `json:"runtime"`
	WatchedAt string   `json:"watched_at"`
	Day       string   `json:"day"`
	Position  int      `json:"position"`
	Seconds   int      `json:"seconds"`
	Completed bool     `json:"completed"`
	Source    string   `json:"source"`
	Device    string   `json:"device"`
	TMDB      string   `json:"tmdb"`
}

// SyncState is where reading a person's history from Jellyfin stands.
type SyncState struct {
	ServerKey  string `json:"-"`
	Watermark  string `json:"watermark"`
	Backfilled bool   `json:"backfilled"`
	SyncedAt   string `json:"synced_at"`
	Error      string `json:"error"`
}

// WatchlistItem is something a person wants to watch.
type WatchlistItem struct {
	ID        int64  `json:"id"`
	MediaType string `json:"media_type"`
	ItemID    string `json:"item_id"`
	TMDBID    int    `json:"tmdb_id"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Poster    string `json:"poster"`
	Note      string `json:"note"`
	AddedAt   string `json:"added_at"`
}

// Request is a Jellyseerr request a person made.
type Request struct {
	ID         int64  `json:"id"`
	SeerrID    int    `json:"seerr_id"`
	MediaType  string `json:"media_type"`
	TMDBID     int    `json:"tmdb_id"`
	Title      string `json:"title"`
	Year       int    `json:"year"`
	Poster     string `json:"poster"`
	Seasons    string `json:"seasons"`
	Status     string `json:"status"`
	JellyfinID string `json:"jellyfin_id"`
	Notified   bool   `json:"notified"`
	ArrivedAt  string `json:"arrived_at"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// Settings are the box-wide title plus one person's preferences.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	DateFormat string `json:"date_format"`
	TimeFormat string `json:"time_format"`

	// Playback.
	Quality      string `json:"quality"`       // auto | 60 | 40 | 20 | 10 | 8 | 4 | 2 | 1 (Mbps cap)
	AudioLang    string `json:"audio_lang"`    // ISO 639-2 ("" = the file's default track)
	SubtitleLang string `json:"subtitle_lang"` // ISO 639-2, comma list in order of preference
	SubtitleMode string `json:"subtitle_mode"` // auto (foreign audio / forced) | always | forced | off
	SubtitleSize string `json:"subtitle_size"` // s | m | l | xl
	AutoplayNext bool   `json:"autoplay_next"` // next episode starts by itself
	SkipIntro    string `json:"skip_intro"`    // button | auto | off
	Completion   int    `json:"completion"`    // a title counts as watched from this share (%)

	// History and upcoming.
	HistoryAll    bool   `json:"history_all"`    // read plays from every Jellyfin app, not just this one
	UpcomingScope string `json:"upcoming_scope"` // mine (shows I watch) | all

	// Pushes: a requested title arrived; once a week a recap.
	NotifyURL       string `json:"notify_url"`
	NotifyFormat    string `json:"notify_format"` // apprise | ntfy | json
	NotifyAvailable bool   `json:"notify_available"`
	NotifyWeekly    bool   `json:"notify_weekly"`

	// "What should we watch?" (Pocket AI through Core, or an OpenAI-compatible API).
	AIProvider    string `json:"ai_provider"` // auto | pocket | openai
	AIPocketModel string `json:"ai_pocket_model"`
	OpenAIKey     string `json:"openai_api_key"`
	OpenAIModel   string `json:"openai_model"`
	OpenAIBaseURL string `json:"openai_base_url"`

	// The box's services (admins): requests, upcoming, leaving soon, subtitles.
	SeerrURL       string `json:"seerr_url"`
	SeerrPublicURL string `json:"seerr_public_url"`
	SeerrKey       string `json:"seerr_key"`
	SonarrURL      string `json:"sonarr_url"`
	SonarrKey      string `json:"sonarr_key"`
	RadarrURL      string `json:"radarr_url"`
	RadarrKey      string `json:"radarr_key"`
	MaintainerrURL string `json:"maintainerr_url"`
	SubtitlesURL   string `json:"subtitles_url"`
}
