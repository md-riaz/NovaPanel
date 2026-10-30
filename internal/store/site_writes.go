package store

import (
	"context"
	"database/sql"
	"errors"
)

var (
	ErrSiteInvalid      = errors.New("invalid site metadata")
	ErrSiteConflict     = errors.New("site metadata conflict")
	ErrSitePrecondition = errors.New("site precondition failed")
)

type SitePatch struct {
	PHPVersion, ConfigHash, PrimaryDomainID *string
	ForceHTTPS, Maintenance, SSLAuto        *bool
	HSTSMaxAge                              *int64
}

// UpdateSite stores desired metadata. Certificate validity is checked at the
// execution sink, not inferred from database certificate metadata.
func (s *Store) UpdateSite(ctx context.Context, id string, p SitePatch) error {
	if p.HSTSMaxAge != nil && *p.HSTSMaxAge < 0 {
		return ErrSiteInvalid
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var intents int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM settings WHERE key=?", tlsIntentKey).Scan(&intents); err != nil {
			return err
		}
		if intents != 0 {
			return ErrSitePrecondition
		}
		if p.PrimaryDomainID != nil {
			var status, source string
			var force bool
			var hsts int64
			if err := tx.QueryRowContext(ctx, "SELECT status,cert_source,force_https,hsts_max_age FROM sites WHERE id=?", id).Scan(&status, &source, &force, &hsts); err != nil {
				return err
			}
			var count, primaries, targets, covered int
			if err := tx.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(is_primary),0),COALESCE(sum(id=?),0),COALESCE(sum(cert_state!='none'),0) FROM domains WHERE site_id=?", *p.PrimaryDomainID, id).Scan(&count, &primaries, &targets, &covered); err != nil {
				return err
			}
			if status != "active" || count < 1 || primaries != 1 || targets != 1 {
				return ErrSitePrecondition
			}
			var primary string
			if err := tx.QueryRowContext(ctx, "SELECT id FROM domains WHERE site_id=? AND is_primary=1", id).Scan(&primary); err != nil {
				return err
			}
			if primary != *p.PrimaryDomainID {
				if source != "none" || force || hsts != 0 || covered != 0 {
					return ErrSitePrecondition
				}
				if _, err := tx.ExecContext(ctx, "UPDATE domains SET is_primary=(id=?) WHERE site_id=?", *p.PrimaryDomainID, id); err != nil {
					return err
				}
			} else if p.PHPVersion == nil && p.ConfigHash == nil && p.ForceHTTPS == nil && p.Maintenance == nil && p.SSLAuto == nil && p.HSTSMaxAge == nil {
				return nil
			}
		}
		r, e := tx.ExecContext(ctx, `UPDATE sites SET php_version=COALESCE(?,php_version),config_hash=COALESCE(?,config_hash),force_https=COALESCE(?,force_https),maintenance=COALESCE(?,maintenance),ssl_auto=COALESCE(?,ssl_auto),hsts_max_age=COALESCE(?,hsts_max_age),updated=? WHERE id=?`, p.PHPVersion, p.ConfigHash, p.ForceHTTPS, p.Maintenance, p.SSLAuto, p.HSTSMaxAge, s.clock.Now(), id)
		return siteChanged(r, e)
	})
}

func siteChanged(r sql.Result, e error) error {
	if e != nil {
		return siteWriteError(e)
	}
	n, e := r.RowsAffected()
	if e == nil && n == 0 {
		return sql.ErrNoRows
	}
	return e
}

func (s *Store) AddDomain(ctx context.Context, v Domain) (out Domain, err error) {
	if v.ID == "" || v.SiteID == "" || v.FQDN == "" {
		return out, ErrSiteInvalid
	}
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		old, e := scanDomain(tx.QueryRowContext(ctx, "SELECT "+domainColumns+" FROM domains WHERE fqdn=?", v.FQDN))
		if e == nil {
			if old.SiteID != v.SiteID {
				return ErrSiteConflict
			}
			out = old
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var status string
		if e = tx.QueryRowContext(ctx, "SELECT status FROM sites WHERE id=?", v.SiteID).Scan(&status); e != nil {
			return e
		}
		if status == "deleting" {
			return ErrSitePrecondition
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO domains(id,site_id,fqdn,created) VALUES(?,?,?,?)", v.ID, v.SiteID, v.FQDN, s.clock.Now()); e != nil {
			return siteWriteError(e)
		}
		out, e = scanDomain(tx.QueryRowContext(ctx, "SELECT "+domainColumns+" FROM domains WHERE id=?", v.ID))
		return e
	})
	return
}

