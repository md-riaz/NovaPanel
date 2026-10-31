package store

import (
	"context"
	"database/sql"
	"novapanel/internal/wordpress"
)

// TerminalWordPressConfigs returns only successful committed operation identities.
// No credential material is projected. Eligibility is rechecked under queue locks.
func (s *Store) TerminalWordPressConfigs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT job_id FROM phase4_operations WHERE kind='wordpress_install' AND checkpoint='done' ORDER BY job_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CheckTerminalWordPressConfig proves the existing terminal transaction and
// successful completion audit before the operation owner destroys file recovery.
func (s *Store) CheckTerminalWordPressConfig(ctx context.Context, jid string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		op, err := loadOperation(ctx, tx, jid)
		if err != nil {
			return err
		}
		if op.Kind != WordPressInstall || op.Checkpoint != "done" || len(op.Nonce) != 0 || len(op.Ciphertext) != 0 {
			return ErrSitePrecondition
		}
		plan, err := wordpress.BuildInstall(op.Identity.WordPress.Input, op.Identity.WordPress.Evidence)
		if err != nil {
			return ErrSitePrecondition
		}
		var n int
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM jobs j JOIN sites s ON s.id=? JOIN databases d ON d.id=? JOIN wordpress_bindings w ON w.site_id=s.id AND w.db_id=d.id WHERE j.id=? AND j.state='done' AND j.step='phase4_committed' AND s.name=? AND s.type='wordpress' AND s.docroot=? AND d.site_id=s.id AND d.name=? AND d.primary_user_id=? AND EXISTS(SELECT 1 FROM events e WHERE e.verb=j.verb AND e.object_key=j.object_key AND e.actor=j.actor AND e.outcome='ok' AND coalesce(e.code,'')='' AND json_extract(e.detail_json,'$.job_id')=j.id AND json_extract(e.detail_json,'$.stage')='exec') AND NOT EXISTS(SELECT 1 FROM job_claims c WHERE c.job_id=j.id AND c.claimed=0 AND (length(c.nonce)=0 OR length(c.ciphertext)=0 OR c.expires=0))`, op.SiteID, op.DatabaseID, jid, op.Identity.WordPress.Input.SiteName, plan.Docroot, op.Identity.Database.Name, op.Identity.Database.PrimaryUserID).Scan(&n)
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrSitePrecondition
		}
		return nil
	})
}
