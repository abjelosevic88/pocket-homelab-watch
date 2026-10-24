// Package jellyfin is a small client for the Jellyfin API (10.9+), always
// acting as one person: their access token (from a password or Quick Connect
// sign-in) or an API key plus the user id it acts for.
package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client reaches one Jellyfin server as one person.
type Client struct {
	Base     string
	Token    string // access token or API key ("" before signing in)
	UserID   string // the Jellyfin user every user-scoped call is for
	DeviceID string
	Device   string // shown under Dashboard → Devices
	Version  string
	HTTP     *http.Client
}

// ClientName is how Pocket Watch shows up in Jellyfin.
const ClientName = "Pocket Watch"

// New creates a client.
func New(base, token, userID, deviceID string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), Token: token, UserID: userID, DeviceID: deviceID, Device: ClientName, Version: "1",
		HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Authorization is the MediaBrowser header Jellyfin expects.
func (c *Client) Authorization() string {
	q := func(s string) string { return strings.ReplaceAll(s, `"`, "") }
	h := fmt.Sprintf(`MediaBrowser Client="%s", Device="%s", DeviceId="%s", Version="%s"`, ClientName, q(c.Device), q(c.DeviceID), q(c.Version))
	if c.Token != "" {
		h += fmt.Sprintf(`, Token="%s"`, q(c.Token))
	}
	return h
}

// Error is a non-2xx answer.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "Jellyfin did not accept the sign-in (it may have been signed out under Dashboard → Devices)"
	case http.StatusForbidden:
		return "Jellyfin refused: this account may not do that"
	case http.StatusNotFound:
		return "not found in Jellyfin"
	}
	if e.Msg != "" {
		return fmt.Sprintf("Jellyfin answered HTTP %d: %s", e.Status, e.Msg)
	}
	return fmt.Sprintf("Jellyfin answered HTTP %d", e.Status)
}

// IsAuth reports a rejected token.
func IsAuth(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusUnauthorized
}

// IsNotFound reports a 404 (or an id Jellyfin can't parse).
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Status == http.StatusNotFound || e.Status == http.StatusBadRequest)
}

