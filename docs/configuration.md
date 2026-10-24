# Configuration

Everything is read from environment variables; every value has a default, so a bare `docker run -v ./data:/data` works.
Integration values seed the owner's (first admin's) own settings on first start, once; after that the values saved under
**Settings** win, and clearing one there keeps it cleared.

## Server

| Variable | Default | What it does |
|---|---|---|
| `PORT` / `HOST` | `8080` / `0.0.0.0` | Listen address |
| `BASE_URL` | – | Public URL: notification links, the OIDC callback, what Pocket Core shows |
| `DATA_DIR` | `./data` (`/data` in the image) | SQLite database, `secret.key`, backups |
| `DB_PATH` | `$DATA_DIR/pocket-watch.db` | Override the database location |
| `TZ` | – | Time zone for "today", streaks and the weekly recap |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `text` | `debug`, `warn`, `error`; `json` |
| `TRUST_PROXY` | `true` | Honour `X-Forwarded-For` |
| `METRICS_ENABLED` | `true` | Prometheus `/metrics` (row counts only, never content) |
| `USER_AGENT` | `PocketWatch/1.0 (+repo URL)` | Sent with outgoing requests |

## Sign-in

| Variable | Default | What it does |
|---|---|---|
| `AUTH_MODE` | `local` | `local` (built-in accounts), `proxy` (trust `AUTH_PROXY_HEADER`), `none` (single user, LAN only) |
| `AUTH_PROXY_HEADER` / `AUTH_PROXY_EMAIL_HEADER` | `Remote-User` / `Remote-Email` | Headers for proxy mode |
| `SESSION_TTL` | `720h` | Cookie lifetime |
| `SECURE_COOKIES` | `false` | Set `true` behind HTTPS (on plain http the browser drops the cookie and login bounces) |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` / `ADMIN_NAME` | – | Create the first admin on an empty database (otherwise the UI asks) |
| `OIDC_ISSUER` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | – | Sign in with an OpenID Connect provider (PocketID, Authelia, Authentik, Keycloak…). The callback is `BASE_URL/api/v1/auth/oidc/callback`. With Pocket Core none of these are needed. |
| `OIDC_NAME` / `OIDC_AUTO_CREATE` | `single sign-on` / `true` | Button label; give an unknown verified email a member account |

## Jellyfin

Every person watches with their **own** Jellyfin account: they sign in once under Settings → Jellyfin with their
password or Quick Connect (only the token Jellyfin hands out is kept, sealed with `DATA_DIR/secret.key`), or Pocket
Core holds their login (service type `jellyfin`: a token, or an API key plus `USER_ID`).

| Variable | Default | What it does |
|---|---|---|
| `JELLYFIN_URL` | – | Jellyfin's address as this container reaches it (`http://jellyfin:8096`). Without it, each person types an address under Settings → Jellyfin. |
| `JELLYFIN_PUBLIC_URL` | – | What a browser opens for "Open in Jellyfin" |
| `JELLYFIN_API_KEY` + `JELLYFIN_USER` | – | The owner's account, copied once into their connection: an API key (Dashboard → API Keys) and the Jellyfin user (name or id) it acts as. Resume points and watched marks are written for that user. |
| `JELLYFIN_USER` + `JELLYFIN_PASSWORD` | – | Or the owner's user name and password (signed in once; only the token is kept) |
| `SYNC_EVERY` | `10m` | How often each person's history is read from Jellyfin (plays in other Jellyfin apps) |

## The box's services

These belong to the whole box and are also editable by admins under Settings → Services (the environment seeds
them once). Pocket Watch only ever acts as each person: requests go in as their own Jellyseerr user.

On a box with Pocket Core they come **from Core**: the address, the key (the box owner's own login under Core →
Settings → My logins) and the address people open, each switched on in Core → Links — nothing about them is kept in
Watch, and the variables below are not needed. *Automatic* (the default) is Core when the box has one, else Direct:
the variables below and Settings → Services, for a box without Core.

| Variable | Default | What it does |
|---|---|---|
| `JELLYSEERR_URL` / `JELLYSEERR_API_KEY` | – | Discover, requests, arrivals. A person's Jellyseerr user is the one linked to their Jellyfin account; one without gets it imported from Jellyfin on their first request. |
| `JELLYSEERR_PUBLIC_URL` | – | What a browser opens for "Open in Jellyseerr" |
| `SONARR_URL` / `SONARR_API_KEY` | – | Upcoming episodes (only the calendar is read) |
| `RADARR_URL` / `RADARR_API_KEY` | – | Upcoming films: cinema, digital and disc releases |
| `MAINTAINERR_URL` | – | "Leaving soon": titles a Maintainerr collection will remove, with the day |
| `SUBTITLES_URL` / `SUBTITLES_API_TOKEN` | – | [Pocket Subtitles](https://github.com/abjelosevic88/pocket-homelab-subtitles)' API and a token from it: the player saves a subtitle's timing into the .srt for everyone. Without a token, Watch goes through Pocket Core as each person (app link `subtitles:sync`, switched on in Core → Links). Settings → Services can choose. |
| `SUBTITLES_PUBLIC_URL` | – | Pocket Subtitles' address in a browser: admins get a link to the file from the player |

## Personal integrations

| Variable | Default | What it does |
|---|---|---|
| `NOTIFY_URL` / `NOTIFY_FORMAT` | – / guessed | The owner's Apprise `…/notify/<key>`, ntfy topic URL or JSON webhook: arrived requests, the weekly recap. Everyone sets their own under Settings → Notifications. |
| `OPENAI_API_KEY` / `OPENAI_MODEL` / `OPENAI_BASE_URL` | – / `gpt-4o-mini` / OpenAI | The owner's AI for "What should we watch?" (any OpenAI-compatible API). With Pocket Core, Pocket AI is used when its link is on. |

## Pocket Core

| Variable | Default | What it does |
|---|---|---|
| `CORE_URL` / `CORE_REGISTRATION_KEY` | – | [Pocket Core](https://github.com/abjelosevic88/pocket-homelab-core): register on start (the key is needed once; the app token is kept in `$DATA_DIR/core.token`), "Sign in with Pocket Core", accept the tokens Core gives paired phones, get each person's Jellyfin login from Core, use Pocket AI, publish events, offer `watching:read` and `requests:write` to other Pocket apps |
| `CORE_APP_URL` | `BASE_URL` | How Core reaches this app for health checks (e.g. `http://host.docker.internal:8201`) |

With `CORE_URL` empty the app behaves exactly as a standalone app: every Core-only choice stays visible, greyed out
with a note.

## Demo data

`DEMO_DATA=true` gives the owner three months of believable evenings (written to Jellyfin too, so Continue
watching and Next up have something to show) and a watchlist, once their history was first read. It is meant for
the throwaway Jellyfin from `scripts/demo-library/make.sh`, never for a real account.
