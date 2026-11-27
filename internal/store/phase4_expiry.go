package store

import (
	"context"
	"database/sql"
)

func (s *Store) FailOperation(ctx context.Context, jid, code string) error {
	return s.failOperation(ctx, jid, code, false)
}

// FailRestoredPHP atomically records a failed job and its verified restoration.
func (s *Store) FailRestoredPHP(ctx context.Context, jid, code string) error {
	return s.failOperation(ctx, jid, code, true)
}

func (s *Store) failOperation(ctx context.Context, jid, code string, restored bool) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE phase4_operations SET updated=updated WHERE job_id=?", jid); e != nil {
			return e
		}
		op, e := loadOperation(ctx, tx, jid)
		if e != nil {
			return e
		}
		now := s.clock.Now()
		if TeardownStarted(op) {
			return ErrOperationLocked
		}
		if restored && (op.Kind != PHPSet || op.Checkpoint == "done" || op.Checkpoint == "expired") {
			return ErrSitePrecondition
		}
		expired := op.Checkpoint == "expired" || now >= op.Expires
		if op.Kind == PHPSet && !op.Reconciled && expired && !restored {
			return ErrOperationLocked
		}
		if expired {
			code = "interrupted"
		}
		r, e := tx.ExecContext(ctx, `UPDATE jobs SET state='failed',code=?,error='job execution failed',updated=?,finished=? WHERE id=? AND state='running'`, code, now, now, jid)
		if e = affected(r, e); e != nil {
			return e
		}
		if code == "interrupted" && !expired && !restored {
			return nil
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM job_claims WHERE job_id=? AND NOT EXISTS (SELECT 1 FROM phase4_operations WHERE job_id=? AND ciphertext IS NOT NULL)", jid, jid); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE phase4_operations SET checkpoint='expired',updated=?,reconciled=? WHERE job_id=? AND checkpoint<>'done'", now, restored, jid)
		return e
	})
}

// FailUnappliedOperation is used only when the first worker attempt rejected its preflight before invoking any effect.
func (s *Store) FailUnappliedOperation(ctx context.Context, jid, code string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		op, err := loadOperation(ctx, tx, jid)
		if err != nil {
			return err
		}
		if op.Checkpoint != "prepared" {
			return ErrSitePrecondition
		}
		now := s.clock.Now()
		r, err := tx.ExecContext(ctx, `UPDATE jobs SET state='failed',code=?,error='job execution failed',updated=?,finished=? WHERE id=? AND state='running' AND attempts=1`, code, now, now, jid)
		if err = affected(r, err); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM job_claims WHERE job_id=?", jid); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE phase4_operations SET checkpoint='expired',nonce=NULL,ciphertext=NULL,updated=? WHERE job_id=? AND checkpoint='prepared'", now, jid)
		return err
	})
}

