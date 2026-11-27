package store

import (
	"context"
	"database/sql"
)

// PendingOperations returns startup-blocking journals; fresh installs resume in the worker.
// Unknown journal kinds fail when the queue loads their typed identities.
func (s *Store) PendingOperations(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE EXISTS(SELECT 1 FROM phase4_operations o WHERE o.job_id=jobs.id AND o.checkpoint NOT IN ('done','expired') AND NOT(o.kind='wordpress_install' AND EXISTS (SELECT 1 FROM sites WHERE sites.id=o.site_id AND sites.lifetime_state='provisioning'))) ORDER BY created,id LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// PrepareExpiredPHPRestoration requeues interrupted PHP journals for committed-state restoration.
func (s *Store) PrepareExpiredPHPRestoration(ctx context.Context) ([]Job, error) {
	var jobs []Job
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		now := s.clock.Now()
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET state='queued',code=NULL,error=NULL,finished=NULL,updated=? WHERE (state='running' OR (state='failed' AND code='interrupted')) AND EXISTS (SELECT 1 FROM phase4_operations o WHERE o.job_id=jobs.id AND o.kind='php_set' AND o.reconciled=0 AND o.checkpoint IN ('prepared','config_published','verified') AND o.expires<=?)`, now, now)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE state='queued' AND EXISTS (SELECT 1 FROM phase4_operations o WHERE o.job_id=jobs.id AND o.kind='php_set' AND o.reconciled=0 AND o.checkpoint IN ('prepared','config_published','verified') AND o.expires<=?) ORDER BY created,id", now)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			j, err := scanJob(rows)
			if err != nil {
				return err
			}
			jobs = append(jobs, j)
		}
		return rows.Err()
	})
	return jobs, err
}
