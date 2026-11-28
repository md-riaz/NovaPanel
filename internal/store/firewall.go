package store

import (
	"context"
	"database/sql"
)

type FirewallPending struct {
	ID, SnapshotPath, RevertUnit string
	Deadline                     int64
	Confirmed                    bool
}

// ReserveFirewall serializes admission of pending changes in a single transaction.
func (s *Store) ReserveFirewall(ctx context.Context, p FirewallPending) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM fw_pending)").Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrSiteConflict
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO fw_pending(id,snapshot_path,revert_unit,deadline) VALUES(?,?,?,?)", p.ID, p.SnapshotPath, p.RevertUnit, p.Deadline)
		return err
	})
}

func (s *Store) FirewallPending(ctx context.Context) (p FirewallPending, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT id,snapshot_path,revert_unit,deadline,confirmed FROM fw_pending").Scan(&p.ID, &p.SnapshotPath, &p.RevertUnit, &p.Deadline, &p.Confirmed)
	return
}

func (s *Store) DeleteFirewallPending(ctx context.Context, id string) error {
	r, err := s.db.ExecContext(ctx, "DELETE FROM fw_pending WHERE id=?", id)
	return affected(r, err)
}

// FirewallJob resolves the owning apply job from the reserved pending nonce.
func (s *Store) FirewallJob(ctx context.Context, pendingID string) (string, error) {
	var jobID string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM jobs WHERE verb='firewall.apply' AND json_extract(result_json,'$.pending_id')=? AND step IN ('prepared','snapshotted','armed','applied','confirmed') ORDER BY created DESC LIMIT 1`, pendingID).Scan(&jobID)
	return jobID, err
}
