package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"novapanel/internal/wordpress"
	"path/filepath"
	"strings"
)

// WordPressReservation contains only trusted nonsecret identity. Evidence is a
// pure validation input, never a replacement for runtime artifact custody.
type WordPressReservation struct {
	SiteID   string
	DomainID string
	Input    wordpress.InstallInput
	Evidence wordpress.ArtifactEvidence
}

type FreshWordPressIdempotency struct {
	Key, Payload string
	SealedToken  []byte
}

// PrepareFreshWordPress creates the job before its account provenance, then
// seals the allocated identity and credentials before committing the reservation.
func (s *Store) PrepareFreshWordPress(ctx context.Context, j Job, c JobClaim, r WordPressReservation, site Site, domain Domain, limits PHPLimits, seal func(WordPressReservation) ([]byte, []byte, error), idem ...FreshWordPressIdempotency) (op Operation, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if j.Verb != "site.create" || j.CreatedBy == "" || j.State != "queued" || c.JobID != j.ID || c.ClaimHash == "" || seal == nil || r.SiteID != site.ID || r.DomainID != domain.ID || r.Input.SiteName != site.Name || r.Input.PrimaryDomain != domain.FQDN {
			return ErrSiteInvalid
		}
		if err := insertJob(ctx, tx, j); err != nil {
			return err
		}
		uid, err := s.ReserveWordPressSiteTx(ctx, tx, site, domain, j.ID, limits)
		if err != nil {
			return err
		}
		r.Input.UID, r.Input.GID = uid, uid
		ident := OperationIdentity{Version: 1, Database: OperationDatabaseRecord{ID: r.Input.DatabaseID, SiteID: r.SiteID, Name: "d_" + strings.ToLower(r.Input.DatabaseID), Engine: "mariadb", PrimaryUserID: r.Input.DatabaseUserID}, Accounts: []OperationAccount{{ID: r.Input.DatabaseUserID, Username: "u_" + strings.ToLower(r.Input.DatabaseUserID), Host: "127.0.0.1"}}, WordPress: &r}
		op = Operation{JobID: j.ID, Owner: j.CreatedBy, Kind: WordPressInstall, SiteID: r.SiteID, DatabaseID: r.Input.DatabaseID, Checkpoint: "prepared", Identity: ident}
		if !validWordPressIdentity(op) {
			return ErrSiteInvalid
		}
		if err = operationBlocked(ctx, tx, op); err != nil {
			return err
		}
		op.Nonce, op.Ciphertext, err = seal(r)
		if err != nil {
			return err
		}
		if len(op.Nonce) == 0 || len(op.Ciphertext) == 0 {
			return ErrSiteInvalid
		}
		c.Nonce, c.Ciphertext, c.Expires = nil, nil, 0
		if err = insertClaim(ctx, tx, c); err != nil {
			return err
		}
		op.Updated = s.clock.Now()
		op.Expires = op.Updated + OperationTTLMillis
		raw, err := json.Marshal(ident)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO phase4_operations(job_id,kind,site_id,db_id,checkpoint,identities_json,nonce,ciphertext,expires,updated) VALUES(?,?,?,?,'prepared',?,?,?,?,?)`, j.ID, WordPressInstall, r.SiteID, op.DatabaseID, string(raw), op.Nonce, op.Ciphertext, op.Expires, op.Updated); err != nil {
			return err
		}
		if len(idem) > 1 {
			return ErrSiteInvalid
		}
		if len(idem) == 1 && idem[0].Key != "" {
			if idem[0].Payload == "" || len(idem[0].SealedToken) == 0 {
				return ErrSiteInvalid
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM idempotency WHERE id=? AND created<=?`, idem[0].Key, s.clock.Now()-24*60*60*1000); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO idempotency(id,job_id,result_json,payload_hash,created) VALUES(?,?,?,?,?)`, idem[0].Key, j.ID, string(idem[0].SealedToken), idem[0].Payload, s.clock.Now()); err != nil {
				return err
			}
		}
		return checkWordPress(ctx, tx, op)
	})
	return
}

func validWordPressIdentity(op Operation) bool {
	id := op.Identity
	w := id.WordPress
	if id.Version != 1 || op.Owner == "" || !phase4ResourceID.MatchString(op.SiteID) || w == nil || w.SiteID != op.SiteID || !phase4ResourceID.MatchString(w.DomainID) || w.Input.DatabaseID != op.DatabaseID || w.Input.DatabaseUserID != id.Database.PrimaryUserID || id.Site != nil || len(id.Attached) != 0 || id.BindingID != "" || id.TargetUserID != "" || id.PurgeFiles || id.PurgeDatabases {
		return false
	}
	nested := op
	nested.Kind = DatabaseCreate
	nested.Identity.WordPress = nil
	nested.Identity.ConfigCleanup = ""
	nested.Identity.DownloadStarted = false
	if !validOperationIdentity(nested) {
		return false
	}
	_, e := wordpress.BuildInstall(w.Input, w.Evidence)
	return e == nil
}

// MarkWordPressDownloadStarted records the first empty-docroot check before download.
// The caller must hold the original operation and lifetime locks throughout.
func (s *Store) MarkWordPressDownloadStarted(ctx context.Context, jobID string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		op, err := loadOperation(ctx, tx, jobID)
		if err != nil {
			return err
		}
		if op.Kind != WordPressInstall || op.Checkpoint != "db_applied" || op.Identity.WordPress == nil {
			return ErrSitePrecondition
		}
		jobDriven, err := freshInstallJobDriven(ctx, tx, op)
		if err != nil {
			return err
		}
		if s.clock.Now() >= op.Expires && !jobDriven {
			return ErrOperationExpired
		}
		w := op.Identity.WordPress.Input
		var found int
		args := lifetimeArgs(SiteLifetime{SiteID: op.SiteID, Name: w.SiteName, Docroot: filepath.Join(w.SitesRoot, w.SiteName, "public"), AllocatingJob: jobID, UID: w.UID, GID: w.GID})
		if err = tx.QueryRowContext(ctx, `SELECT 1 FROM sites WHERE status='creating' AND `+freshLifetimeWhere, args...).Scan(&found); err != nil {
			if err == sql.ErrNoRows {
				return ErrSitePrecondition
			}
			return err
		}
		if op.Identity.DownloadStarted {
			return nil
		}
		op.Identity.DownloadStarted = true
		raw, err := json.Marshal(op.Identity)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE phase4_operations SET identities_json=?,updated=? WHERE job_id=? AND checkpoint='db_applied'`, string(raw), s.clock.Now(), jobID)
		return affected(result, err)
	})
}

