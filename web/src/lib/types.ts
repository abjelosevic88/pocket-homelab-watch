export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

/** A film, show, season, episode or person in the library (the API's view of a Jellyfin item). */
export interface Title {
  id: string; type: 'movie' | 'series' | 'season' | 'episode' | 'person' | 'collection' | string; name: string; original?: string
  year?: number; end_year?: number; premiere?: string; added?: string; status?: string; rating?: string; score?: number; critics?: number
  runtime?: number; overview?: string; tagline?: string; genres?: string[]; studio?: string
  series_id?: string; series_name?: string; season_id?: string; season?: number; episode?: number; episode_end?: number; child_count?: number; missing?: boolean
  poster?: string; backdrop?: string; thumb?: string; logo?: string; aspect?: number; tmdb?: string; imdb?: string; tvdb?: string
  played: boolean; favorite: boolean; position?: number; progress?: number; unplayed?: number; last_played?: string
  video?: string; audio?: string; trailer?: string; people?: Cast[]; leaving?: string; playable: boolean
}
export interface Cast { id: string; name: string; role?: string; kind: string; photo?: string }

export interface SyncState { watermark: string; backfilled: boolean; synced_at: string; error: string }

export interface WatchlistItem { id: number; media_type: 'movie' | 'tv'; item_id: string; tmdb_id: number; title: string; year: number; poster: string; note: string; added_at: string; library?: Title; request?: string }
export interface Request {
  id: number; seerr_id: number; media_type: 'movie' | 'tv'; tmdb_id: number; title: string; year: number; poster: string; seasons: string
  status: 'pending' | 'approved' | 'declined' | 'processing' | 'partial' | 'available' | 'failed'; jellyfin_id: string; notified: boolean; arrived_at: string; created_at: string; updated_at: string
}

export interface Home {
  not_connected?: string
  continue?: Title[]; next_up?: Title[]; latest_movies?: Title[]; latest_shows?: Title[]; picks?: Title[]
  because?: { title: string; id: string; items: Title[] }; watchlist?: WatchlistItem[]; arrived?: Request[]; leaving?: Title[]
  today?: { titles: number; seconds: number }; sync?: SyncState
}

export interface Watch {
  id: number; item_id: string; kind: 'movie' | 'episode'; title: string; series_id: string; series: string; season: number; episode: number; year: number
  genres: string[]; runtime: number; watched_at: string; day: string; position: number; seconds: number; completed: boolean; source: string; device: string; tmdb: string
  poster: string; thumb: string
}
export interface CountRow { key: string; name: string; count: number; seconds: number; image?: string }
export interface Stats {
  range: string; from: string; to: string; days_active: number
  totals: { titles: number; movies: number; episodes: number; seconds: number; movie_seconds: number; episode_seconds: number; completed: number }
  series: { key: string; label: string; seconds: number }[]; top_shows: CountRow[]; top_genres: CountRow[]; weekday: number[]; hours: number[]
  streak: { longest: number; current: number }
}

export interface Found { media_type: 'movie' | 'tv'; tmdb_id: number; title: string; year?: number; date?: string; overview?: string; poster?: string; backdrop?: string; score?: number; status: '' | 'pending' | 'processing' | 'partial' | 'available'; jellyfin_id?: string }
export interface FoundDetail {
  title: Found; tagline?: string; runtime?: number; genres: string[]; seasons: { number: number; name: string; episodes: number; air_date?: string; status: string }[]
  cast: Cast[]; trailer?: string; request?: Request; seerr_url: string
}

export interface Upcoming {
  kind: 'episode' | 'movie'; at: string; date: string; title: string; series?: string; season?: number; episode?: number; release?: 'cinema' | 'digital' | 'physical'
  has_file: boolean; poster?: string; tmdb?: number; tvdb?: number; library_id?: string; mine: boolean
}

export interface StreamInfo { index: number; label: string; lang: string; codec: string; channels?: number; default?: boolean; forced?: boolean; external?: boolean; text: boolean; url?: string }
export interface PlayInfo {
  session: string; media_source: string; method: 'DirectPlay' | 'Transcode'; url: string; hls: boolean; start: number; runtime: number; title: Title
  audio: StreamInfo[]; audio_index: number; subtitles: StreamInfo[]; subtitle_index: number; burned: boolean
  chapters: { name: string; start: number }[]; segments: { type: string; start: number; end: number }[]
  trickplay?: { width: number; height: number; tile_w: number; tile_h: number; count: number; interval: number; url: string; query: string }
  next?: Title; prev?: Title; subtitles_tool?: string
}

export interface Connection {
  mode: 'core' | 'direct' | 'off'; connected: boolean; message?: string; via?: 'direct' | 'key' | 'core'; api_key?: boolean; account?: string; admin?: boolean
  server_name?: string; server_version?: string; public_url?: string; username?: string; custom_url?: string; server_url: boolean; quick_connect?: boolean
  core: boolean; core_configured: boolean; counts?: { MovieCount: number; SeriesCount: number; EpisodeCount: number }; sync: SyncState; syncing: boolean
}

export interface Settings {
  core_configured: boolean; core_registered?: boolean; title: string; date_format: string; time_format: string
  quality: string; audio_lang: string; subtitle_lang: string; subtitle_mode: 'auto' | 'always' | 'forced' | 'off'; subtitle_size: 's' | 'm' | 'l' | 'xl'
  autoplay_next: boolean; skip_intro: 'button' | 'auto' | 'off'; completion: number; history_all: boolean; upcoming_scope: 'mine' | 'all'
  notify_url: string; notify_format: string; notify_available: boolean; notify_weekly: boolean; notify_configured: boolean
  ai_provider: 'auto' | 'pocket' | 'openai'; ai_pocket_model: string; openai_api_key: string; openai_model: string; openai_base_url: string
  setup_complete: boolean; base_url: string; jellyfin_url: boolean; jellyfin_public_url: string; requests: boolean; upcoming: boolean; seerr_link: string
  seerr_url?: string; seerr_public_url?: string; seerr_key?: string; sonarr_url?: string; sonarr_key?: string; radarr_url?: string; radarr_key?: string; maintainerr_url?: string; subtitles_url?: string
}