func (c *Client) req(ctx context.Context, method, path string, q url.Values, body any) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	u := c.Base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	r, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", c.Authorization())
	r.Header.Set("Accept", "application/json")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	return r, nil
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	r, err := c.req(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	res, err := c.HTTP.Do(r)
	if err != nil {
		return fmt.Errorf("Jellyfin is not reachable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		msg := strings.TrimSpace(string(b))
		if strings.HasPrefix(msg, "<") || strings.HasPrefix(msg, "{") {
			msg = ""
		}
		return &Error{Status: res.StatusCode, Msg: msg}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// withUser adds userId to a query.
func (c *Client) withUser(q url.Values) url.Values {
	if q == nil {
		q = url.Values{}
	}
	if c.UserID != "" && q.Get("userId") == "" {
		q.Set("userId", c.UserID)
	}
	return q
}

// ---- server and sign-in ----

// PublicInfo is /System/Info/Public.
type PublicInfo struct {
	ServerName string `json:"ServerName"`
	Version    string `json:"Version"`
	ID         string `json:"Id"`
}

// Public reads the server's name and version (no sign-in needed).
func (c *Client) Public(ctx context.Context) (*PublicInfo, error) {
	var p PublicInfo
	return &p, c.do(ctx, http.MethodGet, "/System/Info/Public", nil, nil, &p)
}

// User is a Jellyfin account.
type User struct {
	ID              string `json:"Id"`
	Name            string `json:"Name"`
	PrimaryImageTag string `json:"PrimaryImageTag"`
	Policy          struct {
		IsAdministrator bool `json:"IsAdministrator"`
		IsDisabled      bool `json:"IsDisabled"`
	} `json:"Policy"`
	Configuration struct {
		AudioLanguagePreference    string `json:"AudioLanguagePreference"`
		SubtitleLanguagePreference string `json:"SubtitleLanguagePreference"`
		SubtitleMode               string `json:"SubtitleMode"`
		PlayDefaultAudioTrack      bool   `json:"PlayDefaultAudioTrack"`
	} `json:"Configuration"`
}

// AuthResult is a sign-in's answer.
type AuthResult struct {
	User        User   `json:"User"`
	AccessToken string `json:"AccessToken"`
}

// AuthenticateByName signs in with a password and returns the token.
func (c *Client) AuthenticateByName(ctx context.Context, user, password string) (*AuthResult, error) {
	var out AuthResult
	if err := c.do(ctx, http.MethodPost, "/Users/AuthenticateByName", nil, map[string]string{"Username": user, "Pw": password}, &out); err != nil {
		var e *Error
		if errors.As(err, &e) && e.Status == http.StatusUnauthorized {
			return nil, errors.New("Jellyfin did not accept that username and password")
		}
		return nil, err
	}
	return &out, nil
}

// QuickConnect is a pending Quick Connect sign-in.
type QuickConnect struct {
	Secret        string `json:"Secret"`
	Code          string `json:"Code"`
	Authenticated bool   `json:"Authenticated"`
}

// QuickConnectEnabled asks whether the server allows Quick Connect.
func (c *Client) QuickConnectEnabled(ctx context.Context) bool {
	var on bool
	return c.do(ctx, http.MethodGet, "/QuickConnect/Enabled", nil, nil, &on) == nil && on
}

// QuickConnectInitiate starts a Quick Connect sign-in: the person types the
// code into a Jellyfin app they are signed in to.
func (c *Client) QuickConnectInitiate(ctx context.Context) (*QuickConnect, error) {
	var out QuickConnect
	return &out, c.do(ctx, http.MethodPost, "/QuickConnect/Initiate", nil, nil, &out)
}

// QuickConnectState reports whether the code was approved.
func (c *Client) QuickConnectState(ctx context.Context, secret string) (*QuickConnect, error) {
	var out QuickConnect
	return &out, c.do(ctx, http.MethodGet, "/QuickConnect/Connect", url.Values{"secret": {secret}}, nil, &out)
}

// AuthenticateWithQuickConnect turns an approved code into a token.
func (c *Client) AuthenticateWithQuickConnect(ctx context.Context, secret string) (*AuthResult, error) {
	var out AuthResult
	return &out, c.do(ctx, http.MethodPost, "/Users/AuthenticateWithQuickConnect", nil, map[string]string{"Secret": secret}, &out)
}

// Logout ends the session the token belongs to.
func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/Sessions/Logout", nil, nil, nil)
}

// Me is the account the client acts for.
func (c *Client) Me(ctx context.Context) (*User, error) {
	var u User
	if c.UserID != "" {
		return &u, c.do(ctx, http.MethodGet, "/Users/"+url.PathEscape(c.UserID), nil, nil, &u)
	}
	return &u, c.do(ctx, http.MethodGet, "/Users/Me", nil, nil, &u)
}

// Users lists the accounts (an API key or an administrator's token only).
func (c *Client) Users(ctx context.Context) ([]User, error) {
	var out []User
	return out, c.do(ctx, http.MethodGet, "/Users", nil, nil, &out)
}

// ---- items ----

// UserData is a person's state for one item.
type UserData struct {
	PlaybackPositionTicks int64    `json:"PlaybackPositionTicks"`
	PlayCount             int      `json:"PlayCount"`
	IsFavorite            bool     `json:"IsFavorite"`
	Played                bool     `json:"Played"`
	LastPlayedDate        string   `json:"LastPlayedDate"`
	PlayedPercentage      *float64 `json:"PlayedPercentage"`
	UnplayedItemCount     *int     `json:"UnplayedItemCount"`
}

// MediaStream is one video, audio or subtitle stream.
type MediaStream struct {
	Index                int     `json:"Index"`
	Type                 string  `json:"Type"` // Video | Audio | Subtitle | EmbeddedImage
	Codec                string  `json:"Codec"`
	Language             string  `json:"Language"`
	Title                string  `json:"Title"`
	DisplayTitle         string  `json:"DisplayTitle"`
	IsDefault            bool    `json:"IsDefault"`
	IsForced             bool    `json:"IsForced"`
	IsExternal           bool    `json:"IsExternal"`
	IsTextSubtitleStream bool    `json:"IsTextSubtitleStream"`
	IsHearingImpaired    bool    `json:"IsHearingImpaired"`
	Width                int     `json:"Width"`
	Height               int     `json:"Height"`
	Channels             int     `json:"Channels"`
	ChannelLayout        string  `json:"ChannelLayout"`
	BitRate              int64   `json:"BitRate"`
	VideoRange           string  `json:"VideoRange"`
	VideoRangeType       string  `json:"VideoRangeType"`
	RealFrameRate        float64 `json:"RealFrameRate"`
	DeliveryMethod       string  `json:"DeliveryMethod"`
	DeliveryURL          string  `json:"DeliveryUrl"`
}

// MediaSource is one playable version of an item.
type MediaSource struct {
	ID                         string        `json:"Id"`
	Name                       string        `json:"Name"`
	Container                  string        `json:"Container"`
	Size                       int64         `json:"Size"`
	Bitrate                    int64         `json:"Bitrate"`
	RunTimeTicks               int64         `json:"RunTimeTicks"`
	ETag                       string        `json:"ETag"`
	MediaStreams               []MediaStream `json:"MediaStreams"`
	SupportsDirectPlay         bool          `json:"SupportsDirectPlay"`
	SupportsDirectStream       bool          `json:"SupportsDirectStream"`
	SupportsTranscoding        bool          `json:"SupportsTranscoding"`
	TranscodingURL             string        `json:"TranscodingUrl"`
	TranscodingSubProtocol     string        `json:"TranscodingSubProtocol"`
	TranscodingContainer       string        `json:"TranscodingContainer"`
	DefaultAudioStreamIndex    *int          `json:"DefaultAudioStreamIndex"`
	DefaultSubtitleStreamIndex *int          `json:"DefaultSubtitleStreamIndex"`
}

// NameID is a studio or a genre item.
type NameID struct {
	Name string `json:"Name"`
	ID   string `json:"Id"`
}

// Person is cast or crew.
type Person struct {
	Name            string `json:"Name"`
	ID              string `json:"Id"`
	Role            string `json:"Role"`
	Type            string `json:"Type"`
	PrimaryImageTag string `json:"PrimaryImageTag"`
}

// Chapter is a chapter mark.
type Chapter struct {
	StartPositionTicks int64  `json:"StartPositionTicks"`
	Name               string `json:"Name"`
}

// Trickplay describes one width of seek-preview tiles.
type Trickplay struct {
	Width          int `json:"Width"`
	Height         int `json:"Height"`
	TileWidth      int `json:"TileWidth"`
	TileHeight     int `json:"TileHeight"`
	ThumbnailCount int `json:"ThumbnailCount"`
	Interval       int `json:"Interval"`
}

// Item is the part of BaseItemDto Pocket Watch uses.
type Item struct {
	ID                      string                          `json:"Id"`
	Name                    string                          `json:"Name"`
	OriginalTitle           string                          `json:"OriginalTitle"`
	SortName                string                          `json:"SortName"`
	Type                    string                          `json:"Type"` // Movie | Series | Season | Episode | BoxSet | Person | CollectionFolder …
	IsFolder                bool                            `json:"IsFolder"`
	CollectionType          string                          `json:"CollectionType"`
	LocationType            string                          `json:"LocationType"` // Virtual = missing episode
	ProductionYear          int                             `json:"ProductionYear"`
	PremiereDate            string                          `json:"PremiereDate"`
	EndDate                 string                          `json:"EndDate"`
	DateCreated             string                          `json:"DateCreated"`
	Status                  string                          `json:"Status"` // Continuing | Ended
	OfficialRating          string                          `json:"OfficialRating"`
	CommunityRating         float64                         `json:"CommunityRating"`
	CriticRating            float64                         `json:"CriticRating"`
	RunTimeTicks            int64                           `json:"RunTimeTicks"`
	Overview                string                          `json:"Overview"`
	Taglines                []string                        `json:"Taglines"`
	Genres                  []string                        `json:"Genres"`
	Studios                 []NameID                        `json:"Studios"`
	People                  []Person                        `json:"People"`
	ProviderIDs             map[string]string               `json:"ProviderIds"`
	SeriesID                string                          `json:"SeriesId"`
	SeriesName              string                          `json:"SeriesName"`
	SeasonID                string                          `json:"SeasonId"`
	SeasonName              string                          `json:"SeasonName"`
	IndexNumber             *int                            `json:"IndexNumber"`
	IndexNumberEnd          *int                            `json:"IndexNumberEnd"`
	ParentIndexNumber       *int                            `json:"ParentIndexNumber"`
	ChildCount              int                             `json:"ChildCount"`
	RecursiveItemCount      int                             `json:"RecursiveItemCount"`
	ImageTags               map[string]string               `json:"ImageTags"`
	BackdropImageTags       []string                        `json:"BackdropImageTags"`
	ParentBackdropItemID    string                          `json:"ParentBackdropItemId"`
	ParentBackdropImageTags []string                        `json:"ParentBackdropImageTags"`
	ParentThumbItemID       string                          `json:"ParentThumbItemId"`
	ParentThumbImageTag     string                          `json:"ParentThumbImageTag"`
	ParentLogoItemID        string                          `json:"ParentLogoItemId"`
	ParentLogoImageTag      string                          `json:"ParentLogoImageTag"`
	SeriesPrimaryImageTag   string                          `json:"SeriesPrimaryImageTag"`
	PrimaryImageAspectRatio float64                         `json:"PrimaryImageAspectRatio"`
	UserData                *UserData                       `json:"UserData"`
	MediaSources            []MediaSource                   `json:"MediaSources"`
	Chapters                []Chapter                       `json:"Chapters"`
	RemoteTrailers          []struct{ URL, Name string }    `json:"RemoteTrailers"`
	LocalTrailerCount       int                             `json:"LocalTrailerCount"`
	Trickplay               map[string]map[string]Trickplay `json:"Trickplay"`
	AirDays                 []string                        `json:"AirDays"`
	AirTime                 string                          `json:"AirTime"`
}

// Ticks per second.
const Ticks = 10_000_000

// Items is a page of items.
type Items struct {
	Items            []Item `json:"Items"`
	TotalRecordCount int    `json:"TotalRecordCount"`
	StartIndex       int    `json:"StartIndex"`
}

// ListFields is what list views ask for.
const ListFields = "PrimaryImageAspectRatio,Genres,ProductionYear,PremiereDate,DateCreated,OfficialRating,CommunityRating,Overview,ProviderIds,ParentId,ChildCount,RecursiveItemCount,MediaSourceCount"

// Items queries the person's library.
func (c *Client) Items(ctx context.Context, q url.Values) (*Items, error) {
	var out Items
	q = c.withUser(q)
	if q.Get("Fields") == "" {
		q.Set("Fields", ListFields)
	}
	if q.Get("EnableImageTypes") == "" {
		q.Set("EnableImageTypes", "Primary,Backdrop,Thumb,Logo")
		q.Set("ImageTypeLimit", "1")
	}
	return &out, c.do(ctx, http.MethodGet, "/Items", q, nil, &out)
}

// Item reads one item with everything about it.
func (c *Client) Item(ctx context.Context, id string) (*Item, error) {
	var it Item
	return &it, c.do(ctx, http.MethodGet, "/Items/"+url.PathEscape(id), c.withUser(nil), nil, &it)
}

// Resume lists what the person stopped in the middle of.
func (c *Client) Resume(ctx context.Context, limit int) (*Items, error) {
	var out Items
	q := c.withUser(url.Values{"Limit": {fmt.Sprint(limit)}, "MediaTypes": {"Video"}, "Fields": {ListFields}, "EnableImageTypes": {"Primary,Backdrop,Thumb,Logo"}, "ImageTypeLimit": {"1"}, "EnableTotalRecordCount": {"false"}})
	return &out, c.do(ctx, http.MethodGet, "/UserItems/Resume", q, nil, &out)
}

// NextUp lists the next episode of each show the person is watching (or of one show).
func (c *Client) NextUp(ctx context.Context, seriesID string, limit int, rewatching bool) (*Items, error) {
	var out Items
	q := c.withUser(url.Values{"Limit": {fmt.Sprint(limit)}, "Fields": {ListFields}, "EnableImageTypes": {"Primary,Backdrop,Thumb,Logo"}, "ImageTypeLimit": {"1"},
		"EnableResumable": {"false"}, "EnableRewatching": {fmt.Sprint(rewatching)}, "EnableTotalRecordCount": {"false"}})
	if seriesID != "" {
		q.Set("SeriesId", seriesID)
	}
	return &out, c.do(ctx, http.MethodGet, "/Shows/NextUp", q, nil, &out)
}

// Latest lists what was added most recently (episodes grouped by show).
func (c *Client) Latest(ctx context.Context, types string, limit int) ([]Item, error) {
	var out []Item
	q := c.withUser(url.Values{"Limit": {fmt.Sprint(limit)}, "IncludeItemTypes": {types}, "Fields": {ListFields}, "EnableImageTypes": {"Primary,Backdrop,Thumb,Logo"}, "ImageTypeLimit": {"1"}, "GroupItems": {"true"}})
	return out, c.do(ctx, http.MethodGet, "/Items/Latest", q, nil, &out)
}

// Seasons lists a show's seasons.
func (c *Client) Seasons(ctx context.Context, seriesID string) (*Items, error) {
	var out Items
	q := c.withUser(url.Values{"Fields": {"ChildCount,RecursiveItemCount,PremiereDate,Overview"}, "EnableImageTypes": {"Primary"}})
	return &out, c.do(ctx, http.MethodGet, "/Shows/"+url.PathEscape(seriesID)+"/Seasons", q, nil, &out)
}

// Episodes lists a season's episodes (or the whole show's).
func (c *Client) Episodes(ctx context.Context, seriesID, seasonID string) (*Items, error) {
	var out Items
	q := c.withUser(url.Values{"Fields": {"Overview,PremiereDate,MediaSourceCount"}, "EnableImageTypes": {"Primary,Thumb"}, "ImageTypeLimit": {"1"}})
	if seasonID != "" {
		q.Set("SeasonId", seasonID)
	}
	return &out, c.do(ctx, http.MethodGet, "/Shows/"+url.PathEscape(seriesID)+"/Episodes", q, nil, &out)
}

// Similar lists items like this one.
func (c *Client) Similar(ctx context.Context, id string, limit int) (*Items, error) {
	var out Items
	q := c.withUser(url.Values{"Limit": {fmt.Sprint(limit)}, "Fields": {ListFields}})
	return &out, c.do(ctx, http.MethodGet, "/Items/"+url.PathEscape(id)+"/Similar", q, nil, &out)
}

// Genres lists genres (of one kind of item).
func (c *Client) Genres(ctx context.Context, types string) (*Items, error) {
	var out Items
	q := c.withUser(url.Values{"SortBy": {"SortName"}, "Recursive": {"true"}})
	if types != "" {
		q.Set("IncludeItemTypes", types)
	}
	return &out, c.do(ctx, http.MethodGet, "/Genres", q, nil, &out)
}

// SetPlayed marks an item (a show or season: every episode) watched or not.
func (c *Client) SetPlayed(ctx context.Context, id string, played bool) error {
	m := http.MethodPost
	if !played {
		m = http.MethodDelete
	}
	return c.do(ctx, m, "/UserPlayedItems/"+url.PathEscape(id), c.withUser(nil), nil, nil)
}

// SetFavorite stars an item or takes the star away.
func (c *Client) SetFavorite(ctx context.Context, id string, fav bool) error {
	m := http.MethodPost
	if !fav {
		m = http.MethodDelete
	}
	return c.do(ctx, m, "/UserFavoriteItems/"+url.PathEscape(id), c.withUser(nil), nil, nil)
}

// SaveUserData writes a person's state for an item (resume point, played).
func (c *Client) SaveUserData(ctx context.Context, id string, data map[string]any) error {
	return c.do(ctx, http.MethodPost, "/UserItems/"+url.PathEscape(id)+"/UserData", c.withUser(nil), data, nil)
}

// Counts is /Items/Counts.
type Counts struct {
	MovieCount   int `json:"MovieCount"`
	SeriesCount  int `json:"SeriesCount"`
	EpisodeCount int `json:"EpisodeCount"`
}

// Counts reads how many films, shows and episodes the person can see.
func (c *Client) Counts(ctx context.Context) (*Counts, error) {
	var out Counts
	return &out, c.do(ctx, http.MethodGet, "/Items/Counts", c.withUser(nil), nil, &out)
}

// ---- playback ----

// PlaybackInfo is the server's answer about how to play an item.
type PlaybackInfo struct {
	MediaSources  []MediaSource `json:"MediaSources"`
	PlaySessionID string        `json:"PlaySessionId"`
	ErrorCode     string        `json:"ErrorCode"`
}

// PlaybackInfo asks how an item can be played on a device with this profile.
func (c *Client) PlaybackInfo(ctx context.Context, id string, body map[string]any) (*PlaybackInfo, error) {
	var out PlaybackInfo
	body["UserId"] = c.UserID
	if err := c.do(ctx, http.MethodPost, "/Items/"+url.PathEscape(id)+"/PlaybackInfo", c.withUser(nil), body, &out); err != nil {
		return nil, err
	}
	if out.ErrorCode != "" {
		return nil, fmt.Errorf("Jellyfin can't play this here: %s", out.ErrorCode)
	}
	return &out, nil
}

// Report tells Jellyfin playback started, progressed or stopped
// (kind "", "Progress" or "Stopped"), so resume points and "now playing"
// stay right in every other Jellyfin app.
func (c *Client) Report(ctx context.Context, kind string, body map[string]any) error {
	p := "/Sessions/Playing"
	if kind != "" {
		p += "/" + kind
	}
	return c.do(ctx, http.MethodPost, p, nil, body, nil)
}

// StopEncoding ends the transcode of a play session.
func (c *Client) StopEncoding(ctx context.Context, playSessionID string) error {
	return c.do(ctx, http.MethodDelete, "/Videos/ActiveEncodings", url.Values{"deviceId": {c.DeviceID}, "playSessionId": {playSessionID}}, nil, nil)
}

// Segment is an intro, recap, credits… mark (Jellyfin 10.10+, from a plugin such as Intro Skipper).
type Segment struct {
	Type       string `json:"Type"` // Intro | Outro | Recap | Preview | Commercial | Unknown
	StartTicks int64  `json:"StartTicks"`
	EndTicks   int64  `json:"EndTicks"`
}

// Segments reads an item's media segments (none on older servers).
func (c *Client) Segments(ctx context.Context, id string) ([]Segment, error) {
	var out struct {
		Items []Segment `json:"Items"`
	}
	err := c.do(ctx, http.MethodGet, "/MediaSegments/"+url.PathEscape(id), nil, nil, &out)
	return out.Items, err
}

// Raw sends a request as the person and returns the answer unread (streams,
// HLS playlists and segments, subtitles, images).
func (c *Client) Raw(ctx context.Context, method, path, rawQuery string, hdr http.Header) (*http.Response, error) {
	u := c.Base + path
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	r, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range hdr {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	r.Header.Set("Authorization", c.Authorization())
	return streamClient.Do(r)
}

// streamClient has no overall timeout: a film streams for hours.
var streamClient = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConnsPerHost: 32, ResponseHeaderTimeout: 60 * time.Second, IdleConnTimeout: 90 * time.Second}}
