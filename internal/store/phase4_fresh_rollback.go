package store

import (
	"context"
	"database/sql"
	"path/filepath"
)

const FreshWordPressRollbackStep = "wordpress_rollback"
const FreshWordPressRollbackIntentStep = "wordpress_rollback_intent"

// PersistFreshWordPressRollbackIntent fences installation before rollback is entered.
func (s *Store) PersistFreshWordPressRollbackIntent(ctx context.Context, jid, code string) error {
	if code == "" {
		return ErrSiteInvalid
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		op, err := loadOperation(ctx, tx, jid)
		if err != nil {
			return err
		}
		if op.Kind != WordPressInstall || op.Identity.WordPress == nil || op.Checkpoint == "done" || op.Checkpoint == "expired" {
			return ErrSitePrecondition
		}
		w := op.Identity.WordPress
		var n int
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sites s JOIN site_account_provenance p ON p.site_id=s.id WHERE s.id=? AND s.name=? AND s.docroot=? AND s.lifetime_state='provisioning' AND s.status IN ('creating','failed') AND p.allocating_job=? AND p.uid=? AND p.gid=?`, op.SiteID, w.Input.SiteName, filepath.Join(w.Input.SitesRoot, w.Input.SiteName, "public"), jid, w.Input.UID, w.Input.GID).Scan(&n)
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrSitePrecondition
		}
		result, err := tx.ExecContext(ctx, `UPDATE jobs SET step=?,code=coalesce(nullif(code,''),?),updated=? WHERE id=? AND verb='site.create' AND state IN ('running','queued') AND (step IS NULL OR step='' OR step=?)`, FreshWordPressRollbackIntentStep, code, s.clock.Now(), jid, FreshWordPressRollbackIntentStep)
		return affected(result, err)
	})
}

// BeginFreshWordPressRollback records the obligation before external teardown.
// The caller holds the operation's full lock set, including the OS-user lock.
func (s *Store) BeginFreshWordPressRollback(ctx context.Context, jid, code string) error {
	if code == "" {
		return ErrSiteInvalid
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		op, err := loadOperation(ctx, tx, jid)
		if err != nil {
			return err
		}
		if op.Kind != WordPressInstall || op.Identity.WordPress == nil || op.Checkpoint == "done" || op.Checkpoint == "expired" {
			return ErrSitePrecondition
		}
		w := op.Identity.WordPress
		result, err := tx.ExecContext(ctx, `UPDATE sites SET status='failed' WHERE id=? AND name=? AND docroot=? AND type='wordpress' AND lifetime_state='provisioning' AND status IN ('creating','failed') AND EXISTS(SELECT 1 FROM site_account_provenance p WHERE p.site_id=sites.id AND p.account_name=sites.name AND p.allocating_job=? AND p.uid=? AND p.gid=?)`, op.SiteID, w.Input.SiteName, filepath.Join(w.Input.SitesRoot, w.Input.SiteName, "public"), jid, w.Input.UID, w.Input.GID)
		if err = affected(result, err); err != nil {
			return err
		}
		result, err = tx.ExecContext(ctx, `UPDATE jobs SET step=?,code=coalesce(nullif(code,''),?),updated=? WHERE id=? AND verb='site.create' AND state IN ('running','queued') AND (step IS NULL OR step='' OR step=? OR step=?)`, FreshWordPressRollbackStep, code, s.clock.Now(), jid, FreshWordPressRollbackStep, FreshWordPressRollbackIntentStep)
		return affected(result, err)
	})
}

const FreshWordPressRollbackCleanedStep = "wordpress_rollback_cleaned"

// MarkFreshWordPressRollbackCleaned follows confirmed external teardown.
func (s *Store) MarkFreshWordPressRollbackCleaned(ctx context.Context, jid string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		op, err := loadOperation(ctx, tx, jid)
		if err != nil {
			return err
		}
		if op.Kind != WordPressInstall || op.Identity.WordPress == nil {
			return ErrSitePrecondition
		}
		w := op.Identity.WordPress
		var n int
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sites s JOIN site_account_provenance p ON p.site_id=s.id WHERE s.id=? AND s.name=? AND s.docroot=? AND s.status='failed' AND s.lifetime_state='provisioning' AND p.allocating_job=? AND p.uid=? AND p.gid=?`, op.SiteID, w.Input.SiteName, filepath.Join(w.Input.SitesRoot, w.Input.SiteName, "public"), jid, w.Input.UID, w.Input.GID).Scan(&n)
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrSitePrecondition
		}
		result, err := tx.ExecContext(ctx, `UPDATE jobs SET step=?,updated=? WHERE id=? AND state='running' AND step=?`, FreshWordPressRollbackCleanedStep, s.clock.Now(), jid, FreshWordPressRollbackStep)
		return affected(result, err)
	})
}

// CompleteFreshWordPressRollback finalizes only after teardown and custody cleanup.
func (s *Store) CompleteFreshWordPressRollback(ctx context.Context, jid string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		op, err := loadOperation(ctx, tx, jid)
		if err != nil {
			return err
		}
		if op.Kind != WordPressInstall || op.Identity.WordPress == nil || op.Checkpoint == "done" || op.Checkpoint == "expired" {
			return ErrSitePrecondition
		}
		w := op.Identity.WordPress
		var n int
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sites s JOIN site_account_provenance p ON p.site_id=s.id JOIN jobs j ON j.id=p.allocating_job WHERE s.id=? AND s.name=? AND s.docroot=? AND s.status='failed' AND s.lifetime_state='provisioning' AND p.allocating_job=? AND p.uid=? AND p.gid=? AND j.state='running' AND j.step=?`, op.SiteID, w.Input.SiteName, filepath.Join(w.Input.SitesRoot, w.Input.SiteName, "public"), jid, w.Input.UID, w.Input.GID, FreshWordPressRollbackCleanedStep).Scan(&n)
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrSitePrecondition
		}
		now := s.clock.Now()
		if _, err = tx.ExecContext(ctx, `UPDATE idempotency SET result_json=NULL WHERE job_id=?`, jid); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM job_claims WHERE job_id=?`, jid); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, phpLimitsKey(op.SiteID)); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE phase4_operations SET checkpoint='expired',reconciled=1,nonce=NULL,ciphertext=NULL,updated=? WHERE job_id=? AND checkpoint NOT IN ('done','expired')`, now, jid)
		if err = affected(result, err); err != nil {
			return err
		}
		result, err = tx.ExecContext(ctx, `DELETE FROM sites WHERE id=? AND status='failed' AND lifetime_state='provisioning'`, op.SiteID)
		if err = affected(result, err); err != nil {
			return err
		}
		result, err = tx.ExecContext(ctx, `UPDATE jobs SET state='failed',error='job execution failed',updated=?,finished=? WHERE id=? AND state='running' AND step=?`, now, now, jid, FreshWordPressRollbackCleanedStep)
		return affected(result, err)
	})
}
