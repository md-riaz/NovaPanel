package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

var (
	ErrAuthConflict = errors.New("auth state conflict")
	ErrAuthExpired  = errors.New("auth state expired")
	ErrAuthInvalid  = errors.New("invalid auth operation")
)

type User struct {
	ID, Username, Email, PWPHC, Role string
	TOTPSecret, TOTPPending          *string
	TOTPPendingAt                    *int64
	Disabled                         bool
	Created, Updated                 int64
}
type UserPatch struct {
	Email, Role *string
	Disabled    *bool
}
type Session struct {
	ID, TokenHash, Kind                          string
	UserID, Role                                 *string
	CSRFToken                                    string
	MFAAt                                        *int64
	Created, LastSeen, IdleDeadline, AbsDeadline int64
}
type APIToken struct {
	ID, Name, SecretPHC string
	Scopes              []string
	Automation          bool
	CreatedBy           string
	Created             int64
	Expires             *int64
	Revoked             bool
	LastUsed            *int64
}
type BackupCode struct {
	ID, UserID, CodePHC string
	UsedAt              *int64
}
type MFAUse struct {
	UserID              string
	StepCounter, UsedAt int64
}

const userColumns = "id,username,email,pw_phc,role,totp_secret,totp_pending,totp_pending_at,disabled,created,updated"

func scanUser(r scanner) (u User, e error) {
	e = r.Scan(&u.ID, &u.Username, &u.Email, &u.PWPHC, &u.Role, &u.TOTPSecret, &u.TOTPPending, &u.TOTPPendingAt, &u.Disabled, &u.Created, &u.Updated)
	return
}
func authWriteError(e error) error {
	// SQLite constraint errors retain their numeric driver code without exposing SQL to callers.
	var c interface{ Code() int }
	if errors.As(e, &c) && c.Code()&255 == 19 {
		return ErrAuthConflict
	}
	return e
}
func changed(r sql.Result, e error) error {
	if e != nil {
		return authWriteError(e)
	}
	n, e := r.RowsAffected()
	if e == nil && n == 0 {
		return sql.ErrNoRows
	}
	return e
}
func (s *Store) CreateUser(ctx context.Context, u User) error {
	if u.ID == "" || u.Username == "" || u.PWPHC == "" || u.TOTPSecret != nil || u.TOTPPending != nil || u.TOTPPendingAt != nil {
		return ErrAuthInvalid
	}
	now := s.clock.Now()
	_, e := s.db.ExecContext(ctx, "INSERT INTO users(id,username,email,pw_phc,role,disabled,created,updated) VALUES(?,?,?,?,?,?,?,?)", u.ID, u.Username, u.Email, u.PWPHC, u.Role, u.Disabled, now, now)
	return authWriteError(e)
}
func (s *Store) UserByName(ctx context.Context, name string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE username=?", name))
}
func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE id=?", id))
}
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT "+userColumns+" FROM users ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, e := scanUser(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
func (s *Store) UpdateUser(ctx context.Context, id string, p UserPatch) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		u, e := scanUser(tx.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE id=?", id))
		if e != nil {
			return e
		}
		oldRole, oldDisabled := u.Role, u.Disabled
		if p.Email != nil {
			u.Email = *p.Email
		}
		if p.Role != nil {
			u.Role = *p.Role
		}
		if p.Disabled != nil {
			u.Disabled = *p.Disabled
		}
		if e = changed(tx.ExecContext(ctx, "UPDATE users SET email=?,role=?,disabled=?,updated=? WHERE id=?", u.Email, u.Role, u.Disabled, s.clock.Now(), id)); e != nil {
			return e
		}
		if oldRole != u.Role || oldDisabled != u.Disabled {
			_, e = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", id)
		}
		return e
	})
}
func (s *Store) SetPassword(ctx context.Context, id, phc string) error {
	if phc == "" {
		return ErrAuthInvalid
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if e := changed(tx.ExecContext(ctx, "UPDATE users SET pw_phc=?,updated=? WHERE id=?", phc, s.clock.Now(), id)); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", id)
		return e
	})
}
func (s *Store) RehashPassword(ctx context.Context, id, oldPHC, newPHC string) error {
	if newPHC == "" {
		return ErrAuthInvalid
	}
	e := changed(s.db.ExecContext(ctx, "UPDATE users SET pw_phc=?,updated=? WHERE id=? AND pw_phc=?", newPHC, s.clock.Now(), id, oldPHC))
	if errors.Is(e, sql.ErrNoRows) {
		return ErrAuthConflict
	}
	return e
}
func (s *Store) DeleteUser(ctx context.Context, id string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "DELETE FROM api_tokens WHERE created_by=?", id); e != nil {
			return e
		}
		return changed(tx.ExecContext(ctx, "DELETE FROM users WHERE id=?", id))
	})
}