func checkWordPress(ctx context.Context, tx *sql.Tx, op Operation) error {
	w := op.Identity.WordPress
	var n int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM sites s JOIN domains d ON d.site_id=s.id WHERE s.id=? AND s.name=? AND s.type='wordpress' AND s.docroot=? AND d.id=? AND d.fqdn=? AND d.is_primary=1 AND (s.status='active' OR (s.status='creating' AND s.lifetime_state IN ('provisioning','served') AND EXISTS (SELECT 1 FROM site_account_provenance p JOIN phase4_operations o ON o.job_id=p.allocating_job JOIN jobs j ON j.id=o.job_id WHERE p.site_id=s.id AND p.account_name=s.name AND p.uid=? AND p.gid=? AND p.allocating_job=? AND o.site_id=s.id AND o.kind='wordpress_install' AND o.reconciled=0 AND o.checkpoint NOT IN ('done','expired') AND j.state IN ('queued','running'))))`, op.SiteID, w.Input.SiteName, filepath.Join(w.Input.SitesRoot, w.Input.SiteName, "public"), w.DomainID, w.Input.PrimaryDomain, w.Input.UID, w.Input.GID, op.JobID).Scan(&n); e != nil {
		return e
	}
	if n != 1 {
		return ErrSitePrecondition
	}
	if e := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM wordpress_bindings WHERE site_id=?)+(SELECT count(*) FROM databases WHERE id=? OR name=?)+(SELECT count(*) FROM db_users WHERE id=? OR username=?)`, op.SiteID, op.DatabaseID, op.Identity.Database.Name, op.Identity.Database.PrimaryUserID, op.Identity.Accounts[0].Username).Scan(&n); e != nil {
		return e
	}
	if n != 0 {
		return ErrSiteConflict
	}
	return nil
}

