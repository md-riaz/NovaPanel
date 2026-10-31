package store

import (
	"context"
	"database/sql"
)

// ClearSiteCertificates follows certificate file removal during deletion.
func (s *Store) ClearSiteCertificates(ctx context.Context, id string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var status string
		if e := tx.QueryRowContext(ctx, "SELECT status FROM sites WHERE id=?", id).Scan(&status); e != nil {
			return e
		}
		if status != "deleting" {
			return ErrSitePrecondition
		}
		if _, e := tx.ExecContext(ctx, "UPDATE domains SET cert_state='none',cert_staging=0,cert_not_after=NULL WHERE site_id=?", id); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "UPDATE sites SET cert_source='none',force_https=0,hsts_max_age=0 WHERE id=?", id)
		return e
	})
}
