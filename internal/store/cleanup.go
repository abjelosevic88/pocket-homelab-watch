package store

import (
	"context"
	"encoding/json"
)

// ---- clean-up rules, leaving soon, keeps and the log ----

// Condition is one test a title must pass for a rule.
type Condition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// CleanupRule says which films, shows or seasons may leave the library.
type CleanupRule struct {
	ID           int64       `json:"id"`
	Name         string      `json:"name"`
	Target       string      `json:"target"` // movie | series | season
	Match        string      `json:"match"`  // all | any
	Conditions   []Condition `json:"conditions"`
	Protect      []string    `json:"protect"` // watchlist | in_progress | favorite
	GraceDays    int         `json:"grace_days"`
	Action       string      `json:"action"` // delete | unmonitor
	ExcludeLists bool        `json:"exclude_lists"`
	ClearRequest bool        `json:"clear_request"`
	Active       bool        `json:"active"`
	LastRunAt    string      `json:"last_run_at"`
	LastMatched  int         `json:"last_matched"`
	LastError    string      `json:"last_error"`
	CreatedAt    string      `json:"created_at"`
	UpdatedAt    string      `json:"updated_at"`
}

// LeavingItem is one title on its way out of the library.
type LeavingItem struct {
	ItemID    string `json:"item_id"`
	RuleID    int64  `json:"rule_id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	SeriesID  string `json:"series_id"`
	Season    int    `json:"season"`
	TMDB      int    `json:"tmdb"`
	TVDB      int    `json:"tvdb"`
	Size      int64  `json:"size"`
	Reason    string `json:"reason"`
	AddedBy   int64  `json:"added_by"`
	MatchedAt string `json:"matched_at"`
	RemoveOn  string `json:"remove_on"`
	Notified  bool   `json:"notified"`
	Attempts  int    `json:"attempts"`
	Error     string `json:"error"`
}

// Keep is a title someone asked to keep.
type Keep struct {
	ItemID    string `json:"item_id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	UserID    int64  `json:"user_id"`
	CreatedAt string `json:"created_at"`
}

// CleanupLog is one removal (or failed one).
type CleanupLog struct {
	ID     int64  `json:"id"`
	At     string `json:"at"`
	ItemID string `json:"item_id"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Rule   string `json:"rule"`
	Action string `json:"action"`
	Via    string `json:"via"`
	Size   int64  `json:"size"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

const ruleCols = `id, name, target, match, conditions, protect, grace_days, action, exclude_lists, clear_request, active, last_run_at, last_matched, last_error, created_at, updated_at`

func scanRule(row interface{ Scan(...any) error }) (CleanupRule, error) {
	var r CleanupRule
	var cond, prot string
	var ex, cl, act int
	err := row.Scan(&r.ID, &r.Name, &r.Target, &r.Match, &cond, &prot, &r.GraceDays, &r.Action, &ex, &cl, &act, &r.LastRunAt, &r.LastMatched, &r.LastError, &r.CreatedAt, &r.UpdatedAt)
	_ = json.Unmarshal([]byte(cond), &r.Conditions)
	_ = json.Unmarshal([]byte(prot), &r.Protect)
	if r.Conditions == nil {
		r.Conditions = []Condition{}
	}
	if r.Protect == nil {
		r.Protect = []string{}
	}
	r.ExcludeLists, r.ClearRequest, r.Active = ex == 1, cl == 1, act == 1
	return r, err
}

// CleanupRules lists every rule.
func (s *Store) CleanupRules(ctx context.Context) ([]CleanupRule, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+ruleCols+` FROM cleanup_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CleanupRule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CleanupRule reads one rule.
func (s *Store) CleanupRule(ctx context.Context, id int64) (*CleanupRule, error) {
	r, err := scanRule(s.DB.QueryRowContext(ctx, `SELECT `+ruleCols+` FROM cleanup_rules WHERE id = ?`, id))
	if err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

// SaveCleanupRule creates (ID 0) or updates a rule.
func (s *Store) SaveCleanupRule(ctx context.Context, r *CleanupRule) error {
	cond, _ := json.Marshal(r.Conditions)
	prot, _ := json.Marshal(r.Protect)
	if r.ID == 0 {
		res, err := s.DB.ExecContext(ctx, `INSERT INTO cleanup_rules (name, target, match, conditions, protect, grace_days, action, exclude_lists, clear_request, active) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Name, r.Target, r.Match, string(cond), string(prot), r.GraceDays, r.Action, boolInt(r.ExcludeLists), boolInt(r.ClearRequest), boolInt(r.Active))
		if err != nil {
			return err
		}
		r.ID, _ = res.LastInsertId()
		return nil
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE cleanup_rules SET name = ?, target = ?, match = ?, conditions = ?, protect = ?, grace_days = ?, action = ?, exclude_lists = ?, clear_request = ?, active = ?, updated_at = ? WHERE id = ?`,
		r.Name, r.Target, r.Match, string(cond), string(prot), r.GraceDays, r.Action, boolInt(r.ExcludeLists), boolInt(r.ClearRequest), boolInt(r.Active), Now(), r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RuleRan records a rule's last run.
func (s *Store) RuleRan(ctx context.Context, id int64, matched int, errMsg string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE cleanup_rules SET last_run_at = ?, last_matched = ?, last_error = ? WHERE id = ?`, Now(), matched, errMsg, id)
}

