package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const tlsIntentKey = "tls:publication:v1"

// TLSIntent is agent-owned nonsecret intent. One intent gates all nginx changes.
func (s *Store) TLSIntent(ctx context.Context) (json.RawMessage, error) {
	var b string
	err := s.db.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key=?", tlsIntentKey).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return json.RawMessage(b), err
}
func (s *Store) BeginTLSIntent(ctx context.Context, b json.RawMessage) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO settings(key,value_json) VALUES(?,?)", tlsIntentKey, string(b))
	return err
}
func (s *Store) ClearTLSIntent(ctx context.Context, b json.RawMessage) error {
	r, err := s.db.ExecContext(ctx, "DELETE FROM settings WHERE key=? AND value_json=?", tlsIntentKey, string(b))
	return affected(r, err)
}

// CommitManualTLS commits metadata and the replay marker together. It cannot make
// the preceding filesystem changes atomic with SQLite; the retained intent owns recovery.
func (s *Store) CommitManualTLS(ctx context.Context, siteID, jobID string, expiry int64, b json.RawMessage) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, "UPDATE sites SET cert_source='manual',updated=? WHERE id=? AND status='active'", s.clock.Now(), siteID)
		if err = affected(r, err); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE domains SET cert_state='issued',cert_staging=0,cert_not_after=? WHERE site_id=?", expiry, siteID); err != nil {
			return err
		}
		r, err = tx.ExecContext(ctx, "UPDATE jobs SET step='tls_committed',result_json='{}' WHERE id=? AND verb='ssl.upload' AND object_key=? AND state='running'", jobID, "site:"+siteID)
		if err = affected(r, err); err != nil {
			return err
		}
		r, err = tx.ExecContext(ctx, "DELETE FROM settings WHERE key=? AND value_json=?", tlsIntentKey, string(b))
		return affected(r, err)
	})
}
