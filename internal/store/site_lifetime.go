package store

import (
	"context"
	"database/sql"
	"errors"
)

// SiteLifetime identifies the account allocated by its original provisioning job.
type SiteLifetime struct {
	SiteID, Name, Docroot, AllocatingJob string
	UID, GID                             uint32
}

const freshLifetimeWhere = `id=? AND name=? AND docroot=? AND type='wordpress' AND lifetime_state='provisioning' AND EXISTS (SELECT 1 FROM site_account_provenance p WHERE p.site_id=sites.id AND p.account_name=? AND p.uid=? AND p.gid=? AND p.allocating_job=?)`

func lifetimeArgs(l SiteLifetime) []any {
	return []any{l.SiteID, l.Name, l.Docroot, l.Name, l.UID, l.GID, l.AllocatingJob}
}

// CheckFreshSiteLifetime refuses unknown, changed, or previously serving accounts.
func (s *Store) CheckFreshSiteLifetime(ctx context.Context, l SiteLifetime) error {
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM sites WHERE `+freshLifetimeWhere, lifetimeArgs(l)...).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSitePrecondition
	}
	return err
}

// ActivateSiteLifetime records serving disposition before serving is enabled.
func (s *Store) ActivateSiteLifetime(ctx context.Context, l SiteLifetime) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sites SET lifetime_state='served' WHERE `+freshLifetimeWhere, lifetimeArgs(l)...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrSitePrecondition
	}
	return nil
}