// DeleteCleanupRule removes a rule and takes its titles off Leaving soon.
func (s *Store) DeleteCleanupRule(ctx context.Context, id int64) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM cleanup_items WHERE rule_id = ?`, id); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM cleanup_rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const leavingCols = `item_id, rule_id, kind, title, series_id, season, tmdb, tvdb, size, reason, added_by, matched_at, remove_on, notified, attempts, error`

func scanLeaving(row interface{ Scan(...any) error }) (LeavingItem, error) {
	var l LeavingItem
	var n int
	err := row.Scan(&l.ItemID, &l.RuleID, &l.Kind, &l.Title, &l.SeriesID, &l.Season, &l.TMDB, &l.TVDB, &l.Size, &l.Reason, &l.AddedBy, &l.MatchedAt, &l.RemoveOn, &n, &l.Attempts, &l.Error)
	l.Notified = n == 1
	return l, err
}

// Leaving lists everything on its way out, soonest first.
func (s *Store) Leaving(ctx context.Context) ([]LeavingItem, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+leavingCols+` FROM cleanup_items ORDER BY remove_on, title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LeavingItem{}
	for rows.Next() {
		l, err := scanLeaving(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LeavingOne reads one title on Leaving soon.
func (s *Store) LeavingOne(ctx context.Context, itemID string) (*LeavingItem, error) {
	l, err := scanLeaving(s.DB.QueryRowContext(ctx, `SELECT `+leavingCols+` FROM cleanup_items WHERE item_id = ?`, itemID))
	if err != nil {
		return nil, notFound(err)
	}
	return &l, nil
}

// PutLeaving adds a title to Leaving soon, or refreshes what is known about
// it. The day it leaves never moves later once set, unless it is set by hand.
func (s *Store) PutLeaving(ctx context.Context, l *LeavingItem, byHand bool) (added bool, err error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO cleanup_items (item_id, rule_id, kind, title, series_id, season, tmdb, tvdb, size, reason, added_by, remove_on)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(item_id) DO NOTHING`,
		l.ItemID, l.RuleID, l.Kind, l.Title, l.SeriesID, l.Season, l.TMDB, l.TVDB, l.Size, l.Reason, l.AddedBy, l.RemoveOn)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return true, nil
	}
	if byHand {
		_, err = s.DB.ExecContext(ctx, `UPDATE cleanup_items SET rule_id = 0, added_by = ?, reason = ?, remove_on = ?, attempts = 0, error = '' WHERE item_id = ?`, l.AddedBy, l.Reason, l.RemoveOn, l.ItemID)
		return false, err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE cleanup_items SET title = ?, size = ?, reason = ?, tmdb = ?, tvdb = ? WHERE item_id = ? AND rule_id = ?`, l.Title, l.Size, l.Reason, l.TMDB, l.TVDB, l.ItemID, l.RuleID)
	return false, err
}

// DropLeaving takes a title off Leaving soon.
func (s *Store) DropLeaving(ctx context.Context, itemID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM cleanup_items WHERE item_id = ?`, itemID)
	return err
}

// MarkLeavingNotified remembers that people were told.
func (s *Store) MarkLeavingNotified(ctx context.Context, itemID string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE cleanup_items SET notified = 1 WHERE item_id = ?`, itemID)
}

