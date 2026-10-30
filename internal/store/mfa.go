package store

import (
	"context"
	"database/sql"
	"errors"
)

func conflictUnlessChanged(r sql.Result, e error) error {
	e = changed(r, e)
	if errors.Is(e, sql.ErrNoRows) {
		return ErrAuthConflict
	}
	return e
}
func (s *Store) SetPendingMFA(ctx context.Context, userID, sealed string) error {
	if sealed == "" {
		return ErrAuthInvalid
	}
	return conflictUnlessChanged(s.db.ExecContext(ctx, "UPDATE users SET totp_pending=?,totp_pending_at=?,updated=? WHERE id=? AND totp_secret IS NULL AND disabled=0", sealed, s.clock.Now(), s.clock.Now(), userID))
}
func consumeStep(ctx context.Context, tx *sql.Tx, userID string, step, now int64) error {
	current := now / 30000
	if step < current-1 || step > current+1 {
		return ErrAuthExpired
	}
	if _, e := tx.ExecContext(ctx, "DELETE FROM mfa_used WHERE user_id=? AND step_counter<?", userID, current-1); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, "INSERT INTO mfa_used(user_id,step_counter,used_at) VALUES(?,?,?)", userID, step, now)
	return authWriteError(e)
}

// ConfirmMFA promotes exactly the verified pending ciphertext. No hashing or
// decryption occurs inside this short transaction.
func (s *Store) ConfirmMFA(ctx context.Context, userID, expectedPending string, step, pendingTTLMS int64, codes []BackupCode) error {
	if expectedPending == "" || pendingTTLMS <= 0 || pendingTTLMS > 600000 || len(codes) < 8 || len(codes) > 10 {
		return ErrAuthInvalid
	}
	for _, v := range codes {
		if v.ID == "" || v.UserID != userID || v.CodePHC == "" || v.UsedAt != nil {
			return ErrAuthInvalid
		}
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		u, e := scanUser(tx.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE id=?", userID))
		if e != nil {
			return e
		}
		if u.Disabled || u.TOTPSecret != nil || u.TOTPPending == nil || *u.TOTPPending != expectedPending {
			return ErrAuthConflict
		}
		now := s.clock.Now()
		if u.TOTPPendingAt == nil || *u.TOTPPendingAt > now || now-*u.TOTPPendingAt >= pendingTTLMS {
			return ErrAuthExpired
		}
		if e = consumeStep(ctx, tx, userID, step, now); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE users SET totp_secret=totp_pending,totp_pending=NULL,totp_pending_at=NULL,updated=? WHERE id=?", now, userID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM backup_codes WHERE user_id=?", userID); e != nil {
			return e
		}
		for _, v := range codes {
			if _, e = tx.ExecContext(ctx, "INSERT INTO backup_codes(id,user_id,code_phc) VALUES(?,?,?)", v.ID, userID, v.CodePHC); e != nil {
				return authWriteError(e)
			}
		}
		return nil
	})
}
func (s *Store) ConsumeTOTP(ctx context.Context, userID, expectedActive string, step int64) error {
	if expectedActive == "" {
		return ErrAuthInvalid
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var active *string
		var disabled bool
		if e := tx.QueryRowContext(ctx, "SELECT totp_secret,disabled FROM users WHERE id=?", userID).Scan(&active, &disabled); e != nil {
			return e
		}
		if disabled || active == nil || *active != expectedActive {
			return ErrAuthConflict
		}
		return consumeStep(ctx, tx, userID, step, s.clock.Now())
	})
}
func (s *Store) ListBackupCodes(ctx context.Context, userID string) ([]BackupCode, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT id,user_id,code_phc,used_at FROM backup_codes WHERE user_id=? AND used_at IS NULL ORDER BY id", userID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []BackupCode{}
	for rows.Next() {
		var v BackupCode
		if e = rows.Scan(&v.ID, &v.UserID, &v.CodePHC, &v.UsedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) ConsumeBackupCode(ctx context.Context, userID, id, expectedPHC string) error {
	return conflictUnlessChanged(s.db.ExecContext(ctx, `UPDATE backup_codes SET used_at=? WHERE id=? AND user_id=? AND code_phc=? AND used_at IS NULL AND EXISTS(SELECT 1 FROM users WHERE id=? AND disabled=0 AND totp_secret IS NOT NULL)`, s.clock.Now(), id, userID, expectedPHC, userID))
}

// DisableMFA requires the orchestrator to consume a proof first, and CASes the
// verified active ciphertext so concurrent enrollment cannot be cleared.
func (s *Store) DisableMFA(ctx context.Context, userID, expectedActive string) error {
	if expectedActive == "" {
		return ErrAuthInvalid
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if e := conflictUnlessChanged(tx.ExecContext(ctx, "UPDATE users SET totp_secret=NULL,totp_pending=NULL,totp_pending_at=NULL,updated=? WHERE id=? AND totp_secret=? AND disabled=0", s.clock.Now(), userID, expectedActive)); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "DELETE FROM backup_codes WHERE user_id=?", userID)
		return e
	})
}
