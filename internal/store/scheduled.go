package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
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
		if strings.HasPrefix(scheduleID, "acme:auto:") {
			var eligible bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sites s JOIN domains d ON d.site_id=s.id AND d.is_primary=1 WHERE s.id=? AND s.status='active' AND s.cert_source='letsencrypt' AND s.ssl_auto=1 AND (d.cert_not_after<=? OR (s.force_https=1 AND EXISTS(SELECT 1 FROM domains a WHERE a.site_id=s.id AND a.cert_state='none'))))`, strings.TrimPrefix(scheduleID, "acme:auto:"), now+30*24*60*60*1000).Scan(&eligible); e != nil {
				return e
			}
			if !eligible {
				return nil
			}
		}
		var busy int
		query := "SELECT count(*) FROM jobs WHERE object_key=? AND state='running'"
		if strings.HasPrefix(scheduleID, "acme:auto:") {
			query = "SELECT count(*) FROM jobs WHERE object_key=? AND (state='running' OR (verb IN ('ssl.issue','ssl.renew') AND state='queued'))"
		}
		if e = tx.QueryRowContext(ctx, query, j.ObjectKey).Scan(&busy); e != nil {
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
