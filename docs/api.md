# API

JSON over HTTP at `/api/v1`. Authenticate with the session cookie (the web app), a personal token from
Settings → API & shortcuts (`Authorization: Bearer pwt_…`), a token Pocket Core gave a paired phone, or a Pocket Core
app-link token (`pcx_…`, limited to the scopes below). Everything answers for the person the token belongs to; other
people's ids answer 404. Times are UTC RFC 3339; days are the server's local `YYYY-MM-DD`.

A person without a working Jellyfin connection gets `409 {"code":"not_connected","error":"…"}` from anything that
needs Jellyfin (`GET /home` answers `{"not_connected":"…"}` instead).

Films, shows, seasons, episodes and people are **titles**: Jellyfin's item, trimmed to what a screen needs —
`id`, `type` (`movie` `series` `season` `episode` `person`), `name`, `year`, `runtime` (seconds), `overview`,
`genres`, `poster` / `backdrop` / `thumb` / `logo` (Pocket Watch image URLs), `series_id`, `season`, `episode`,
`tmdb` / `imdb` / `tvdb`, and the person's own `played`, `favorite`, `position`, `progress` (%), `unplayed`.

## Library

| | |
|---|---|
| `GET /home` | Continue watching, next up, recently added films and episodes, picks for today, because you watched, watchlist titles already here, arrived requests, leaving soon, today's time |
| `GET /browse?type=movie\|series&sort=&show=&genre=&decade=&q=&letter=&start=&limit=` | Films or shows. `sort` = `added` `title` `year` `score` `critics` `runtime` `played` `random`; `show` = `all` `unwatched` `watched` `favorites` `resumable` |
| `GET /genres?type=movie\|series` | Genre names |
| `GET /search?q=` | `{movies, series, episodes, people}` in the library |
| `GET /titles/{id}` | A title with what goes around it: `seasons` and `next` (shows), `episodes` (seasons, episodes), `similar`, `titles` (people), `watchlist_id` |
| `GET /titles/{id}/episodes?season=<seasonId>` | One season's episodes |
| `POST /titles/{id}/played {"played":true}` | Watched / not watched (a show or season: every episode), in Jellyfin |
| `POST /titles/{id}/favorite {"favorite":true}` | A favourite in Jellyfin |
| `GET /leaving` | Titles Maintainerr will remove, with `leaving` (the day) |
| `GET /img/{id}/{Primary\|Backdrop\|Thumb\|Logo}?tag=&w=` | An image, resized (cached for a month when `tag` is given) |

## Playing

| | |
|---|---|
| `POST /play/{id}` | `{"start": -1, "caps": {…}, "audio": n, "subtitle": n, "quality": "auto\|20\|8\|…", "transcode": false, "replace": "<session>"}` → how to play it: `url` (under `/api/v1/media/`), `hls`, `method` (`DirectPlay` / `Transcode`), `start`, `runtime`, `audio` and `subtitles` (text ones with a WebVTT `url`), `chapters`, `segments` (intro, outro, recap…), `trickplay`, `next` / `prev` episode. `start` -1 resumes; `caps` says what the browser decodes; `replace` hands an old session over (switching tracks) |
| `POST /play/{id}/report` | `{"session","event":"start\|progress\|pause\|unpause\|stop","position":seconds}` — passed on to Jellyfin as the person and written into history |
| `GET /media/Videos/{id}/…` | The stream, HLS playlists and segments, subtitles and trickplay tiles, passed to Jellyfin with the person's own token (byte ranges included). Only `Videos/<id>/…` paths. |

## History and stats

| | |
|---|---|
| `GET /history?from=&to=&kind=movie\|episode&series=&q=&limit=&offset=` | `{items, total}`, newest first — one row per title per day, with `seconds` watched, `position`, `completed`, `source` (`pocket`, `jellyfin`, `backfill`, `import`) |
| `DELETE /history/{id}` | Forget one row (Jellyfin keeps its own state) |
| `GET /day/2026-10-24` | One day: `items`, `seconds`, `movies`, `episodes`, `summary` ("Sintel and 2 episodes of Bonanza") |
| `GET /stats?range=7d\|30d\|90d\|365d\|all\|2026` | Totals, time per day / week / month, top shows, genres, weekdays, hours, streaks |
| `GET /dashboard` | Today and this week in seconds, the last title, what is playing now, open requests |
| `GET /sync` · `POST /sync` · `POST /sync?full=1` | Reading history from Jellyfin: state; now; everything again |

