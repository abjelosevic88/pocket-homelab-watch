-- How much earlier or later a person wants one subtitle track of one title,
-- in milliseconds (positive = later). Applied by the player; saving it into
-- the file through Pocket Subtitles resets it to 0.
CREATE TABLE subtitle_offsets (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id    TEXT NOT NULL,
  stream     INTEGER NOT NULL,
  ms         INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (user_id, item_id, stream)
);
