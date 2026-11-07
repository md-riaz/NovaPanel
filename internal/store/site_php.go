package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// PHPLimits contains per-site overrides; nil fields use runtime defaults.
type PHPLimits struct {
	MemoryMB   *int `json:"memory_mb,omitempty"`
	UploadMB   *int `json:"upload_mb,omitempty"`
	MaxExecSec *int `json:"max_exec_sec,omitempty"`
}

func phpLimitsKey(id string) string { return "site:php_limits:" + id }

// SitePHPLimits returns persisted overrides, or an empty set for a site without overrides.
func (s *Store) SitePHPLimits(ctx context.Context, id string) (limits PHPLimits, err error) {
	var raw sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT (SELECT value_json FROM settings WHERE key=?) FROM sites WHERE id=?`, phpLimitsKey(id), id).Scan(&raw)
	if err != nil || !raw.Valid {
		return limits, err
	}
	err = json.Unmarshal([]byte(raw.String), &limits)
	return limits, err
}

// SetSitePHP atomically persists the version and complete set of overrides.
// Callers validate values and merge partial requests with the current overrides.
func (s *Store) SetSitePHP(ctx context.Context, id, version string, limits PHPLimits) error {
	if id == "" || version == "" {
		return ErrSiteInvalid
	}
	raw, err := json.Marshal(limits)
	if err != nil {
		return err
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var typ, status string
		if err := tx.QueryRowContext(ctx, "SELECT type,status FROM sites WHERE id=?", id).Scan(&typ, &status); err != nil {
			return err
		}
		if typ == "static" || status == "deleting" {
			return ErrSitePrecondition
		}
		r, err := tx.ExecContext(ctx, "UPDATE sites SET php_version=?,updated=? WHERE id=?", version, s.clock.Now(), id)
		if err = siteChanged(r, err); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value_json) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json`, phpLimitsKey(id), string(raw))
		return err
	})
}

// deleteSitePHPLimits removes settings that cannot be cascaded by SQLite.
func deleteSitePHPLimits(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM settings WHERE key=?", phpLimitsKey(id))
	return err
}
