package store

import (
	"context"
	"database/sql"
)

func (s *Store) FailOperation(ctx context.Context, jid, code string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE phase4_operations SET updated=updated WHERE job_id=?", jid); e != nil {
			return e
		}
		op, e := loadOperation(ctx, tx, jid)
		if e != nil {
			return e
		}
		now := s.clock.Now()
		expired := op.Checkpoint == "expired" || now >= op.Expires
		if expired {
			code = "interrupted"
		}
		r, e := tx.ExecContext(ctx, `UPDATE jobs SET state='failed',code=?,error='job execution failed',updated=?,finished=? WHERE id=? AND state='running'`, code, now, now, jid)
		if e = affected(r, e); e != nil {
			return e
		}
		if code == "interrupted" && !expired {
			return nil
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM job_claims WHERE job_id=?", jid); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE phase4_operations SET checkpoint='expired',nonce=NULL,ciphertext=NULL,updated=? WHERE job_id=? AND checkpoint<>'done'", now, jid)
		return e
	})
}

// ExpireOperations clears at most 100 expired unfinished journals in one commit.
// Historical identities remain blockers until an authorized verifier reconciles.
func (s *Store) ExpireOperations(ctx context.Context) (count int, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE phase4_operations SET updated=updated WHERE 0"); e != nil {
			return e
		}
		now := s.clock.Now()
		rows, e := tx.QueryContext(ctx, `SELECT job_id FROM phase4_operations INDEXED BY idx_phase4_operations_expiry WHERE expires<=? AND checkpoint NOT IN ('done','expired') ORDER BY expires,job_id LIMIT 100`, now)
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
			if _, e = tx.ExecContext(ctx, "UPDATE phase4_operations SET checkpoint='expired',nonce=NULL,ciphertext=NULL,updated=? WHERE job_id=?", now, id); e != nil {
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
		r, e := tx.ExecContext(ctx, "UPDATE phase4_operations SET reconciled=1,updated=? WHERE job_id=? AND checkpoint='expired'", s.clock.Now(), jid)
		return affected(r, e)
	})
}
