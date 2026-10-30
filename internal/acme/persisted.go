package acme

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"time"
)

// NewPersisted wires mandatory durable callbacks without network I/O. The single
// worker must serialize whole account operations, including across site jobs.
func NewPersisted(ctx context.Context, s *store.Store, cfg Config, now func() int64) (*Client, error) {
	if s == nil || now == nil {
		return nil, errAccount
	}
	if cfg.Environment == "" {
		cfg.Environment = Staging
	}
	name, e := accountName(cfg.Environment)
	if e != nil {
		return nil, e
	}
	if !validDirectory(cfg.Directory) || !validEmail(cfg.Email, cfg.Environment) || cfg.Sealer == nil {
		return nil, errAccount
	}
	sealed, e := s.ACMEAccount(ctx, name)
	if errors.Is(e, sql.ErrNoRows) {
		record, err := NewAccount(cfg.Sealer, cfg.Environment, cfg.Directory, cfg.Email)
		if err != nil {
			return nil, err
		}
		sealed, e = s.CreateACMEAccount(ctx, name, store.ACMESealed(record.Sealed))
	}
	if e != nil {
		return nil, e
	}
	p := Policy{Store: s, Now: now}
	cfg.BeforeOrder = func(o Order) error { return p.BeforeOrder(ctx, o) }
	cfg.FailedOrder = func(o Order) error {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return p.FailedOrder(cleanup, o)
	}
	cfg.SaveAccount = func(r AccountRecord) error {
		if r.Name != name {
			return errAccount
		}
		return s.SaveACMEAccount(ctx, r.Name, store.ACMESealed(r.Sealed))
	}
	return New(cfg, AccountRecord{Name: name, Sealed: secret.Sealed(sealed)})
}
