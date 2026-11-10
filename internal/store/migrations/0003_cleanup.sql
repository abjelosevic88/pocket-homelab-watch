-- Clean-up rules: an admin says which films, shows or seasons may leave the
-- library (watched by everyone, not played for a year, …). What a rule finds
-- goes on "Leaving soon" for its grace days, where anyone can keep it; after
-- that it is removed through Radarr or Sonarr (or Jellyfin when neither has it).
CREATE TABLE cleanup_rules (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  name          TEXT NOT NULL,
  target        TEXT NOT NULL DEFAULT 'movie',   -- movie | series | season
  match         TEXT NOT NULL DEFAULT 'all',     -- all | any
  conditions    TEXT NOT NULL DEFAULT '[]',      -- JSON [{field, op, value}]
  protect       TEXT NOT NULL DEFAULT '["watchlist","in_progress","favorite"]',
  grace_days    INTEGER NOT NULL DEFAULT 14,
  action        TEXT NOT NULL DEFAULT 'delete',  -- delete (with files) | unmonitor (files go, Radarr/Sonarr keep it unmonitored)
  exclude_lists INTEGER NOT NULL DEFAULT 0,      -- add an import-list exclusion so lists don't bring it back
  clear_request INTEGER NOT NULL DEFAULT 1,      -- clear it in Jellyseerr so it can be asked for again
  active        INTEGER NOT NULL DEFAULT 1,
  last_run_at   TEXT NOT NULL DEFAULT '',
  last_matched  INTEGER NOT NULL DEFAULT 0,
  last_error    TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Leaving soon: one row per Jellyfin item (film, show or season) on its way out.
CREATE TABLE cleanup_items (
  item_id    TEXT PRIMARY KEY,
  rule_id    INTEGER NOT NULL DEFAULT 0,          -- 0 = put there by hand
  kind       TEXT NOT NULL DEFAULT 'movie',       -- movie | series | season
  title      TEXT NOT NULL DEFAULT '',
  series_id  TEXT NOT NULL DEFAULT '',
  season     INTEGER NOT NULL DEFAULT 0,
  tmdb       INTEGER NOT NULL DEFAULT 0,
  tvdb       INTEGER NOT NULL DEFAULT 0,
  size       INTEGER NOT NULL DEFAULT 0,          -- bytes on disk, when Radarr/Sonarr know
  reason     TEXT NOT NULL DEFAULT '',
  added_by   INTEGER NOT NULL DEFAULT 0,          -- the admin, for by-hand entries
  matched_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  remove_on  TEXT NOT NULL,                       -- local date
  notified   INTEGER NOT NULL DEFAULT 0,
  attempts   INTEGER NOT NULL DEFAULT 0,
  error      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX cleanup_items_rule ON cleanup_items(rule_id);

-- Titles someone asked to keep: no rule touches them.
CREATE TABLE cleanup_keeps (
  item_id    TEXT PRIMARY KEY,
  kind       TEXT NOT NULL DEFAULT 'movie',
  title      TEXT NOT NULL DEFAULT '',
  user_id    INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- What clean-up removed (or failed to).
CREATE TABLE cleanup_log (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  item_id   TEXT NOT NULL DEFAULT '',
  kind      TEXT NOT NULL DEFAULT '',
  title     TEXT NOT NULL DEFAULT '',
  rule      TEXT NOT NULL DEFAULT '',             -- the rule's name then ('' = by hand)
  action    TEXT NOT NULL DEFAULT '',
  via       TEXT NOT NULL DEFAULT '',             -- radarr | sonarr | jellyfin
  size      INTEGER NOT NULL DEFAULT 0,
  ok        INTEGER NOT NULL DEFAULT 1,
  detail    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX cleanup_log_at ON cleanup_log(at);