func (s *Store) RemoveDomain(ctx context.Context, id string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		d, e := scanDomain(tx.QueryRowContext(ctx, "SELECT "+domainColumns+" FROM domains WHERE id=?", id))
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		var count int
		if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM domains WHERE site_id=?", d.SiteID).Scan(&count); e != nil {
			return e
		}
		if d.IsPrimary || count <= 1 {
			return ErrSitePrecondition
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM domains WHERE id=?", id)
		return e
	})
}

func (s *Store) AddRedirect(ctx context.Context, v Redirect) (out Redirect, err error) {
	if v.ID == "" || v.SiteID == "" || v.Source == "" || v.Target == "" || (v.Code != 301 && v.Code != 302) {
		return out, ErrSiteInvalid
	}
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		old, e := scanRedirect(tx.QueryRowContext(ctx, "SELECT "+redirectColumns+" FROM redirects WHERE id=?", v.ID))
		if e == nil {
			if old != v {
				return ErrSiteConflict
			}
			out = old
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var status string
		if e = tx.QueryRowContext(ctx, "SELECT status FROM sites WHERE id=?", v.SiteID).Scan(&status); e != nil {
			return e
		}
		if status == "deleting" {
			return ErrSitePrecondition
		}
		old, e = scanRedirect(tx.QueryRowContext(ctx, "SELECT "+redirectColumns+" FROM redirects WHERE site_id=? AND source=?", v.SiteID, v.Source))
		if e == nil {
			if old.Target != v.Target || old.Code != v.Code {
				return ErrSiteConflict
			}
			out = old
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO redirects(id,site_id,source,target,code) VALUES(?,?,?,?,?)", v.ID, v.SiteID, v.Source, v.Target, v.Code)
		out = v
		return siteWriteError(e)
	})
	return
}

func (s *Store) RemoveRedirect(ctx context.Context, id string) error {
	_, e := s.db.ExecContext(ctx, "DELETE FROM redirects WHERE id=?", id)
	return e
}

// DeleteSite is metadata-only and must follow successful external cleanup.
func (s *Store) DeleteSite(ctx context.Context, id string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var status string
		e := tx.QueryRowContext(ctx, "SELECT status FROM sites WHERE id=?", id).Scan(&status)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if status != "deleting" {
			return ErrSitePrecondition
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM sites WHERE id=?", id)
		return e
	})
}

func siteWriteError(err error) error {
	var c interface{ Code() int }
	if errors.As(err, &c) && c.Code()&255 == 19 {
		return ErrSiteConflict
	}
	return err
}

// CreateSite publishes desired state and its primary domain atomically. IDs are
// assigned by Run before enqueue; matching retries retain the original IDs.
func (s *Store) CreateSite(ctx context.Context, v Site, d Domain) (out Site, err error) {
	if v.ID == "" || v.Name == "" || v.Type == "" || v.Docroot == "" || d.ID == "" || d.FQDN == "" {
		return out, ErrSiteInvalid
	}
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		old, e := scanSite(tx.QueryRowContext(ctx, "SELECT "+siteColumns+" FROM sites WHERE name=?", v.Name))
		if e == nil {
			if old.Status == "deleting" {
				return ErrSitePrecondition
			}
			var primary string
			if e = tx.QueryRowContext(ctx, "SELECT fqdn FROM domains WHERE site_id=? AND is_primary=1", old.ID).Scan(&primary); e != nil {
				return e
			}
			if old.Type != v.Type || old.Docroot != v.Docroot || primary != d.FQDN || (old.PHPVersion == nil) != (v.PHPVersion == nil) || (old.PHPVersion != nil && v.PHPVersion != nil && *old.PHPVersion != *v.PHPVersion) {
				return ErrSiteConflict
			}
			out = old
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		now := s.clock.Now()
		if _, e = tx.ExecContext(ctx, "INSERT INTO sites(id,name,type,docroot,php_version,created,updated) VALUES(?,?,?,?,?,?,?)", v.ID, v.Name, v.Type, v.Docroot, v.PHPVersion, now, now); e != nil {
			return siteWriteError(e)
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO domains(id,site_id,fqdn,is_primary,created) VALUES(?,?,?,1,?)", d.ID, v.ID, d.FQDN, now); e != nil {
			return siteWriteError(e)
		}
		out, e = scanSite(tx.QueryRowContext(ctx, "SELECT "+siteColumns+" FROM sites WHERE id=?", v.ID))
		return e
	})
	return
}

func (s *Store) SetSiteStatus(ctx context.Context, id, status string) error {
	switch status {
	case "creating", "active", "failed", "deleting":
	default:
		return ErrSiteInvalid
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		r, e := tx.ExecContext(ctx, "UPDATE sites SET status=?,updated=? WHERE id=?", status, s.clock.Now(), id)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e == nil && n == 0 {
			return sql.ErrNoRows
		}
		return e
	})
}
