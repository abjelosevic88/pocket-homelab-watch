package server

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-watch/internal/jellyfin"
)

// Title is what the API says about a film, show, season or episode: the
// parts of Jellyfin's item a screen needs, images as Pocket Watch URLs (the
// browser never talks to Jellyfin) and the person's own state.
type Title struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"` // movie | series | season | episode | person | collection
	Name        string   `json:"name"`
	Original    string   `json:"original,omitempty"`
	Year        int      `json:"year,omitempty"`
	EndYear     int      `json:"end_year,omitempty"`
	Premiere    string   `json:"premiere,omitempty"`
	Added       string   `json:"added,omitempty"`
	Status      string   `json:"status,omitempty"`
	Rating      string   `json:"rating,omitempty"` // official (PG-13)
	Score       float64  `json:"score,omitempty"`  // community, out of 10
	Critics     float64  `json:"critics,omitempty"`
	Runtime     int      `json:"runtime,omitempty"` // seconds
	Overview    string   `json:"overview,omitempty"`
	Tagline     string   `json:"tagline,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	Studio      string   `json:"studio,omitempty"`
	SeriesID    string   `json:"series_id,omitempty"`
	SeriesName  string   `json:"series_name,omitempty"`
	SeasonID    string   `json:"season_id,omitempty"`
	Season      *int     `json:"season,omitempty"`
	Episode     *int     `json:"episode,omitempty"`
	EpisodeEnd  *int     `json:"episode_end,omitempty"`
	ChildCount  int      `json:"child_count,omitempty"`
	Missing     bool     `json:"missing,omitempty"` // a known episode without a file
	Poster      string   `json:"poster,omitempty"`
	Backdrop    string   `json:"backdrop,omitempty"`
	Thumb       string   `json:"thumb,omitempty"` // landscape (episode still, film thumb)
	Logo        string   `json:"logo,omitempty"`
	Aspect      float64  `json:"aspect,omitempty"`
	TMDB        string   `json:"tmdb,omitempty"`
	IMDB        string   `json:"imdb,omitempty"`
	TVDB        string   `json:"tvdb,omitempty"`
	Played      bool     `json:"played"`
	Favorite    bool     `json:"favorite"`
	Position    int      `json:"position,omitempty"` // seconds
	Progress    float64  `json:"progress,omitempty"` // 0..100
	Unplayed    *int     `json:"unplayed,omitempty"`
	LastPlayed  string   `json:"last_played,omitempty"`
	Video       string   `json:"video,omitempty"` // 4K · HDR10 · HEVC
	Audio       string   `json:"audio,omitempty"` // 5.1 · EAC3
	Trailer     string   `json:"trailer,omitempty"`
	People      []Cast   `json:"people,omitempty"`
	Leaving     string   `json:"leaving,omitempty"` // Maintainerr: the day it is removed
	PlayableNow bool     `json:"playable"`
}

// Cast is one person in the cast or crew.
type Cast struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Role  string `json:"role,omitempty"`
	Kind  string `json:"kind"` // Actor | Director | Writer …
	Photo string `json:"photo,omitempty"`
}

// imgURL is Pocket Watch's address for one Jellyfin image.
func imgURL(id, typ, tag string) string {
	if id == "" || tag == "" {
		return ""
	}
	return "/api/v1/img/" + url.PathEscape(id) + "/" + typ + "?tag=" + url.QueryEscape(tag)
}

func year(date string) int {
	var y int
	if len(date) >= 4 {
		fmt.Sscanf(date[:4], "%d", &y)
	}
	return y
}

func lowerType(t string) string {
	switch t {
	case "Movie":
		return "movie"
	case "Series":
		return "series"
	case "Season":
		return "season"
	case "Episode":
		return "episode"
	case "Person":
		return "person"
	case "BoxSet":
		return "collection"
	}
	return strings.ToLower(t)
}

// view turns a Jellyfin item into a Title (full: cast and media details too).
func view(it *jellyfin.Item, full bool) Title {
	t := Title{ID: it.ID, Type: lowerType(it.Type), Name: it.Name, Year: it.ProductionYear, Premiere: dateOnly(it.PremiereDate), Added: it.DateCreated,
		Status: it.Status, Rating: it.OfficialRating, Score: round1(it.CommunityRating), Critics: it.CriticRating, Runtime: int(it.RunTimeTicks / jellyfin.Ticks),
		Overview: it.Overview, Genres: it.Genres, SeriesID: it.SeriesID, SeriesName: it.SeriesName, SeasonID: it.SeasonID,
		ChildCount: it.ChildCount, Missing: it.LocationType == "Virtual", Aspect: it.PrimaryImageAspectRatio}
	if it.OriginalTitle != "" && it.OriginalTitle != it.Name {
		t.Original = it.OriginalTitle
	}
	if t.Year == 0 {
		t.Year = year(it.PremiereDate)
	}
	if it.Type == "Series" && it.EndDate != "" && it.Status == "Ended" {
		t.EndYear = year(it.EndDate)
	}
	if it.Type == "Series" && it.RecursiveItemCount > 0 {
		t.ChildCount = it.RecursiveItemCount
	}
	if len(it.Taglines) > 0 {
		t.Tagline = it.Taglines[0]
	}
	if len(it.Studios) > 0 {
		t.Studio = it.Studios[0].Name
	}
	switch it.Type {
	case "Episode":
		t.Season, t.Episode, t.EpisodeEnd = it.ParentIndexNumber, it.IndexNumber, it.IndexNumberEnd
	case "Season":
		t.Season = it.IndexNumber
	}
	if p := it.ProviderIDs; p != nil {
		t.TMDB, t.IMDB, t.TVDB = p["Tmdb"], p["Imdb"], p["Tvdb"]
	}
	// images: a film or show has its own; an episode borrows its show's poster and backdrop
	tags := it.ImageTags
	if tags == nil {
		tags = map[string]string{}
	}
	if it.Type == "Episode" {
		t.Poster = imgURL(it.SeriesID, "Primary", it.SeriesPrimaryImageTag)
		t.Thumb = imgURL(it.ID, "Primary", tags["Primary"])
	} else {
		t.Poster = imgURL(it.ID, "Primary", tags["Primary"])
		t.Thumb = imgURL(it.ID, "Thumb", tags["Thumb"])
	}
	if len(it.BackdropImageTags) > 0 {
		t.Backdrop = imgURL(it.ID, "Backdrop", it.BackdropImageTags[0])
	} else if len(it.ParentBackdropImageTags) > 0 {
		t.Backdrop = imgURL(it.ParentBackdropItemID, "Backdrop", it.ParentBackdropImageTags[0])
	}
	if t.Thumb == "" {
		if it.ParentThumbItemID != "" && it.Type != "Episode" {
			t.Thumb = imgURL(it.ParentThumbItemID, "Thumb", it.ParentThumbImageTag)
		}
		if t.Thumb == "" {
			t.Thumb = t.Backdrop
		}
	}
	if tags["Logo"] != "" {
		t.Logo = imgURL(it.ID, "Logo", tags["Logo"])
	} else if it.ParentLogoItemID != "" {
		t.Logo = imgURL(it.ParentLogoItemID, "Logo", it.ParentLogoImageTag)
	}
	if it.Type == "Season" && t.Poster == "" && it.SeriesID != "" {
		t.Poster = imgURL(it.SeriesID, "Primary", it.SeriesPrimaryImageTag)
	}
	if ud := it.UserData; ud != nil {
		t.Played, t.Favorite, t.LastPlayed = ud.Played, ud.IsFavorite, ud.LastPlayedDate
		t.Position = int(ud.PlaybackPositionTicks / jellyfin.Ticks)
		if ud.PlayedPercentage != nil {
			t.Progress = round1(*ud.PlayedPercentage)
		} else if t.Runtime > 0 && t.Position > 0 {
			t.Progress = round1(float64(t.Position) * 100 / float64(t.Runtime))
		}
		t.Unplayed = ud.UnplayedItemCount
	}
	t.PlayableNow = (it.Type == "Movie" || it.Type == "Episode") && !t.Missing
	if len(it.RemoteTrailers) > 0 {
		t.Trailer = it.RemoteTrailers[0].URL
	}
	if full {
		for _, p := range it.People {
			if len(t.People) >= 30 {
				break
			}
			t.People = append(t.People, Cast{ID: p.ID, Name: p.Name, Role: p.Role, Kind: p.Type, Photo: imgURL(p.ID, "Primary", p.PrimaryImageTag)})
		}
		if len(it.MediaSources) > 0 {
			t.Video, t.Audio = mediaSummary(it.MediaSources[0])
		}
	}
	return t
}

func views(items []jellyfin.Item) []Title {
	out := make([]Title, 0, len(items))
	for i := range items {
		out = append(out, view(&items[i], false))
	}
	return out
}

func dateOnly(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

// mediaSummary describes a file: "4K · Dolby Vision · HEVC" and "5.1 · EAC3".
func mediaSummary(ms jellyfin.MediaSource) (string, string) {
	var v, a []string
	for _, st := range ms.MediaStreams {
		switch {
		case st.Type == "Video" && len(v) == 0:
			switch {
			case st.Width >= 3200 || st.Height >= 2000:
				v = append(v, "4K")
			case st.Width >= 1900 || st.Height >= 1000:
				v = append(v, "1080p")
			case st.Width >= 1260 || st.Height >= 700:
				v = append(v, "720p")
			case st.Height > 0:
				v = append(v, "SD")
			}
			switch r := st.VideoRangeType; {
			case strings.HasPrefix(r, "DOVI"):
				v = append(v, "Dolby Vision")
			case r == "HDR10Plus":
				v = append(v, "HDR10+")
			case r == "HDR10" || r == "HLG":
				v = append(v, r)
			}
			if st.Codec != "" {
				v = append(v, strings.ToUpper(st.Codec))
			}
		case st.Type == "Audio" && len(a) == 0:
			switch st.Channels {
			case 0:
			case 1:
				a = append(a, "Mono")
			case 2:
				a = append(a, "Stereo")
			case 6:
				a = append(a, "5.1")
			case 8:
				a = append(a, "7.1")
			default:
				a = append(a, fmt.Sprintf("%d ch", st.Channels))
			}
			if st.Codec != "" {
				a = append(a, strings.ToUpper(st.Codec))
			}
		}
	}
	return strings.Join(v, " · "), strings.Join(a, " · ")
}