const sessionColumns = "id,token_hash,kind,user_id,role,csrf_token,mfa_at,created,last_seen,idle_deadline,abs_deadline"

func scanSession(r scanner) (v Session, e error) {
	e = r.Scan(&v.ID, &v.TokenHash, &v.Kind, &v.UserID, &v.Role, &v.CSRFToken, &v.MFAAt, &v.Created, &v.LastSeen, &v.IdleDeadline, &v.AbsDeadline)
	return
}
func insertSession(ctx context.Context, db execer, v Session, now int64) error {
	if v.ID == "" || v.TokenHash == "" || v.CSRFToken == "" || v.IdleDeadline <= now || v.AbsDeadline <= now || v.IdleDeadline > v.AbsDeadline {
		return ErrAuthInvalid
	}
	if v.Kind == "anon" {
		if v.UserID != nil || v.Role != nil || v.MFAAt != nil {
			return ErrAuthInvalid
		}
	} else if v.Kind == "user" {
		if v.UserID == nil || v.Role == nil {
			return ErrAuthInvalid
		}
	} else {
		return ErrAuthInvalid
	}
	_, e := db.ExecContext(ctx, "INSERT INTO sessions("+sessionColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?)", v.ID, v.TokenHash, v.Kind, v.UserID, v.Role, v.CSRFToken, v.MFAAt, now, now, v.IdleDeadline, v.AbsDeadline)
	return authWriteError(e)
}

const sessionExpiryDelete = `WITH idle AS (
  SELECT id FROM sessions INDEXED BY idx_sessions_idle_deadline_id
  WHERE idle_deadline < ? ORDER BY idle_deadline, id LIMIT 256
), absolute AS (
  SELECT id FROM sessions INDEXED BY idx_sessions_abs_deadline_id
  WHERE abs_deadline < ? ORDER BY abs_deadline, id LIMIT 256
), doomed AS (
  SELECT id FROM (SELECT id FROM idle UNION SELECT id FROM absolute)
  ORDER BY id LIMIT 256
)
DELETE FROM sessions WHERE id IN (SELECT id FROM doomed)
AND (idle_deadline < ? OR abs_deadline < ?);`

