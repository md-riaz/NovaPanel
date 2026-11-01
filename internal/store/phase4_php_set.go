package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"

	php "novapanel/internal/render/php"
)

// PHPOperationIdentity contains only nonsecret, full desired-state snapshots.
type PHPOperationIdentity struct {
	OriginalVersion       string
	DesiredVersion        string
	OriginalLimits        php.Limits
	OriginalLimitsPresent bool
	DesiredLimits         php.Limits
}

func validPHPSetIdentity(op Operation) bool {
	i := op.Identity
	if op.Owner == "" || !phase4ResourceID.MatchString(op.SiteID) || op.DatabaseID != "" || i.Version != 1 || i.Site == nil || i.Site.ID != op.SiteID || i.Site.Status != "active" || (i.Site.Type != "php" && i.Site.Type != "wordpress") || i.Site.PHPVersion == nil || i.PHP == nil || *i.Site.PHPVersion != i.PHP.OriginalVersion || i.PHP.OriginalVersion != "8.3" || i.PHP.DesiredVersion != "8.3" {
		return false
	}
	// Reject fields belonging to another operation kind.
	expected := OperationIdentity{Version: 1, Site: i.Site, PHP: i.PHP}
	if !reflect.DeepEqual(i, expected) || len(op.Nonce) != 0 || len(op.Ciphertext) != 0 {
		return false
	}
	if _, err := php.MergeLimits(&i.PHP.DesiredLimits, php.LimitsPatch{}); err != nil {
		return false
	}
	if _, err := php.MergeLimits(&i.PHP.OriginalLimits, php.LimitsPatch{}); err != nil {
		return false
	}
	if !i.PHP.OriginalLimitsPresent && i.PHP.OriginalLimits != (php.Limits{MemoryMB: 256, UploadMB: 64, MaxExecSec: 60}) {
		return false
	}
	return true
}

func phpSnapshot(ctx context.Context, tx *sql.Tx, siteID string) (Site, *php.Limits, error) {
	site, err := scanSite(tx.QueryRowContext(ctx, "SELECT "+siteColumns+" FROM sites WHERE id=?", siteID))
	if err != nil {
		return site, nil, err
	}
	if site.Status != "active" || (site.Type != "php" && site.Type != "wordpress") || site.PHPVersion == nil || *site.PHPVersion != "8.3" {
		return site, nil, ErrSitePrecondition
	}
	var limits php.Limits
	err = tx.QueryRowContext(ctx, "SELECT memory_mb,upload_mb,max_exec_sec FROM site_php_limits WHERE site_id=?", siteID).Scan(&limits.MemoryMB, &limits.UploadMB, &limits.MaxExecSec)
	if errors.Is(err, sql.ErrNoRows) {
		return site, nil, nil
	}
	if err != nil {
		return site, nil, err
	}
	return site, &limits, nil
}

func (s *Store) preparePHPSet(ctx context.Context, j Job, r OperationRequest, nonce, ciphertext []byte) (op Operation, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if j.Verb != "php.set" || j.ObjectKey != "site:"+r.SiteID || j.CreatedBy == "" || j.State != "queued" || r.PHP == nil || r.PHP.SiteID != r.SiteID || r.PHP.Version != "8.3" || r.DatabaseID != "" || r.UserID != "" || r.WordPress != nil || r.PurgeFiles || r.PurgeDatabases || len(nonce) != 0 || len(ciphertext) != 0 {
			return ErrSiteInvalid
		}
		site, original, e := phpSnapshot(ctx, tx, r.SiteID)
		if e != nil {
			return e
		}
		desired, e := php.MergeLimits(original, r.PHP.Limits)
		if e != nil {
			return e
		}
		originalTuple, e := php.MergeLimits(original, php.LimitsPatch{})
		if e != nil {
			return e
		}
		identity := OperationIdentity{Version: 1, Site: &site, PHP: &PHPOperationIdentity{OriginalVersion: *site.PHPVersion, DesiredVersion: r.PHP.Version, OriginalLimits: originalTuple, OriginalLimitsPresent: original != nil, DesiredLimits: desired}}
		op = Operation{JobID: j.ID, Owner: j.CreatedBy, Kind: PHPSet, SiteID: r.SiteID, Checkpoint: "prepared", Identity: identity}
		if !validPHPSetIdentity(op) {
			return ErrSiteInvalid
		}
		if e = operationBlocked(ctx, tx, op); e != nil {
			return e
		}
		if e = insertJob(ctx, tx, j); e != nil {
			return e
		}
		op.Updated = s.clock.Now()
		op.Expires = op.Updated + OperationTTLMillis
		raw, e := json.Marshal(identity)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO phase4_operations(job_id,kind,site_id,checkpoint,identities_json,expires,updated) VALUES(?,?,?,'prepared',?,?,?)`, j.ID, PHPSet, r.SiteID, string(raw), op.Expires, op.Updated)
		return e
	})
	return
}

func checkPHPSet(ctx context.Context, tx *sql.Tx, op Operation) error {
	site, limits, err := phpSnapshot(ctx, tx, op.SiteID)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrSitePrecondition) {
		return ErrSiteConflict
	}
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(site, *op.Identity.Site) || (limits != nil) != op.Identity.PHP.OriginalLimitsPresent || (limits != nil && *limits != op.Identity.PHP.OriginalLimits) {
		return ErrSiteConflict
	}
	return nil
}

func completePHPSet(ctx context.Context, tx *sql.Tx, op Operation, now int64, claim *JobClaim) error {
	if claim != nil {
		return ErrSiteInvalid
	}
	if err := checkPHPSet(ctx, tx, op); err != nil {
		return err
	}
	p := op.Identity.PHP
	if _, err := tx.ExecContext(ctx, "UPDATE sites SET php_version=?,updated=? WHERE id=?", p.DesiredVersion, now, op.SiteID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO site_php_limits(site_id,memory_mb,upload_mb,max_exec_sec) VALUES(?,?,?,?) ON CONFLICT(site_id) DO UPDATE SET memory_mb=excluded.memory_mb,upload_mb=excluded.upload_mb,max_exec_sec=excluded.max_exec_sec`, op.SiteID, p.DesiredLimits.MemoryMB, p.DesiredLimits.UploadMB, p.DesiredLimits.MaxExecSec)
	if err != nil {
		return err
	}
	return finishOperation(ctx, tx, op, now, []byte(`{}`))
}
