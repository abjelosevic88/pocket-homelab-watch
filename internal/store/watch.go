package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ---- connections ----

// GetConnection returns how a person reaches Jellyfin.
func (s *Store) GetConnection(ctx context.Context, uid int64) (*Connection, error) {
	var c Connection
	err := s.DB.QueryRowContext(ctx, `SELECT user_id, kind, server_url, username, jf_user_id, secret, device_id, server_name, server_version, updated_at FROM connections WHERE user_id = ?`, uid).
		Scan(&c.UserID, &c.Kind, &c.ServerURL, &c.Username, &c.JFUserID, &c.Secret, &c.DeviceID, &c.ServerName, &c.ServerVersion, &c.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// SaveConnection stores how a person reaches Jellyfin.
func (s *Store) SaveConnection(ctx context.Context, c *Connection) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO connections (user_id, kind, server_url, username, jf_user_id, secret, device_id, server_name, server_version, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET kind = excluded.kind, server_url = excluded.server_url, username = excluded.username, jf_user_id = excluded.jf_user_id,
		secret = excluded.secret, device_id = excluded.device_id, server_name = excluded.server_name, server_version = excluded.server_version, updated_at = excluded.updated_at`,
		c.UserID, c.Kind, c.ServerURL, c.Username, c.JFUserID, c.Secret, c.DeviceID, c.ServerName, c.ServerVersion, Now())
	return err
}

// ConnectedUsers lists everyone with a connection that isn't switched off,
// plus (with Core) everyone without one.
func (s *Store) ConnectedUsers(ctx context.Context) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT u.id FROM users u LEFT JOIN connections c ON c.user_id = u.id WHERE c.kind IS NULL OR c.kind <> 'off' ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---- watches ----

func joinGenres(g []string) string {
	if len(g) == 0 {
		return ""
	}
	return "|" + strings.Join(g, "|") + "|"
}

func splitGenres(s string) []string {
	s = strings.Trim(s, "|")
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "|")
}

const watchCols = `id, item_id, kind, title, series_id, series, season, episode, year, genres, runtime, watched_at, day, position, seconds, completed, source, device, tmdb`

func scanWatch(row interface{ Scan(...any) error }) (Watch, error) {
	var w Watch
	var g string
	var done int
	err := row.Scan(&w.ID, &w.ItemID, &w.Kind, &w.Title, &w.SeriesID, &w.Series, &w.Season, &w.Episode, &w.Year, &g, &w.Runtime, &w.WatchedAt, &w.Day, &w.Position, &w.Seconds, &done, &w.Source, &w.Device, &w.TMDB)
	w.Genres = splitGenres(g)
	w.Completed = done == 1
	return w, err
}

// LocalDay is the local date of a UTC timestamp.
func LocalDay(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return Today()
	}
	return t.Local().Format("2006-01-02")
}

// UpsertWatch records a title watched on a day: one row per title per day,
// later reports move it forward (position, seconds, completed never go back).
// added reports a new row.
func (s *Store) UpsertWatch(ctx context.Context, uid int64, w *Watch) (added bool, err error) {
	if w.Day == "" {
		w.Day = LocalDay(w.WatchedAt)
	}
	var id int64
	var oldPos, oldSec, oldDone int
	var oldAt string
	err = s.DB.QueryRowContext(ctx, `SELECT id, position, seconds, completed, watched_at FROM watches WHERE user_id = ? AND item_id = ? AND day = ?`, uid, w.ItemID, w.Day).
		Scan(&id, &oldPos, &oldSec, &oldDone, &oldAt)
	if errors.Is(err, sql.ErrNoRows) {
		res, err := s.DB.ExecContext(ctx, `INSERT INTO watches (user_id, item_id, kind, title, series_id, series, season, episode, year, genres, runtime, watched_at, day, position, seconds, completed, source, device, tmdb)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uid, w.ItemID, w.Kind, w.Title, w.SeriesID, w.Series, w.Season, w.Episode, w.Year, joinGenres(w.Genres), w.Runtime, w.WatchedAt, w.Day, w.Position, w.Seconds, boolInt(w.Completed), w.Source, w.Device, w.TMDB)
		if err != nil {
			return false, err
		}
		w.ID, _ = res.LastInsertId()
		return true, nil
	}
	if err != nil {
		return false, err
	}
	w.ID = id
	if w.Position < oldPos && !w.Completed {
		w.Position = oldPos
	}
	if w.Seconds < oldSec {
		w.Seconds = oldSec
	}
	if oldDone == 1 {
		w.Completed = true
	}
	if w.WatchedAt < oldAt {
		w.WatchedAt = oldAt
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE watches SET title = ?, series_id = ?, series = ?, season = ?, episode = ?, year = ?, genres = CASE WHEN ? = '' THEN genres ELSE ? END,
		runtime = CASE WHEN ? > 0 THEN ? ELSE runtime END, watched_at = ?, position = ?, seconds = ?, completed = ?,
		source = CASE WHEN source = 'pocket' THEN source ELSE ? END, device = CASE WHEN ? = '' THEN device ELSE ? END, tmdb = CASE WHEN ? = '' THEN tmdb ELSE ? END
		WHERE id = ?`,
		w.Title, w.SeriesID, w.Series, w.Season, w.Episode, w.Year, joinGenres(w.Genres), joinGenres(w.Genres), w.Runtime, w.Runtime, w.WatchedAt, w.Position, w.Seconds, boolInt(w.Completed),
		w.Source, w.Device, w.Device, w.TMDB, w.TMDB, id)
	return false, err
}

// WatchFilter narrows a history query.
type WatchFilter struct {
	From, To string // local days, inclusive ("" = open)
	Kind     string // movie | episode
	SeriesID string
	Q        string
	Limit    int // <= 0: 100; -1 = all
	Offset   int
}

// Watches lists a person's history, newest first.
func (s *Store) Watches(ctx context.Context, uid int64, f WatchFilter) ([]Watch, int, error) {
	where := []string{"user_id = ?"}
	args := []any{uid}
	if f.From != "" {
		where, args = append(where, "day >= ?"), append(args, f.From)
	}
	if f.To != "" {
		where, args = append(where, "day <= ?"), append(args, f.To)
	}
	if f.Kind != "" {
		where, args = append(where, "kind = ?"), append(args, f.Kind)
	}
	if f.SeriesID != "" {
		where, args = append(where, "series_id = ?"), append(args, f.SeriesID)
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		where, args = append(where, "(title LIKE ? OR series LIKE ?)"), append(args, "%"+q+"%", "%"+q+"%")
	}
	w := strings.Join(where, " AND ")
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM watches WHERE `+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := f.Limit
	if limit == 0 {
		limit = 100
	}
	q := `SELECT ` + watchCols + ` FROM watches WHERE ` + w + ` ORDER BY watched_at DESC`
	if limit > 0 {
		q += ` LIMIT ? OFFSET ?`
		args = append(args, limit, f.Offset)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Watch{}
	for rows.Next() {
		x, err := scanWatch(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, x)
	}
	return out, total, rows.Err()
}

// DeleteWatch removes one row of a person's history.
func (s *Store) DeleteWatch(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM watches WHERE user_id = ? AND id = ?`, uid, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddWatches inserts rows (an import); rows already there are skipped.
func (s *Store) AddWatches(ctx context.Context, uid int64, ws []Watch) (int, error) {
	n := 0
	for i := range ws {
		w := ws[i]
		if w.ItemID == "" || w.WatchedAt == "" {
			continue
		}
		if w.Source == "" {
			w.Source = "import"
		}
		added, err := s.UpsertWatch(ctx, uid, &w)
		if err != nil {
			return n, err
		}
		if added {
			n++
		}
	}
	return n, nil
}

// WatchedSeries returns the shows a person watched since a day, most recent first.
func (s *Store) WatchedSeries(ctx context.Context, uid int64, since string) ([]Watch, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT series_id, series, MAX(watched_at), COUNT(1) FROM watches WHERE user_id = ? AND kind = 'episode' AND series_id <> '' AND day >= ? GROUP BY series_id ORDER BY MAX(watched_at) DESC`, uid, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Watch
	for rows.Next() {
		var w Watch
		if err := rows.Scan(&w.SeriesID, &w.Series, &w.WatchedAt, &w.Episode); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetSync returns where reading history stands for a person.
func (s *Store) GetSync(ctx context.Context, uid int64) SyncState {
	var st SyncState
	var b int
	_ = s.DB.QueryRowContext(ctx, `SELECT server_key, watermark, backfilled, synced_at, error FROM watch_sync WHERE user_id = ?`, uid).Scan(&st.ServerKey, &st.Watermark, &b, &st.SyncedAt, &st.Error)
	st.Backfilled = b == 1
	return st
}

// SaveSync stores where reading history stands.
func (s *Store) SaveSync(ctx context.Context, uid int64, st SyncState) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO watch_sync (user_id, server_key, watermark, backfilled, synced_at, error) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET server_key = excluded.server_key, watermark = excluded.watermark, backfilled = excluded.backfilled, synced_at = excluded.synced_at, error = excluded.error`,
		uid, st.ServerKey, st.Watermark, boolInt(st.Backfilled), st.SyncedAt, st.Error)
	return err
}

// ---- watchlist ----

// Watchlist lists what a person wants to watch, newest first.
func (s *Store) Watchlist(ctx context.Context, uid int64) ([]WatchlistItem, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, media_type, item_id, tmdb_id, title, year, poster, note, added_at FROM watchlist WHERE user_id = ? ORDER BY added_at DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WatchlistItem{}
	for rows.Next() {
		var w WatchlistItem
		if err := rows.Scan(&w.ID, &w.MediaType, &w.ItemID, &w.TMDBID, &w.Title, &w.Year, &w.Poster, &w.Note, &w.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// AddToWatchlist adds a title unless it is already there (by Jellyfin id or TMDB id).
func (s *Store) AddToWatchlist(ctx context.Context, uid int64, w *WatchlistItem) error {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM watchlist WHERE user_id = ? AND ((item_id <> '' AND item_id = ?) OR (tmdb_id > 0 AND tmdb_id = ? AND media_type = ?))`, uid, w.ItemID, w.TMDBID, w.MediaType).Scan(&id)
	if err == nil {
		w.ID = id
		_, err = s.DB.ExecContext(ctx, `UPDATE watchlist SET item_id = CASE WHEN ? <> '' THEN ? ELSE item_id END, tmdb_id = CASE WHEN ? > 0 THEN ? ELSE tmdb_id END WHERE id = ?`, w.ItemID, w.ItemID, w.TMDBID, w.TMDBID, id)
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if w.AddedAt == "" {
		w.AddedAt = Now()
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO watchlist (user_id, media_type, item_id, tmdb_id, title, year, poster, note, added_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uid, w.MediaType, w.ItemID, w.TMDBID, w.Title, w.Year, w.Poster, w.Note, w.AddedAt)
	if err != nil {
		return err
	}
	w.ID, _ = res.LastInsertId()
	return nil
}

// RemoveFromWatchlist removes an entry.
func (s *Store) RemoveFromWatchlist(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM watchlist WHERE user_id = ? AND id = ?`, uid, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// LinkWatchlist records the Jellyfin id of a title that arrived.
func (s *Store) LinkWatchlist(ctx context.Context, uid int64, mediaType string, tmdbID int, itemID string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE watchlist SET item_id = ? WHERE user_id = ? AND media_type = ? AND tmdb_id = ? AND item_id = ''`, itemID, uid, mediaType, tmdbID)
}

// ---- requests ----

const reqCols = `id, seerr_id, media_type, tmdb_id, title, year, poster, seasons, status, jellyfin_id, notified, arrived_at, created_at, updated_at`

func scanReq(row interface{ Scan(...any) error }) (Request, error) {
	var r Request
	var n int
	err := row.Scan(&r.ID, &r.SeerrID, &r.MediaType, &r.TMDBID, &r.Title, &r.Year, &r.Poster, &r.Seasons, &r.Status, &r.JellyfinID, &n, &r.ArrivedAt, &r.CreatedAt, &r.UpdatedAt)
	r.Notified = n == 1
	return r, err
}

// Requests lists a person's followed requests, newest first.
func (s *Store) Requests(ctx context.Context, uid int64) ([]Request, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+reqCols+` FROM requests WHERE user_id = ? ORDER BY created_at DESC, id DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, err := scanReq(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveRequest inserts or refreshes a followed request; it returns the status
// it had before ("" when new) and whether its arrival was already announced.
func (s *Store) SaveRequest(ctx context.Context, uid int64, r *Request) (prev string, notified bool, err error) {
	var n int
	err = s.DB.QueryRowContext(ctx, `SELECT id, status, notified FROM requests WHERE user_id = ? AND seerr_id = ?`, uid, r.SeerrID).Scan(&r.ID, &prev, &n)
	if errors.Is(err, sql.ErrNoRows) {
		if r.CreatedAt == "" {
			r.CreatedAt = Now()
		}
		res, err := s.DB.ExecContext(ctx, `INSERT INTO requests (user_id, seerr_id, media_type, tmdb_id, title, year, poster, seasons, status, jellyfin_id, notified, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uid, r.SeerrID, r.MediaType, r.TMDBID, r.Title, r.Year, r.Poster, r.Seasons, r.Status, r.JellyfinID, boolInt(r.Notified), r.CreatedAt)
		if err != nil {
			return "", false, conflict(err)
		}
		r.ID, _ = res.LastInsertId()
		return "", r.Notified, nil
	}
	if err != nil {
		return "", false, err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE requests SET media_type = ?, tmdb_id = ?, title = CASE WHEN ? = '' THEN title ELSE ? END, year = CASE WHEN ? = 0 THEN year ELSE ? END,
		poster = CASE WHEN ? = '' THEN poster ELSE ? END, seasons = ?, status = ?, jellyfin_id = CASE WHEN ? = '' THEN jellyfin_id ELSE ? END,
		arrived_at = CASE WHEN ? = 'available' AND status <> 'available' THEN ? ELSE arrived_at END, updated_at = ? WHERE id = ?`,
		r.MediaType, r.TMDBID, r.Title, r.Title, r.Year, r.Year, r.Poster, r.Poster, r.Seasons, r.Status, r.JellyfinID, r.JellyfinID, r.Status, Now(), Now(), r.ID)
	return prev, n == 1, err
}

// MarkRequestNotified records that a person heard about an arrival.
func (s *Store) MarkRequestNotified(ctx context.Context, uid, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE requests SET notified = 1 WHERE user_id = ? AND id = ?`, uid, id)
	return err
}

// ForgetRequest drops a followed request (cancelled or gone from Jellyseerr).
func (s *Store) ForgetRequest(ctx context.Context, uid int64, seerrID int) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM requests WHERE user_id = ? AND seerr_id = ?`, uid, seerrID)
	return err
}

// PrevPosition is how far a person had got into a title before a day.
func (s *Store) PrevPosition(ctx context.Context, uid int64, itemID, day string) int {
	var p sql.NullInt64
	_ = s.DB.QueryRowContext(ctx, `SELECT MAX(position) FROM watches WHERE user_id = ? AND item_id = ? AND day < ? AND completed = 0`, uid, itemID, day).Scan(&p)
	return int(p.Int64)
}

// ---- subtitle offsets ----

// SubtitleOffsets returns a person's offsets for a title's subtitle tracks (stream index → ms).
func (s *Store) SubtitleOffsets(ctx context.Context, uid int64, itemID string) map[int]int {
	out := map[int]int{}
	rows, err := s.DB.QueryContext(ctx, `SELECT stream, ms FROM subtitle_offsets WHERE user_id = ? AND item_id = ?`, uid, itemID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var st, ms int
		if rows.Scan(&st, &ms) == nil {
			out[st] = ms
		}
	}
	return out
}

// SetSubtitleOffset stores (or with 0 forgets) a person's offset for one track.
func (s *Store) SetSubtitleOffset(ctx context.Context, uid int64, itemID string, stream, ms int) error {
	if ms == 0 {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM subtitle_offsets WHERE user_id = ? AND item_id = ? AND stream = ?`, uid, itemID, stream)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO subtitle_offsets (user_id, item_id, stream, ms, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id, item_id, stream) DO UPDATE SET ms = excluded.ms, updated_at = excluded.updated_at`, uid, itemID, stream, ms, Now())
	return err
}
