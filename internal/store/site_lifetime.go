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

const siteUIDMin = 2000000
const siteUIDMax = 2999999

// ReserveSiteAccountTx records the next identity after its site and job exist.
// The immutable provenance row retains every allocation, including failed jobs.
func (s *Store) ReserveSiteAccountTx(ctx context.Context, tx *sql.Tx, siteID, name, jobID string) (uint32, error) {
	var next int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(uid), ?-1)+1 FROM site_account_provenance WHERE uid BETWEEN ? AND ?`, siteUIDMin, siteUIDMin, siteUIDMax).Scan(&next); err != nil {
		return 0, err
	}
	if next > siteUIDMax {
		return 0, ErrSitePrecondition
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO site_account_provenance(site_id,account_name,uid,gid,allocating_job,evidence) VALUES(?,?,?,?,?,'reserved')`, siteID, name, next, next, jobID)
	if err != nil {
		return 0, err
	}
	return uint32(next), nil
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

// LoadSiteLifetime reloads the durable account identity for an interrupted job.
func (s *Store) LoadSiteLifetime(ctx context.Context, siteID string) (SiteLifetime, error) {
	var l SiteLifetime
	err := s.db.QueryRowContext(ctx, `SELECT s.id, s.name, s.docroot, p.allocating_job, p.uid, p.gid FROM sites s JOIN site_account_provenance p ON p.site_id=s.id AND p.account_name=s.name WHERE s.id=?`, siteID).Scan(&l.SiteID, &l.Name, &l.Docroot, &l.AllocatingJob, &l.UID, &l.GID)
	return l, err
}

// CheckActivatedSiteLifetime accepts only the original unfinished install job.
func (s *Store) CheckActivatedSiteLifetime(ctx context.Context, l SiteLifetime, jobID string) error {
	if l.AllocatingJob != jobID {
		return ErrSitePrecondition
	}
	var found int
	args := append(lifetimeArgs(l), jobID)
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM sites WHERE id=? AND name=? AND docroot=? AND type='wordpress' AND lifetime_state='served' AND EXISTS (SELECT 1 FROM site_account_provenance p WHERE p.site_id=sites.id AND p.account_name=? AND p.uid=? AND p.gid=? AND p.allocating_job=?) AND EXISTS (SELECT 1 FROM phase4_operations o JOIN jobs j ON j.id=o.job_id WHERE o.job_id=? AND o.site_id=sites.id AND o.kind='wordpress_install' AND o.reconciled=0 AND o.checkpoint NOT IN ('done','expired') AND j.state IN ('queued','running'))`, args...).Scan(&found)
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
	freshCheckpointTestHook(ctx, l.AllocatingJob, "activated")
	return nil
}
