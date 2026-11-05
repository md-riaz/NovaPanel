package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// CommitDatabaseClaim publishes a sealed credential and records the completed
// external effect together, so replay cannot recreate an already-consumed claim.
func (s *Store) CommitDatabaseClaim(ctx context.Context, jobID, verb, objectKey string, result json.RawMessage, nonce, ciphertext []byte) error {
	if len(nonce) == 0 || len(ciphertext) == 0 || !json.Valid(result) {
		return errors.New("invalid database claim")
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var args []byte
		if err := tx.QueryRowContext(ctx, `SELECT args_json FROM jobs WHERE id=? AND verb=? AND object_key=? AND state='running'`, jobID, verb, objectKey).Scan(&args); err != nil {
			return err
		}
		var v struct {
			DBID     string  `json:"db_id"`
			UserID   string  `json:"user_id"`
			SiteID   *string `json:"site_id"`
			Name     string  `json:"name"`
			Username string  `json:"username"`
		}
		if err := json.Unmarshal(args, &v); err != nil {
			return err
		}
		switch verb {
		case "database.create":
			if _, err := tx.ExecContext(ctx, `INSERT INTO databases(id,site_id,name,engine,created) VALUES(?,?,?,'mariadb',?)`, v.DBID, v.SiteID, v.Name, s.clock.Now()); err != nil {
				return siteWriteError(err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO db_users(id,db_id,username,owner,created) VALUES(?,?,?,1,?)`, v.UserID, v.DBID, v.Username, s.clock.Now()); err != nil {
				return siteWriteError(err)
			}
		case "dbuser.create":
			if _, err := tx.ExecContext(ctx, `INSERT INTO db_users(id,db_id,username,owner,created) VALUES(?,?,?,0,?)`, v.UserID, v.DBID, v.Username, s.clock.Now()); err != nil {
				return siteWriteError(err)
			}
		}
		r, err := tx.ExecContext(ctx, `UPDATE jobs SET step='database_committed',result_json=? WHERE id=? AND verb=? AND object_key=? AND state='running' AND step!='database_committed'`, string(result), jobID, verb, objectKey)
		if err = affected(r, err); err != nil {
			return err
		}
		r, err = tx.ExecContext(ctx, `UPDATE job_claims SET nonce=?,ciphertext=?,expires=? WHERE job_id=? AND claimed=0 AND length(ciphertext)=0`, nonce, ciphertext, s.clock.Now()+ClaimTTLMillis, jobID)
		return affected(r, err)
	})
}
