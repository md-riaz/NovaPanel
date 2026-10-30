package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

const tokenColumns = "id,name,secret_phc,scopes,automation,created_by,created,expires,revoked,last_used"

func scanToken(r scanner) (v APIToken, e error) {
	var scopes string
	e = r.Scan(&v.ID, &v.Name, &v.SecretPHC, &scopes, &v.Automation, &v.CreatedBy, &v.Created, &v.Expires, &v.Revoked, &v.LastUsed)
	if e == nil {
		e = json.Unmarshal([]byte(scopes), &v.Scopes)
	}
	return
}
func (s *Store) CreateAPIToken(ctx context.Context, v APIToken) error {
	if v.ID == "" || !validAuthText(v.Name) || v.SecretPHC == "" || v.CreatedBy == "" || v.Revoked || v.LastUsed != nil {
		return ErrAuthInvalid
	}
	if v.Scopes == nil {
		v.Scopes = []string{}
	}
	scopes, e := json.Marshal(v.Scopes)
	if e != nil {
		return e
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		now := s.clock.Now()
		if v.Expires != nil && *v.Expires <= now {
			return ErrAuthExpired
		}
		var disabled bool
		if e := tx.QueryRowContext(ctx, "SELECT disabled FROM users WHERE id=?", v.CreatedBy).Scan(&disabled); e != nil {
			return e
		}
		if disabled {
			return ErrAuthConflict
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO api_tokens("+tokenColumns+") VALUES(?,?,?,?,?,?,?,?,?,?)", v.ID, v.Name, v.SecretPHC, string(scopes), v.Automation, v.CreatedBy, now, v.Expires, false, nil)
		return authWriteError(e)
	})
}
func validToken(v APIToken, disabled bool, now int64) error {
	if v.Revoked || disabled {
		return ErrAuthConflict
	}
	if v.Expires != nil && now >= *v.Expires {
		return ErrAuthExpired
	}
	return nil
}
func (s *Store) GetAPIToken(ctx context.Context, id string) (v APIToken, e error) {
	e = s.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		v, err = scanToken(tx.QueryRowContext(ctx, "SELECT "+tokenColumns+" FROM api_tokens WHERE id=?", id))
		if err != nil {
			return err
		}
		var disabled bool
		if err = tx.QueryRowContext(ctx, "SELECT disabled FROM users WHERE id=?", v.CreatedBy).Scan(&disabled); err != nil {
			return err
		}
		return validToken(v, disabled, s.clock.Now())
	})
	return
}
func (s *Store) TouchAPIToken(ctx context.Context, id, expectedPHC string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		v, e := scanToken(tx.QueryRowContext(ctx, "SELECT "+tokenColumns+" FROM api_tokens WHERE id=?", id))
		if e != nil {
			return e
		}
		if v.SecretPHC != expectedPHC {
			return ErrAuthConflict
		}
		var disabled bool
		if e = tx.QueryRowContext(ctx, "SELECT disabled FROM users WHERE id=?", v.CreatedBy).Scan(&disabled); e != nil {
			return e
		}
		now := s.clock.Now()
		if e = validToken(v, disabled, now); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE api_tokens SET last_used=? WHERE id=?", now, id)
		return e
	})
}
func (s *Store) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT "+tokenColumns+" FROM api_tokens ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		v, e := scanToken(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) RevokeAPIToken(ctx context.Context, id string) error {
	return changed(s.db.ExecContext(ctx, "UPDATE api_tokens SET revoked=1 WHERE id=?", id))
}