func (s *Store) prepareWordPress(ctx context.Context, j Job, c JobClaim, r OperationRequest, nonce, ciphertext []byte) (op Operation, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if j.Verb != "site.create" || j.CreatedBy == "" || j.State != "queued" || r.WordPress == nil || len(nonce) == 0 || len(ciphertext) == 0 || c.JobID != j.ID || c.ClaimHash == "" || r.PurgeFiles || r.PurgeDatabases {
			return ErrSiteInvalid
		}
		w := *r.WordPress
		w.Input.DatabaseID = r.DatabaseID
		w.Input.DatabaseUserID = r.UserID
		ident := OperationIdentity{Version: 1, Database: OperationDatabaseRecord{ID: r.DatabaseID, SiteID: r.SiteID, Name: "d_" + strings.ToLower(r.DatabaseID), Engine: "mariadb", PrimaryUserID: r.UserID}, Accounts: []OperationAccount{{ID: r.UserID, Username: "u_" + strings.ToLower(r.UserID), Host: "127.0.0.1"}}, WordPress: &w}
		op = Operation{JobID: j.ID, Owner: j.CreatedBy, Kind: WordPressInstall, SiteID: r.SiteID, DatabaseID: r.DatabaseID, Checkpoint: "prepared", Identity: ident, Nonce: nonce, Ciphertext: ciphertext}
		if !validWordPressIdentity(op) {
			return ErrSiteInvalid
		}
		if e := operationBlocked(ctx, tx, op); e != nil {
			return e
		}
		if e := checkWordPress(ctx, tx, op); e != nil {
			return e
		}
		if e := insertJob(ctx, tx, j); e != nil {
			return e
		}
		c.Nonce = nil
		c.Ciphertext = nil
		c.Expires = 0
		if e := insertClaim(ctx, tx, c); e != nil {
			return e
		}
		op.Updated = s.clock.Now()
		op.Expires = op.Updated + OperationTTLMillis
		raw, e := json.Marshal(ident)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO phase4_operations(job_id,kind,site_id,db_id,checkpoint,identities_json,nonce,ciphertext,expires,updated) VALUES(?,?,?,?,'prepared',?,?,?,?,?)`, j.ID, WordPressInstall, r.SiteID, r.DatabaseID, string(raw), nonce, ciphertext, op.Expires, op.Updated)
		return e
	})
	return
}

func completeWordPress(ctx context.Context, tx *sql.Tx, op Operation, now int64, claim *JobClaim) error {
	if op.Checkpoint != "verified" || claim == nil || len(claim.Nonce) == 0 || len(claim.Ciphertext) == 0 {
		return ErrSitePrecondition
	}
	if e := checkWordPress(ctx, tx, op); e != nil {
		return e
	}
	var e error
	db := op.Identity.Database
	a := op.Identity.Accounts[0]
	if _, e = tx.ExecContext(ctx, "INSERT INTO databases(id,site_id,name,created) VALUES(?,?,?,?)", db.ID, op.SiteID, db.Name, now); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO db_users(id,db_id,username,host,owner,created) VALUES(?,?,?,?,1,?)", a.ID, db.ID, a.Username, a.Host, now); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO wordpress_bindings(site_id,db_id) VALUES(?,?)", op.SiteID, db.ID); e != nil {
		return e
	}
	var fresh bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM site_account_provenance WHERE site_id=? AND allocating_job=?)`, op.SiteID, op.JobID).Scan(&fresh); e != nil {
		return e
	}
	if fresh {
		result, err := tx.ExecContext(ctx, `UPDATE sites SET status='active',updated=? WHERE id=? AND status='creating' AND lifetime_state='served' AND EXISTS (SELECT 1 FROM site_account_provenance WHERE site_id=sites.id AND allocating_job=?)`, now, op.SiteID, op.JobID)
		if err = affected(result, err); err != nil {
			return err
		}
	}
	result, e := tx.ExecContext(ctx, "UPDATE job_claims SET nonce=?,ciphertext=?,expires=? WHERE job_id=? AND length(ciphertext)=0 AND claimed=0", claim.Nonce, claim.Ciphertext, now+ClaimTTLMillis, op.JobID)
	if e = affected(result, e); e != nil {
		return e
	}
	raw, e := json.Marshal(map[string]string{"site_id": op.SiteID})
	if e != nil {
		return e
	}
	return finishOperation(ctx, tx, op, now, raw)
}
