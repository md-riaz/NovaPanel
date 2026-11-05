package store

import (
	"context"
	"database/sql"
	"errors"
)

var ErrIdempotencyConflict = errors.New("idempotency key reused with different payload")

// ReplayJob reads the admission under the same write lock used by admission and claim consumption.
func (s *Store) ReplayJob(ctx context.Context, owner, key, payload string) (j Job, token []byte, found bool, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE idempotency SET created=created WHERE 0"); e != nil {
			return e
		}
		var e error
		j, token, found, e = s.replayJob(ctx, tx, owner, key, payload)
		return e
	})
	return
}

func (s *Store) replayJob(ctx context.Context, tx *sql.Tx, owner, key, payload string) (j Job, token []byte, found bool, err error) {
	var id, hash string
	err = tx.QueryRowContext(ctx, "SELECT job_id,payload_hash,result_json FROM idempotency WHERE id=? AND created>?", key, s.clock.Now()-24*60*60*1000).Scan(&id, &hash, &token)
	if errors.Is(err, sql.ErrNoRows) {
		return j, nil, false, nil
	}
	if err != nil {
		return
	}
	found = true
	if hash != payload {
		err = ErrIdempotencyConflict
		return
	}
	j, err = scanJob(tx.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE id=? AND created_by=? AND created_by<>''", id, owner))
	if err != nil {
		return
	}
	var live int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM job_claims WHERE job_id=? AND claimed=0 AND
		((length(ciphertext)>0 AND expires>?) OR (length(ciphertext)=0 AND ?))`, id, s.clock.Now(), j.State != "failed" || j.Code == "interrupted").Scan(&live)
	if errors.Is(err, sql.ErrNoRows) {
		token = nil
		_, err = tx.ExecContext(ctx, "UPDATE idempotency SET result_json=NULL WHERE id=?", key)
	}
	return
}

// AdmitJob serializes deduplication with job, claim, and optional sealed database intent.
func (s *Store) AdmitJob(ctx context.Context, j Job, c JobClaim, key, payload string, sealedToken []byte, nonce, ciphertext []byte) (existing Job, token []byte, replay bool, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		// Take a write lock before lookup, including on an absent key.
		if _, e := tx.ExecContext(ctx, "UPDATE idempotency SET created=created WHERE 0"); e != nil {
			return e
		}
		if key != "" {
			var e error
			existing, token, replay, e = s.replayJob(ctx, tx, j.CreatedBy, key, payload)
			if e != nil || replay {
				return e
			}
			if _, e = tx.ExecContext(ctx, "DELETE FROM idempotency WHERE id=?", key); e != nil {
				return e
			}
		}
		if c.JobID != j.ID {
			return errors.New("claim job mismatch")
		}
		if e := insertJob(ctx, tx, j); e != nil {
			return e
		}
		if e := insertClaim(ctx, tx, c); e != nil {
			return e
		}
		if len(nonce) != 0 || len(ciphertext) != 0 {
			if len(nonce) == 0 || len(ciphertext) == 0 {
				return errors.New("invalid database intent")
			}
			if _, e := tx.ExecContext(ctx, "INSERT INTO secrets(id,name,nonce,ciphertext,created) VALUES(?,?,?,?,?)", j.ID, "database-job:"+j.ID, nonce, ciphertext, s.clock.Now()); e != nil {
				return e
			}
		}
		if key != "" {
			_, e := tx.ExecContext(ctx, "INSERT INTO idempotency(id,job_id,result_json,payload_hash,created) VALUES(?,?,?,?,?)", key, j.ID, string(sealedToken), payload, s.clock.Now())
			return e
		}
		return nil
	})
	return
}
