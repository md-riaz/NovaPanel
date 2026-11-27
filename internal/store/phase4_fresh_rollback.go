package store

import (
	"context"
	"database/sql"
)

const FreshWordPressRollbackStep = "wordpress_rollback"

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
		result, err := tx.ExecContext(ctx, `UPDATE sites SET status='failed' WHERE id=? AND name=? AND type='wordpress' AND lifetime_state='provisioning' AND status IN ('creating','failed') AND EXISTS(SELECT 1 FROM site_account_provenance p WHERE p.site_id=sites.id AND p.account_name=sites.name AND p.allocating_job=? AND p.uid=? AND p.gid=?)`, op.SiteID, w.Input.SiteName, jid, w.Input.UID, w.Input.GID)
		if err = affected(result, err); err != nil {
			return err
		}
		result, err = tx.ExecContext(ctx, `UPDATE jobs SET step=?,code=coalesce(nullif(code,''),?),updated=? WHERE id=? AND verb='site.create' AND state IN ('running','queued') AND (step IS NULL OR step='' OR step=?)`, FreshWordPressRollbackStep, code, s.clock.Now(), jid, FreshWordPressRollbackStep)
		return affected(result, err)
	})
}
