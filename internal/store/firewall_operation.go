package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"path/filepath"

	"novapanel/internal/exec"
	"novapanel/internal/firewall"
	"novapanel/internal/id"
)

const (
	FirewallPrepared    = "prepared"
	FirewallSnapshotted = "snapshotted"
	FirewallArmed       = "armed"
	FirewallApplied     = "applied"
	FirewallConfirmed   = "confirmed"
	FirewallReverted    = "reverted"
)

// FirewallApply reserves the single pending change and runs under the firewall lock.
// A failed or interrupted effect must be reconciled, never blindly re-applied.
func (s *Store) FirewallApply(ctx context.Context, jobID string, p FirewallPending, runner exec.Runner, rulesDir string, rules []firewall.Rule, sshPort int, webListen string, windowMin int) error {
	if !id.Valid(p.ID) || !filepath.IsAbs(p.SnapshotPath) || filepath.Clean(p.SnapshotPath) != p.SnapshotPath || p.RevertUnit != "novapanel-fwrevert-"+p.ID+".timer" || windowMin <= 0 || p.Deadline <= s.clock.Now() {
		return ErrSiteInvalid
	}
	if err := firewall.DryRun(ctx, runner, rules, sshPort, webListen); err != nil {
		return err
	}
	result, _ := json.Marshal(map[string]string{"pending_id": p.ID})
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM fw_pending)").Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrSiteConflict
		}
		r, err := tx.ExecContext(ctx, "UPDATE jobs SET step=?,result_json=?,updated=? WHERE id=? AND verb='firewall.apply' AND state='running' AND effectful=1 AND coalesce(step,'')=''", FirewallPrepared, string(result), s.clock.Now(), jobID)
		if err = affected(r, err); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO fw_pending(id,snapshot_path,revert_unit,deadline) VALUES(?,?,?,?)", p.ID, p.SnapshotPath, p.RevertUnit, p.Deadline)
		return err
	}); err != nil {
		return err
	}
	if err := firewall.Snapshot(ctx, runner, rulesDir, p.SnapshotPath); err != nil {
		return err
	}
	if err := s.firewallStep(ctx, jobID, FirewallPrepared, FirewallSnapshotted); err != nil {
		return err
	}
	unit, err := firewall.Arm(ctx, runner, p.ID, p.SnapshotPath, windowMin)
	if err != nil {
		return err
	}
	if unit != p.RevertUnit {
		return ErrSiteConflict
	}
	if err = s.firewallStep(ctx, jobID, FirewallSnapshotted, FirewallArmed); err != nil {
		return err
	}
	if s.clock.Now() >= p.Deadline {
		return ErrSitePrecondition
	}
	if err = firewall.Apply(ctx, runner, rules, sshPort, webListen); err != nil {
		return err
	}
	return s.firewallStep(ctx, jobID, FirewallArmed, FirewallApplied)
}

func (s *Store) firewallStep(ctx context.Context, jobID, from, to string) error {
	r, err := s.db.ExecContext(ctx, "UPDATE jobs SET step=?,updated=? WHERE id=? AND verb='firewall.apply' AND step=?", to, s.clock.Now(), jobID, from)
	return affected(r, err)
}

// ReconcileFirewall is called under the same firewall lock as apply and confirm.
// Early interruptions have no firewall effect; armed changes wait for the deadline.
func (s *Store) ReconcileFirewall(ctx context.Context, jobID string, runner exec.Runner, rulesDir string, force bool) error {
	return s.reconcileFirewall(ctx, jobID, runner, rulesDir, force, false)
}

// ReconcileFirewallLostTimer restores immediately when the independent timer is gone.
func (s *Store) ReconcileFirewallLostTimer(ctx context.Context, jobID string, runner exec.Runner, rulesDir string) error {
	return s.reconcileFirewall(ctx, jobID, runner, rulesDir, true, true)
}

func (s *Store) reconcileFirewall(ctx context.Context, jobID string, runner exec.Runner, rulesDir string, force, timerMissing bool) error {
	j, err := s.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	var ref struct {
		PendingID string `json:"pending_id"`
	}
	if json.Unmarshal(j.Result, &ref) != nil || !id.Valid(ref.PendingID) {
		return ErrSiteInvalid
	}
	if j.Step == FirewallReverted {
		return nil
	}
	p, err := s.FirewallPending(ctx)
	if err != nil {
		return err
	}
	if p.ID != ref.PendingID {
		return ErrSiteConflict
	}
	switch j.Step {
	case FirewallConfirmed:
		if !p.Confirmed {
			return ErrSiteConflict
		}
		if !timerMissing {
			if err = firewall.Cancel(ctx, runner, p.ID); err != nil {
				return err
			}
		}
		return s.DeleteFirewallPending(ctx, p.ID)
	case FirewallPrepared, FirewallSnapshotted:
		// Arm may have succeeded before its checkpoint was persisted.
		if j.Step == FirewallSnapshotted && !timerMissing {
			if err = firewall.Cancel(ctx, runner, p.ID); err != nil {
				return err
			}
		}
	case FirewallArmed, FirewallApplied:
		if !force && s.clock.Now() < p.Deadline {
			return nil
		}
		if err = firewall.Restore(ctx, runner, rulesDir, p.SnapshotPath); err != nil {
			return err
		}
		if !timerMissing {
			if err = firewall.Cancel(ctx, runner, p.ID); err != nil {
				return err
			}
		}
	default:
		return ErrSitePrecondition
	}
	return s.finishFirewall(ctx, jobID, p.ID, j.Step, FirewallReverted)
}

// ConfirmFirewall records confirmation before stopping the timer, so recovery can finish cancellation.
func (s *Store) ConfirmFirewall(ctx context.Context, jobID, pendingID string, runner exec.Runner) error {
	j, err := s.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	if j.Step != FirewallApplied && j.Step != FirewallArmed {
		return ErrSitePrecondition
	}
	p, err := s.FirewallPending(ctx)
	if err != nil {
		return err
	}
	if p.ID != pendingID || s.clock.Now() >= p.Deadline {
		return ErrSitePrecondition
	}
	var ref struct {
		PendingID string `json:"pending_id"`
	}
	if json.Unmarshal(j.Result, &ref) != nil || ref.PendingID != p.ID {
		return ErrSiteConflict
	}
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		r, e := tx.ExecContext(ctx, "UPDATE fw_pending SET confirmed=1 WHERE id=? AND confirmed=0 AND deadline>?", p.ID, s.clock.Now())
		if e = affected(r, e); e != nil {
			return e
		}
		r, e = tx.ExecContext(ctx, "UPDATE jobs SET step=?,updated=? WHERE id=? AND step=?", FirewallConfirmed, s.clock.Now(), jobID, j.Step)
		return affected(r, e)
	}); err != nil {
		return err
	}
	if err = firewall.Cancel(ctx, runner, p.ID); err != nil {
		return err
	}
	return s.DeleteFirewallPending(ctx, p.ID)
}

func (s *Store) finishFirewall(ctx context.Context, jobID, pendingID, from, to string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, "UPDATE jobs SET step=?,updated=? WHERE id=? AND step=?", to, s.clock.Now(), jobID, from)
		if err = affected(r, err); err != nil {
			return err
		}
		r, err = tx.ExecContext(ctx, "DELETE FROM fw_pending WHERE id=?", pendingID)
		return affected(r, err)
	})
}
