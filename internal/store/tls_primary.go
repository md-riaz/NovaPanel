package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
)

// PrimaryPromotion binds the unchanged database state to a probed publication.
type PrimaryPromotion struct {
	Site    Site      `json:"site"`
	Domains []Domain  `json:"domains"`
	Target  string    `json:"target"`
	Patch   SitePatch `json:"patch"`
}

func (s *Store) CommitTLSPrimary(ctx context.Context, jobID string, p PrimaryPromotion, expiry int64, raw json.RawMessage) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		current, e := scanSite(tx.QueryRowContext(ctx, "SELECT "+siteColumns+" FROM sites WHERE id=?", p.Site.ID))
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(current, p.Site) || current.Status != "active" || current.CertSource == "none" {
			return ErrSitePrecondition
		}
		rows, e := tx.QueryContext(ctx, "SELECT "+domainColumns+" FROM domains WHERE site_id=? ORDER BY id", p.Site.ID)
		if e != nil {
			return e
		}
		ds := []Domain{}
		for rows.Next() {
			d, e := scanDomain(rows)
			if e != nil {
				rows.Close()
				return e
			}
			ds = append(ds, d)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(ds, p.Domains) {
			return ErrSitePrecondition
		}
		primaries, targets := 0, 0
		for _, d := range ds {
			if d.IsPrimary {
				primaries++
			}
			if d.ID == p.Target && !d.IsPrimary {
				targets++
			}
		}
		if primaries != 1 || targets != 1 || p.Patch.PrimaryDomainID == nil || *p.Patch.PrimaryDomainID != p.Target {
			return ErrSitePrecondition
		}
		if _, e = tx.ExecContext(ctx, "UPDATE domains SET is_primary=(id=?),cert_state='issued',cert_not_after=? WHERE site_id=?", p.Target, expiry, p.Site.ID); e != nil {
			return e
		}
		r, e := tx.ExecContext(ctx, "UPDATE sites SET maintenance=COALESCE(?,maintenance),force_https=COALESCE(?,force_https),hsts_max_age=COALESCE(?,hsts_max_age),updated=? WHERE id=?", p.Patch.Maintenance, p.Patch.ForceHTTPS, p.Patch.HSTSMaxAge, s.clock.Now(), p.Site.ID)
		if e = affected(r, e); e != nil {
			return e
		}
		r, e = tx.ExecContext(ctx, "UPDATE jobs SET step='tls_committed',result_json='{}' WHERE id=? AND verb='site.update' AND object_key=? AND state='running'", jobID, "site:"+p.Site.ID)
		if e = affected(r, e); e != nil {
			return e
		}
		r, e = tx.ExecContext(ctx, "DELETE FROM settings WHERE key=? AND value_json=?", tlsIntentKey, string(raw))
		return affected(r, e)
	})
}
