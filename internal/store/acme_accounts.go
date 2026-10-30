package store

import (
	"context"
	"database/sql"
)

type ACMESealed struct {
	Nonce      []byte
	Ciphertext []byte
}

func validACMEName(name string) bool {
	return name == "acme:account:staging" || name == "acme:account:prod"
}
func validACMESealed(v ACMESealed) bool {
	return len(v.Nonce) == 12 && len(v.Ciphertext) >= 16 && len(v.Ciphertext) <= 64<<10
}
func (s *Store) ACMEAccount(ctx context.Context, name string) (ACMESealed, error) {
	var v ACMESealed
	if !validACMEName(name) {
		return v, ErrACMEPolicy
	}
	e := s.db.QueryRowContext(ctx, "SELECT nonce,ciphertext FROM secrets WHERE name=?", name).Scan(&v.Nonce, &v.Ciphertext)
	if e == nil && !validACMESealed(v) {
		return ACMESealed{}, ErrACMEPolicy
	}
	return v, e
}

// CreateACMEAccount never replaces a previously persisted account key.
func (s *Store) CreateACMEAccount(ctx context.Context, name string, v ACMESealed) (ACMESealed, error) {
	if !validACMEName(name) || !validACMESealed(v) {
		return ACMESealed{}, ErrACMEPolicy
	}
	var out ACMESealed
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "INSERT INTO secrets(id,name,nonce,ciphertext,created) VALUES(?,?,?,?,?) ON CONFLICT(name) DO NOTHING", name, name, v.Nonce, v.Ciphertext, s.clock.Now()); e != nil {
			return e
		}
		return tx.QueryRowContext(ctx, "SELECT nonce,ciphertext FROM secrets WHERE name=?", name).Scan(&out.Nonce, &out.Ciphertext)
	})
	return out, e
}
func (s *Store) SaveACMEAccount(ctx context.Context, name string, v ACMESealed) error {
	if !validACMEName(name) || !validACMESealed(v) {
		return ErrACMEPolicy
	}
	r, e := s.db.ExecContext(ctx, "UPDATE secrets SET nonce=?,ciphertext=? WHERE name=?", v.Nonce, v.Ciphertext, name)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e == nil && n != 1 {
		return sql.ErrNoRows
	}
	return e
}