// LeavingFailed counts a failed removal.
func (s *Store) LeavingFailed(ctx context.Context, itemID, msg string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE cleanup_items SET attempts = attempts + 1, error = ? WHERE item_id = ?`, msg, itemID)
}

// Keeps lists what people asked to keep.
func (s *Store) Keeps(ctx context.Context) ([]Keep, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT item_id, kind, title, user_id, created_at FROM cleanup_keeps ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Keep{}
	for rows.Next() {
		var k Keep
		if err := rows.Scan(&k.ItemID, &k.Kind, &k.Title, &k.UserID, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// AddKeep keeps a title out of every rule (and off Leaving soon).
func (s *Store) AddKeep(ctx context.Context, k Keep) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cleanup_keeps (item_id, kind, title, user_id) VALUES (?, ?, ?, ?) ON CONFLICT(item_id) DO NOTHING`, k.ItemID, k.Kind, k.Title, k.UserID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cleanup_items WHERE item_id = ?`, k.ItemID); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// DeleteKeep lets the rules see a title again.
func (s *Store) DeleteKeep(ctx context.Context, itemID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM cleanup_keeps WHERE item_id = ?`, itemID)
	return err
}

// AddCleanupLog records a removal.
func (s *Store) AddCleanupLog(ctx context.Context, l CleanupLog) {
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO cleanup_log (item_id, kind, title, rule, action, via, size, ok, detail) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.ItemID, l.Kind, l.Title, l.Rule, l.Action, l.Via, l.Size, boolInt(l.OK), l.Detail)
}

// CleanupLogs lists the newest removals.
func (s *Store) CleanupLogs(ctx context.Context, limit int) ([]CleanupLog, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, at, item_id, kind, title, rule, action, via, size, ok, detail FROM cleanup_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CleanupLog{}
	for rows.Next() {
		var l CleanupLog
		var ok int
		if err := rows.Scan(&l.ID, &l.At, &l.ItemID, &l.Kind, &l.Title, &l.Rule, &l.Action, &l.Via, &l.Size, &ok, &l.Detail); err != nil {
			return nil, err
		}
		l.OK = ok == 1
		out = append(out, l)
	}
	return out, rows.Err()
}

// Wanted is one title someone has on their watchlist or asked for.
type Wanted struct {
	UserID    int64
	MediaType string // movie | tv
	ItemID    string
	TMDB      int
}

// AllWatchlists lists every person's watchlist (for the clean-up rules).
func (s *Store) AllWatchlists(ctx context.Context) ([]Wanted, error) {
	return s.wanted(ctx, `SELECT user_id, media_type, item_id, tmdb_id FROM watchlist`)
}

// AllRequests lists every person's requests (for the clean-up rules).
func (s *Store) AllRequests(ctx context.Context) ([]Wanted, error) {
	return s.wanted(ctx, `SELECT user_id, media_type, jellyfin_id, tmdb_id FROM requests WHERE status NOT IN ('declined', 'failed')`)
}

func (s *Store) wanted(ctx context.Context, q string) ([]Wanted, error) {
	rows, err := s.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Wanted
	for rows.Next() {
		var w Wanted
		if err := rows.Scan(&w.UserID, &w.MediaType, &w.ItemID, &w.TMDB); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