// ExpireOperations marks at most 100 unfinished journals expired in one commit.
// Historical identities remain blockers until an authorized verifier reconciles.
func (s *Store) ExpireOperations(ctx context.Context) (count int, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE phase4_operations SET updated=updated WHERE 0"); e != nil {
			return e
		}
		now := s.clock.Now()
		// PHP publication can leave externally visible configuration behind.
		// Do not convert it to an expired tombstone and reopen startup admission
		// until an authorized recovery has established a safe disposition.
		var unresolved int
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM phase4_operations WHERE kind='php_set' AND reconciled=0 AND (checkpoint='expired' OR (checkpoint<>'done' AND expires<=?)))`, now).Scan(&unresolved); e != nil {
			return e
		}
		if unresolved != 0 {
			return ErrOperationLocked
		}
		rows, e := tx.QueryContext(ctx, `SELECT job_id FROM phase4_operations INDEXED BY idx_phase4_operations_expiry WHERE expires<=? AND checkpoint NOT IN ('done','expired') AND NOT(kind IN ('site_delete','database_delete','dbuser_delete') AND checkpoint IN ('teardown_started','db_applied')) AND NOT(kind='wordpress_install' AND EXISTS (SELECT 1 FROM site_account_provenance p WHERE p.site_id=phase4_operations.site_id AND p.allocating_job=phase4_operations.job_id)) AND NOT(kind='site_update' AND EXISTS (SELECT 1 FROM phase4_operation_children c WHERE c.job_id=phase4_operations.job_id AND c.child_kind='maintenance_on' AND c.bracket_engaged=1) AND NOT EXISTS (SELECT 1 FROM phase4_operation_children c WHERE c.job_id=phase4_operations.job_id AND c.child_kind='maintenance_off' AND c.status='committed')) ORDER BY expires,job_id LIMIT 100`, now)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range ids {
			if _, e = tx.ExecContext(ctx, "UPDATE phase4_operations SET checkpoint='expired',updated=? WHERE job_id=?", now, id); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, "DELETE FROM job_claims WHERE job_id=? AND EXISTS (SELECT 1 FROM jobs WHERE id=? AND state<>'done') AND NOT EXISTS (SELECT 1 FROM phase4_operations WHERE job_id=? AND ciphertext IS NOT NULL)", id, id, id); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='failed',code='interrupted',error='operation recovery expired',updated=?,finished=? WHERE id=? AND state IN ('queued','running')`, now, now, id); e != nil {
				return e
			}
		}
		count = len(ids)
		return nil
	})
	return
}

// ReconcileOperation records an agent-only assertion made under the journal lock
// set after the caller verifies consistent or explicitly non-serving disposition.
func (s *Store) ReconcileOperation(ctx context.Context, jid string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		r, e := tx.ExecContext(ctx, "UPDATE phase4_operations SET reconciled=1,updated=? WHERE job_id=? AND checkpoint='expired' AND ciphertext IS NULL", s.clock.Now(), jid)
		return affected(r, e)
	})
}

// ExpiredClaimInput authenticates the retained claim reservation before disclosing sealed input.
func (s *Store) ExpiredClaimInput(ctx context.Context, jid, hash, owner string) (Operation, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM job_claims c JOIN jobs j ON j.id=c.job_id JOIN phase4_operations o ON o.job_id=j.id WHERE j.id=? AND j.created_by=? AND j.created_by<>'' AND c.claim_hash=? AND c.claimed=0 AND length(c.ciphertext)=0 AND o.checkpoint='expired' AND o.kind IN ('database_create','database_rotate','dbuser_create','wordpress_install') AND length(o.ciphertext)>0`, jid, owner, hash).Scan(&exists)
	if err != nil {
		return Operation{}, err
	}
	return s.LoadOperation(ctx, jid)
}

// PublishExpiredClaim atomically publishes a claim before erasing its retained input.
func (s *Store) PublishExpiredClaim(ctx context.Context, op Operation, hash, owner string, nonce, ciphertext []byte) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if err := publicationPending(ctx, tx, op.JobID); err != nil {
			return err
		}
		now := s.clock.Now()
		r, err := tx.ExecContext(ctx, `UPDATE job_claims SET nonce=?,ciphertext=?,expires=? WHERE job_id=? AND claim_hash=? AND claimed=0 AND length(ciphertext)=0 AND EXISTS(SELECT 1 FROM jobs WHERE id=? AND created_by=? AND created_by<>'') AND EXISTS(SELECT 1 FROM phase4_operations WHERE job_id=? AND checkpoint='expired' AND nonce=? AND ciphertext=?)`, nonce, ciphertext, now+ClaimTTLMillis, op.JobID, hash, op.JobID, owner, op.JobID, op.Nonce, op.Ciphertext)
		if err = affected(r, err); err != nil {
			return err
		}
		r, err = tx.ExecContext(ctx, `UPDATE phase4_operations SET nonce=NULL,ciphertext=NULL,updated=? WHERE job_id=? AND checkpoint='expired' AND nonce=? AND ciphertext=?`, now, op.JobID, op.Nonce, op.Ciphertext)
		return affected(r, err)
	})
}