## Watchlist

| | |
|---|---|
| `GET /watchlist` | Entries with the library `title` when it's here and the `request` status when it was asked for |
| `POST /watchlist` | `{"item_id":"…"}` for a library title, or `{"media_type":"movie\|tv","tmdb_id":…,"title":"…","year":…,"poster":"https://image.tmdb.org/…"}` |
| `DELETE /watchlist/{id}` | Take it off |

## Discover and requests (Jellyseerr)

| | |
|---|---|
| `GET /discover?section=trending\|movies\|tv\|upcoming-movies\|upcoming-tv&page=` · `GET /discover?q=…` | TMDB titles with `status` in the library (`pending` `processing` `partial` `available`) and `jellyfin_id` |
| `GET /discover/{movie\|tv}/{tmdbId}` | Details, seasons with their status, cast, trailer, your request |
| `GET /requests` | Your requests (made here or in Jellyseerr), each with `status`: `pending` `approved` `declined` `processing` `partial` `available` `failed` |
| `POST /requests {"media_type","tmdb_id","seasons":[1,2]}` | Request as your own Jellyseerr user (`seasons` empty = all) |
| `DELETE /requests/{seerrId}` | Withdraw a request still waiting for approval |
| `GET /upcoming?days=21&scope=mine\|all` | Episodes and films on their way (Sonarr and Radarr calendars) |

## What should we watch?

`POST /tonight {"prompt":"something funny under two hours","kind":"any|movie|series","max_minutes":120}` →
`{"intro","provider":"pocket|openai|","picks":[{"title":…,"why":"…"}]}`. The AI sees only unwatched titles' names,
years, genres, lengths and ratings.

## Jellyfin apps (`/jellyfin/*`)

Pocket Watch answers the Jellyfin API under `/jellyfin` for the Pocket Homelab app: use `https://watch.example.com/jellyfin`
as the Jellyfin address and sign in with a personal token (`pwt_…`, Settings → API & shortcuts) or the token Pocket Core
gave a paired phone — in `Authorization: MediaBrowser Token="…"`, `X-Emby-Token` or `api_key`. Each call goes to Jellyfin
with the person's own Jellyfin login (Settings → Jellyfin), which never leaves the box: where Jellyfin writes it into an
answer (HLS playlists, transcoding URLs) it is replaced by the token the app sent. `GET /Users` and `/Users/Me` return only
the person. Passed on: reading the library, images, streams and downloads, trickplay and media segments, playback reports,
watched and favourite marks, and the library scan; anything that changes users, policies or the server is refused (403).

## Pocket Core app links

Watch offers two scopes to other Pocket apps (switched on per person in Pocket Core):

| Scope | Routes |
|---|---|
| `watching:read` | `GET /dashboard`, `/history`, `/stats`, `/upcoming`, `/day/{date}`, images |
| `requests:write` | `GET /search`, `/watchlist`, `/requests`, `/discover…`; `POST /watchlist`, `POST /requests` |

Events published on the Core bus (each with `user_email`): `watch.watched` (a film or episode finished, here or in
another Jellyfin app — `elsewhere: true`), `watch.requested`, `watch.request_available`, `watch.watchlist_added`.

## Homepage widget

```yaml
- Pocket Watch:
    href: https://watch.example.com
    widget:
      type: customapi
      url: https://watch.example.com/api/v1/dashboard
      headers: { Authorization: Bearer pwt_… }
      mappings:
        - { field: week_seconds, label: This week, format: duration }
        - { field: week_titles, label: Titles }
        - { field: open_requests, label: Requests }
```

## Everything else

Sign-in (`/auth/*`), people (`/users`), personal tokens (`/tokens`), settings (`/settings`), `GET /backup` (admins),
`GET /export.json` and `POST /import` (your own history and watchlist), `/healthz`, `/readyz`, `/metrics`.
