package store

import "context"

// SetSSLAuto changes only the renewal preference, never the certificate source.
// Consumers must require cert_source=letsencrypt independently of this preference.
func (s *Store) SetSSLAuto(ctx context.Context, siteID string, enabled bool) error {
	r, err := s.db.ExecContext(ctx, "UPDATE sites SET ssl_auto=?,updated=? WHERE id=? AND status='active'", enabled, s.clock.Now(), siteID)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrSitePrecondition
	}
	return nil
}
