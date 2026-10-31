package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"novapanel/internal/firewall"
	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
)

// CreateFirewallPending reserves the single armed change. The canonical table
// needs an approved forward migration before this adapter can run in production.
func (s *Store) CreateFirewallPending(ctx context.Context, p firewall.Pending) error {
	if !id.Valid(p.ID) || p.SnapshotPath == "" || p.RevertUnit != "novapanel-fwrevert-"+p.ID || p.Deadline.IsZero() {
		return rpcerr.Errf("invalid_argument", "Invalid firewall pending state")
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		// Acquire SQLite's writer lock before checking singleton state.
		if _, err := tx.ExecContext(ctx, "UPDATE fw_pending SET confirmed=confirmed"); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM fw_pending").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return rpcerr.Errf("conflict", "Firewall change already pending")
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO fw_pending(id,snapshot_path,revert_unit,deadline,confirmed) VALUES(?,?,?,?,0)", p.ID, p.SnapshotPath, p.RevertUnit, p.Deadline.UnixMilli())
		return err
	})
}

// WithPending holds the SQLite writer lock through the callback and consumption.
// This does not solve independent process access while Store holds its lifetime
// flock, or cancellation succeeding before the database commit fails. Live apply
// must remain disabled until those recovery protocols are implemented.
func (s *Store) WithPending(ctx context.Context, pendingID string, fn func(firewall.Pending) (bool, error)) error {
	if !id.Valid(pendingID) {
		return rpcerr.Errf("not_found", "Firewall pending not found or expired")
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE fw_pending SET confirmed=confirmed WHERE id=?", pendingID); err != nil {
			return err
		}
		var p firewall.Pending
		var deadline int64
		err := tx.QueryRowContext(ctx, "SELECT id,snapshot_path,revert_unit,deadline FROM fw_pending WHERE id=? AND confirmed=0", pendingID).Scan(&p.ID, &p.SnapshotPath, &p.RevertUnit, &deadline)
		if errors.Is(err, sql.ErrNoRows) {
			return rpcerr.Errf("not_found", "Firewall pending not found or expired")
		}
		if err != nil {
			return err
		}
		p.Deadline = time.UnixMilli(deadline)
		consume, err := fn(p)
		if err != nil || !consume {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM fw_pending WHERE id=?", pendingID)
		return err
	})
}
