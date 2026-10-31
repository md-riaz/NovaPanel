package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"novapanel/internal/wordpress"
)

// TerminalWordPressConfigs includes acknowledged journals: a reappearing record
// is a conflict, not permission to silently ignore or destroy it.
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

// CheckTerminalWordPressConfig checks terminal evidence and, until durable
// authorization is recorded, the current live resource binding.
func (s *Store) CheckTerminalWordPressConfig(ctx context.Context, jid string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error { _, err := checkTerminalWordPressConfig(ctx, tx, jid); return err })
}

func checkTerminalWordPressConfig(ctx context.Context, tx *sql.Tx, jid string) (Operation, error) {
	op, err := loadOperation(ctx, tx, jid)
	if err != nil {
		return op, err
	}
	if op.Kind != WordPressInstall || op.Checkpoint != "done" || len(op.Nonce) != 0 || len(op.Ciphertext) != 0 {
		return op, ErrSitePrecondition
	}
	plan, err := wordpress.BuildInstall(op.Identity.WordPress.Input, op.Identity.WordPress.Evidence)
	if err != nil {
		return op, ErrSitePrecondition
	}
	var n int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM jobs j WHERE j.id=? AND j.state='done' AND j.step='phase4_committed' AND EXISTS(SELECT 1 FROM events e WHERE e.verb=j.verb AND e.object_key=j.object_key AND e.actor=j.actor AND e.outcome='ok' AND coalesce(e.code,'')='' AND json_extract(e.detail_json,'$.job_id')=j.id AND json_extract(e.detail_json,'$.stage')='exec') AND NOT EXISTS(SELECT 1 FROM job_claims c WHERE c.job_id=j.id AND c.claimed=0 AND (length(c.nonce)=0 OR length(c.ciphertext)=0 OR c.expires=0))`, jid).Scan(&n)
	if err != nil {
		return op, err
	}
	if n != 1 {
		return op, ErrSitePrecondition
	}
	if op.Identity.ConfigCleanup == "" {
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sites s JOIN databases d ON d.id=? JOIN wordpress_bindings w ON w.site_id=s.id AND w.db_id=d.id WHERE s.id=? AND s.name=? AND s.type='wordpress' AND s.docroot=? AND d.site_id=s.id AND d.name=? AND d.primary_user_id=?`, op.DatabaseID, op.SiteID, op.Identity.WordPress.Input.SiteName, plan.Docroot, op.Identity.Database.Name, op.Identity.Database.PrimaryUserID).Scan(&n)
		if err != nil {
			return op, err
		}
		if n != 1 {
			return op, ErrSitePrecondition
		}
	}
	return op, nil
}

// AuthorizeTerminalWordPressConfig must run under the journal lock set before
// unlink. Its persisted evidence survives a crash before filesystem sync/ack.
func (s *Store) AuthorizeTerminalWordPressConfig(ctx context.Context, jid string) error {
	return s.setTerminalWordPressConfig(ctx, jid, false)
}

// AcknowledgeTerminalWordPressConfig is called only after custody directory sync.
func (s *Store) AcknowledgeTerminalWordPressConfig(ctx context.Context, jid string) error {
	return s.setTerminalWordPressConfig(ctx, jid, true)
}

func (s *Store) setTerminalWordPressConfig(ctx context.Context, jid string, acknowledge bool) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		// Acquire write ownership before checking evidence on the same transaction.
		if _, err := tx.ExecContext(ctx, `UPDATE phase4_operations SET updated=updated WHERE job_id=?`, jid); err != nil {
			return err
		}
		op, err := checkTerminalWordPressConfig(ctx, tx, jid)
		if err != nil {
			return err
		}
		state := op.Identity.ConfigCleanup
		if acknowledge {
			if state == "" {
				return ErrSitePrecondition
			}
			op.Identity.ConfigCleanup = "acknowledged"
		} else if state == "" {
			op.Identity.ConfigCleanup = "authorized"
		}
		if state == op.Identity.ConfigCleanup {
			return nil
		}
		raw, err := json.Marshal(op.Identity)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE phase4_operations SET identities_json=? WHERE job_id=?`, string(raw), jid)
		return affected(result, err)
	})
}
