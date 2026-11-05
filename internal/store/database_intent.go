package store

import (
	"context"
	"database/sql"
	"errors"
)

// CreateDatabaseIntent commits the job, claim and sealed input before external effects.
func (s *Store) CreateDatabaseIntent(ctx context.Context, j Job, c JobClaim, nonce, ciphertext []byte, d *Database, u *DatabaseUser) error {
	if c.JobID != j.ID || len(nonce) == 0 || len(ciphertext) == 0 {
		return errors.New("invalid database intent")
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if err := insertJob(ctx, tx, j); err != nil {
			return err
		}
		if err := insertClaim(ctx, tx, c); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO secrets(id,name,nonce,ciphertext,created) VALUES(?,?,?,?,?)`, j.ID, "database-job:"+j.ID, nonce, ciphertext, s.clock.Now())
		return err
	})
}

func (s *Store) DatabaseIntentSecret(ctx context.Context, jobID string) (nonce, ciphertext []byte, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT nonce,ciphertext FROM secrets WHERE id=? AND name=?`, jobID, "database-job:"+jobID).Scan(&nonce, &ciphertext)
	return
}