func (s *Store) CreateSession(ctx context.Context, v Session) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		// Take the write lock before sampling time, including after a pool or SQLite wait.
		if _, e := tx.ExecContext(ctx, "UPDATE sessions SET last_seen=last_seen WHERE 0"); e != nil {
			return e
		}
		now := s.clock.Now()
		if e := sessionUser(ctx, tx, v); e != nil {
			return e
		}
		if v.Kind == "anon" {
			if _, e := tx.ExecContext(ctx, sessionExpiryDelete, now, now, now, now); e != nil {
				return e
			}
		}
		return insertSession(ctx, tx, v, now)
	})
}
func sessionUser(ctx context.Context, tx *sql.Tx, v Session) error {
	if v.Kind != "user" {
		return nil
	}
	if v.UserID == nil || v.Role == nil {
		return ErrAuthInvalid
	}
	var role string
	var disabled bool
	if e := tx.QueryRowContext(ctx, "SELECT role,disabled FROM users WHERE id=?", *v.UserID).Scan(&role, &disabled); e != nil {
		return e
	}
	if disabled || role != *v.Role {
		return ErrAuthConflict
	}
	return nil
}
func validSession(v Session, now int64) error {
	if now >= v.IdleDeadline || now >= v.AbsDeadline {
		return ErrAuthExpired
	}
	return nil
}
func (s *Store) GetSession(ctx context.Context, id string) (Session, error) {
	v, e := scanSession(s.db.QueryRowContext(ctx, "SELECT "+sessionColumns+" FROM sessions WHERE id=?", id))
	if e == nil {
		e = validSession(v, s.clock.Now())
	}
	return v, e
}
func (s *Store) TouchSession(ctx context.Context, id, expectedHash string, idleMS int64) (v Session, e error) {
	if idleMS <= 0 {
		return v, ErrAuthInvalid
	}
	e = s.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		v, err = scanSession(tx.QueryRowContext(ctx, "SELECT "+sessionColumns+" FROM sessions WHERE id=?", id))
		if err != nil {
			return err
		}
		if v.TokenHash != expectedHash {
			return ErrAuthConflict
		}
		now := s.clock.Now()
		if err = validSession(v, now); err != nil {
			return err
		}
		v.LastSeen = now
		v.IdleDeadline = v.AbsDeadline
		if idleMS < v.AbsDeadline-now {
			v.IdleDeadline = now + idleMS
		}
		_, err = tx.ExecContext(ctx, "UPDATE sessions SET last_seen=?,idle_deadline=? WHERE id=?", v.LastSeen, v.IdleDeadline, id)
		return err
	})
	return
}
func (s *Store) RotateSession(ctx context.Context, oldID string, v Session) error {
	return s.rotateSession(ctx, oldID, v, nil, "")
}

// RotateVerifiedSession binds publication to the credentials that were verified.
// Optional password rehashing commits with rotation, never before the state check.
func (s *Store) RotateVerifiedSession(ctx context.Context, oldID string, v Session, verified User, newPHC string) error {
	if v.Kind != "user" || v.UserID == nil || *v.UserID != verified.ID || verified.PWPHC == "" {
		return ErrAuthInvalid
	}
	return s.rotateSession(ctx, oldID, v, &verified, newPHC)
}

func (s *Store) rotateSession(ctx context.Context, oldID string, v Session, verified *User, newPHC string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		old, e := scanSession(tx.QueryRowContext(ctx, "SELECT "+sessionColumns+" FROM sessions WHERE id=?", oldID))
		if e != nil {
			return e
		}
		now := s.clock.Now()
		if e = validSession(old, now); e != nil {
			return e
		}
		if old.ID == v.ID {
			return ErrAuthInvalid
		}
		if old.Kind == "user" && (v.Kind != "user" || old.UserID == nil || v.UserID == nil || *old.UserID != *v.UserID || v.AbsDeadline > old.AbsDeadline) {
			return ErrAuthInvalid
		}
		if e = sessionUser(ctx, tx, v); e != nil {
			return e
		}
		if verified != nil {
			var matches bool
			if e = tx.QueryRowContext(ctx, "SELECT pw_phc=? AND totp_secret IS ? FROM users WHERE id=?", verified.PWPHC, verified.TOTPSecret, verified.ID).Scan(&matches); e != nil {
				return e
			}
			if !matches {
				return ErrAuthConflict
			}
			if newPHC != "" {
				if _, e = tx.ExecContext(ctx, "UPDATE users SET pw_phc=?,updated=? WHERE id=?", newPHC, now, verified.ID); e != nil {
					return e
				}
			}
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM sessions WHERE id=?", oldID); e != nil {
			return e
		}
		return insertSession(ctx, tx, v, now)
	})
}
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, e := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id=?", id)
	return e
}

// Keep strings dependency local to input validation used by token persistence.
func validAuthText(v string) bool { return strings.TrimSpace(v) != "" }
