package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

var ErrACMERateLimited = errors.New("ACME issuance rate limited")
var ErrACMEPolicy = errors.New("invalid persisted ACME policy")

// ACMEBucket is agent-generated, never decoded from RPC input.
type ACMEBucket struct {
	Key       string
	Limit     int
	Window    int64
	CheckOnly bool
}
type acmeWindow struct {
	Times   []int64 `json:"times"`
	Expires int64   `json:"expires"`
}

// ReserveACME atomically checks every budget and retains conservative reservations.
// Future timestamps survive clock rollback. Each row and the total row count are bounded.
func (s *Store) ReserveACME(ctx context.Context, buckets []ACMEBucket, now int64) error {
	if now <= 0 || len(buckets) == 0 || len(buckets) > 201 {
		return ErrACMEPolicy
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		seen := map[string]bool{}
		for _, b := range buckets {
			if len(b.Key) == 0 || len(b.Key) > 512 || strings.ContainsAny(b.Key, "\x00\r\n") || b.Limit < 1 || b.Limit > 50 || b.Window <= 0 || b.Window > 604800000 || seen[b.Key] {
				return ErrACMEPolicy
			}
			seen[b.Key] = true
			key := "acme:policy:" + b.Key
			var raw string
			var row acmeWindow
			e := tx.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key=?", key).Scan(&raw)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if e == nil && (len(raw) > 4096 || json.Unmarshal([]byte(raw), &row) != nil || len(row.Times) > 50) {
				return ErrACMEPolicy
			}
			live := []int64{}
			for _, ts := range row.Times {
				if ts <= 0 {
					return ErrACMEPolicy
				}
				if ts > now-b.Window {
					live = append(live, ts)
				}
			}
			if len(live) >= b.Limit {
				return ErrACMERateLimited
			}
			if b.CheckOnly {
				continue
			}
			live = append(live, now)
			expiry := now + b.Window
			for _, ts := range live {
				if ts+b.Window > expiry {
					expiry = ts + b.Window
				}
			}
			encoded, _ := json.Marshal(acmeWindow{live, expiry})
			if _, e = tx.ExecContext(ctx, "INSERT INTO settings(key,value_json) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json", key, string(encoded)); e != nil {
				return e
			}
		}
		// Bounded opportunistic cleanup, no migration or unbounded process-local map.
		if _, e := tx.ExecContext(ctx, `DELETE FROM settings WHERE key IN (SELECT key FROM settings WHERE key LIKE 'acme:policy:%' AND json_valid(value_json) AND json_extract(value_json,'$.expires')<=? LIMIT 128)`, now); e != nil {
			return e
		}
		var n int
		if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM settings WHERE key LIKE 'acme:policy:%'").Scan(&n); e != nil {
			return e
		}
		if n > 10000 {
			return ErrACMERateLimited
		}
		return nil
	})
}
