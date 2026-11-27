package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"

	php "novapanel/internal/render/php"
)

type CompositeSiteUpdate struct {
	SiteID          string  `json:"site_id"`
	PrimaryDomainID *string `json:"primary_domain_id,omitempty"`
	PHPVersion      *string `json:"php_version,omitempty"`
	ForceHTTPS      *bool   `json:"force_https,omitempty"`
	HSTSMaxAge      *int64  `json:"hsts_max_age,omitempty"`
	Maintenance     *bool   `json:"maintenance,omitempty"`
}

func validCompositeSiteUpdate(op Operation) bool {
	v := op.Identity.SiteUpdate
	if v == nil || op.Owner == "" || !phase4ResourceID.MatchString(op.SiteID) || v.SiteID != op.SiteID || op.DatabaseID != "" || op.Identity.Version != 1 || len(op.Nonce) != 0 || len(op.Ciphertext) != 0 {
		return false
	}
	if v.PrimaryDomainID != nil && !phase4ResourceID.MatchString(*v.PrimaryDomainID) || v.HSTSMaxAge != nil && *v.HSTSMaxAge < 0 || v.PHPVersion != nil && *v.PHPVersion != "8.3" {
		return false
	}
	count := 0
	for _, present := range []bool{v.PrimaryDomainID != nil, v.PHPVersion != nil, v.ForceHTTPS != nil, v.HSTSMaxAge != nil, v.Maintenance != nil} {
		if present {
			count++
		}
	}
	return count > 1 && reflect.DeepEqual(op.Identity, OperationIdentity{Version: 1, SiteUpdate: v, PublicationAudit: op.Identity.PublicationAudit})
}

func (s *Store) prepareCompositeSiteUpdate(ctx context.Context, j Job, r OperationRequest, nonce, ciphertext []byte) (op Operation, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if r.SiteUpdate == nil || r.SiteID != r.SiteUpdate.SiteID || j.Verb != "site.update" || j.ObjectKey != "site:"+r.SiteID || j.State != "queued" || len(nonce) != 0 || len(ciphertext) != 0 {
			return ErrSiteInvalid
		}
		op = Operation{JobID: j.ID, Owner: j.CreatedBy, Kind: SiteUpdate, SiteID: r.SiteID, Checkpoint: "prepared", Identity: OperationIdentity{Version: 1, SiteUpdate: r.SiteUpdate}}
		if !validCompositeSiteUpdate(op) {
			return ErrSiteInvalid
		}
		var status string
		if e := tx.QueryRowContext(ctx, `SELECT status FROM sites WHERE id=?`, r.SiteID).Scan(&status); e != nil {
			return e
		}
		if status != "active" {
			return ErrSitePrecondition
		}
		if e := operationBlocked(ctx, tx, op); e != nil {
			return e
		}
		var snapshot *PHPOperationIdentity
		if r.SiteUpdate.PHPVersion != nil {
			identity, e := phpOperationSnapshot(ctx, tx, php.SetRequest{SiteID: r.SiteID, Version: *r.SiteUpdate.PHPVersion})
			if e != nil {
				return e
			}
			snapshot = identity.PHP
		}
		if e := insertJob(ctx, tx, j); e != nil {
			return e
		}
		op.Updated = s.clock.Now()
		op.Expires = op.Updated + OperationTTLMillis
		raw, e := json.Marshal(op.Identity)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO phase4_operations(job_id,kind,site_id,checkpoint,identities_json,expires,updated) VALUES(?,?,?,'prepared',?,?,?)`, j.ID, SiteUpdate, r.SiteID, string(raw), op.Expires, op.Updated); e != nil {
			return e
		}
		v := r.SiteUpdate
		domain := v.PrimaryDomainID != nil || v.ForceHTTPS != nil || v.HSTSMaxAge != nil
		var kinds []string
		if v.PHPVersion != nil && domain {
			kinds = append(kinds, "maintenance_on")
		}
		if v.PHPVersion != nil {
			kinds = append(kinds, "php_change")
		}
		if domain {
			kinds = append(kinds, "domain_tls")
		}
		kinds = append(kinds, "maintenance_off")
		for i, kind := range kinds {
			identity := OperationChildIdentity{}
			if kind == "php_change" {
				identity.PHP = snapshot
			}
			if !validChildIdentity(kind, identity) {
				return ErrSiteInvalid
			}
			data, e := json.Marshal(identity)
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO phase4_operation_children(job_id,ordinal,child_kind,checkpoint,status,identities_json,updated) VALUES(?,?,?,'prepared','pending',?,?)`, j.ID, i, kind, string(data), op.Updated); e != nil {
				return e
			}
		}
		return nil
	})
	return
}
