CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'member',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup flag); everything else is personal.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL
);


-- How a person reaches Jellyfin: an access token from their own sign-in
-- (kind direct: password or Quick Connect, token sealed with
-- DATA_DIR/secret.key), an API key plus the user it acts for (kind key: the
-- owner's, from the environment), or the login Pocket Core holds for them
-- (kind core: nothing stored here). No row = Core if the box has one.
CREATE TABLE connections (
  user_id        INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  kind           TEXT NOT NULL DEFAULT 'direct',   -- direct | key | core | off
  server_url     TEXT NOT NULL DEFAULT '',         -- '' = JELLYFIN_URL
  username       TEXT NOT NULL DEFAULT '',
  jf_user_id     TEXT NOT NULL DEFAULT '',
  secret         TEXT NOT NULL DEFAULT '',         -- sealed token / API key
  device_id      TEXT NOT NULL DEFAULT '',         -- the device the token was issued to
  server_name    TEXT NOT NULL DEFAULT '',
  server_version TEXT NOT NULL DEFAULT '',
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- What each person watched, owned by Pocket Watch: one row per title per
-- day, from its own player and from every other Jellyfin app (read from the
-- person's Jellyfin state every SYNC_EVERY). Fields are copied so a row
-- survives the file leaving the library. The source of history, stats and
-- the Pocket Day entry.
CREATE TABLE watches (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id     TEXT NOT NULL,
  kind        TEXT NOT NULL DEFAULT 'movie',    -- movie | episode
  title       TEXT NOT NULL DEFAULT '',
  series_id   TEXT NOT NULL DEFAULT '',
  series      TEXT NOT NULL DEFAULT '',
  season      INTEGER NOT NULL DEFAULT 0,
  episode     INTEGER NOT NULL DEFAULT 0,
  year        INTEGER NOT NULL DEFAULT 0,
  genres      TEXT NOT NULL DEFAULT '',         -- '|'-joined with leading/trailing '|'
  runtime     INTEGER NOT NULL DEFAULT 0,       -- the title's length, seconds
  watched_at  TEXT NOT NULL,                    -- UTC, the last moment it played
  day         TEXT NOT NULL,                    -- local date of watched_at
  position    INTEGER NOT NULL DEFAULT 0,       -- seconds reached
  seconds     INTEGER NOT NULL DEFAULT 0,       -- seconds watched that day (estimate)
  completed   INTEGER NOT NULL DEFAULT 0,
  source      TEXT NOT NULL DEFAULT 'jellyfin', -- pocket | jellyfin | backfill | import
  device      TEXT NOT NULL DEFAULT '',
  tmdb        TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX watches_day ON watches(user_id, item_id, day);
CREATE INDEX watches_time ON watches(user_id, watched_at);
CREATE INDEX watches_series ON watches(user_id, series_id);

CREATE TABLE watch_sync (
  user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  server_key TEXT NOT NULL DEFAULT '',  -- server + Jellyfin user the history came from
  watermark  TEXT NOT NULL DEFAULT '',  -- newest LastPlayedDate already read
  backfilled INTEGER NOT NULL DEFAULT 0,
  synced_at  TEXT NOT NULL DEFAULT '',
  error      TEXT NOT NULL DEFAULT ''
);

-- Things a person wants to watch: in the library (item_id) or not yet
-- (tmdb_id, one click from a request).
CREATE TABLE watchlist (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  media_type TEXT NOT NULL DEFAULT 'movie',    -- movie | tv
  item_id    TEXT NOT NULL DEFAULT '',
  tmdb_id    INTEGER NOT NULL DEFAULT 0,
  title      TEXT NOT NULL DEFAULT '',
  year       INTEGER NOT NULL DEFAULT 0,
  poster     TEXT NOT NULL DEFAULT '',
  note       TEXT NOT NULL DEFAULT '',
  added_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX watchlist_user ON watchlist(user_id, added_at);

-- Requests a person made in Jellyseerr (here or there), followed so they
-- hear when a title arrives. Jellyseerr stays the source of truth.
CREATE TABLE requests (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  seerr_id    INTEGER NOT NULL,
  media_type  TEXT NOT NULL DEFAULT 'movie',
  tmdb_id     INTEGER NOT NULL DEFAULT 0,
  title       TEXT NOT NULL DEFAULT '',
  year        INTEGER NOT NULL DEFAULT 0,
  poster      TEXT NOT NULL DEFAULT '',
  seasons     TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'pending', -- pending | approved | declined | processing | partial | available | failed
  jellyfin_id TEXT NOT NULL DEFAULT '',
  notified    INTEGER NOT NULL DEFAULT 0,
  arrived_at  TEXT NOT NULL DEFAULT '',         -- when it was seen turning available
  created_at  TEXT NOT NULL DEFAULT '',
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (user_id, seerr_id)
);
