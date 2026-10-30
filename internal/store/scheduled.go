package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// EnqueueScheduledJob atomically advances cadence and publishes work, or skips a
// busy occurrence. A lost next_run CAS is a no-op, never a second firing.
func (s *Store) EnqueueScheduledJob(ctx context.Context, scheduleID string, expectedNext, next, now int64, state json.RawMessage, j Job, claim *JobClaim) (enqueued bool, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		r, e := tx.ExecContext(ctx, "UPDATE schedules SET next_run=?,last_run=?,state_json=? WHERE id=? AND next_run=? AND enabled=1", next, now, string(state), scheduleID, expectedNext)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e != nil || n == 0 {
			return e
		}
		owner, e := scanSchedule(tx.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id=?", scheduleID))
		if e != nil {
			return e
		}
		var busy int
		if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE object_key=? AND state='running'", j.ObjectKey).Scan(&busy); e != nil {
			return e
		}
		if busy > 0 {
			return nil
		}
		j.Actor = owner.Actor
		j.Role = owner.Role
		j.Scopes = owner.Scopes
		j.Automation = owner.Automation
		j.CreatedBy = owner.CreatedBy
		j.Verb = owner.Verb
		j.Args = owner.Args
		j.State = "queued"
		if e = insertJob(ctx, tx, j); e != nil {
			return e
		}
		if claim != nil {
			if claim.JobID != j.ID {
				return errors.New("claim job mismatch")
			}
			c := *claim
			c.Nonce = nil
			c.Ciphertext = nil
			c.Expires = 0
			if e = insertClaim(ctx, tx, c); e != nil {
				return e
			}
		}
		enqueued = true
		return nil
	})
	if err != nil {
		enqueued = false
	}
	return
}
