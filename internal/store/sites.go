package store

import "context"

// Site is persisted desired state, not a runtime status or an RPC response.
type Site struct {
	ID, Name, Type, StackDriver, Docroot string
	PHPVersion                           *string
	ForceHTTPS                           bool
	HSTSMaxAge                           int64
	CertSource                           string
	SSLAuto, Maintenance                 bool
	ConfigHash                           *string
	Created, Updated                     int64
}

// Domain records persisted certificate metadata, not proof of a valid installed cert.
type Domain struct {
	ID, SiteID, FQDN string
	IsPrimary        bool
	CertState        string
	CertStaging      bool
	CertNotAfter     *int64
	Created          int64
}

const domainColumns = "id,site_id,fqdn,is_primary,cert_state,cert_staging,cert_not_after,created"

func scanDomain(r scanner) (v Domain, err error) {
	err = r.Scan(&v.ID, &v.SiteID, &v.FQDN, &v.IsPrimary, &v.CertState, &v.CertStaging, &v.CertNotAfter, &v.Created)
	return
}

func (s *Store) DomainByID(ctx context.Context, id string) (Domain, error) {
	return scanDomain(s.db.QueryRowContext(ctx, "SELECT "+domainColumns+" FROM domains WHERE id=?", id))
}

// ListDomains returns an empty slice when the site has no domains or does not exist.
func (s *Store) ListDomains(ctx context.Context, siteID string) ([]Domain, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+domainColumns+" FROM domains WHERE site_id=? ORDER BY id", siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		v, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type Redirect struct {
	ID, SiteID, Source, Target string
	Code                       int
}

const redirectColumns = "id,site_id,source,target,code"

func scanRedirect(r scanner) (v Redirect, err error) {
	err = r.Scan(&v.ID, &v.SiteID, &v.Source, &v.Target, &v.Code)
	return
}

func (s *Store) RedirectByID(ctx context.Context, id string) (Redirect, error) {
	return scanRedirect(s.db.QueryRowContext(ctx, "SELECT "+redirectColumns+" FROM redirects WHERE id=?", id))
}

// ListRedirects returns an empty slice when the site has no redirects or does not exist.
func (s *Store) ListRedirects(ctx context.Context, siteID string) ([]Redirect, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+redirectColumns+" FROM redirects WHERE site_id=? ORDER BY id", siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Redirect{}
	for rows.Next() {
		v, err := scanRedirect(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

const siteColumns = "id,name,type,stack_driver,docroot,php_version,force_https,hsts_max_age,cert_source,ssl_auto,maintenance,config_hash,created,updated"

func scanSite(r scanner) (v Site, err error) {
	err = r.Scan(&v.ID, &v.Name, &v.Type, &v.StackDriver, &v.Docroot, &v.PHPVersion, &v.ForceHTTPS, &v.HSTSMaxAge, &v.CertSource, &v.SSLAuto, &v.Maintenance, &v.ConfigHash, &v.Created, &v.Updated)
	return
}

func (s *Store) SiteByID(ctx context.Context, id string) (Site, error) {
	return scanSite(s.db.QueryRowContext(ctx, "SELECT "+siteColumns+" FROM sites WHERE id=?", id))
}

func (s *Store) SiteByName(ctx context.Context, name string) (Site, error) {
	return scanSite(s.db.QueryRowContext(ctx, "SELECT "+siteColumns+" FROM sites WHERE name=?", name))
}

func (s *Store) ListSites(ctx context.Context) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+siteColumns+" FROM sites ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Site{}
	for rows.Next() {
		v, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
