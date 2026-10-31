package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"novapanel/internal/wordpress"
	"strings"
)

// WordPressReservation contains only trusted nonsecret identity. Evidence is a
// pure validation input, never a replacement for runtime artifact custody.
type WordPressReservation struct {
	DomainID string
	Input    wordpress.InstallInput
	Evidence wordpress.ArtifactEvidence
}

func validWordPressIdentity(op Operation) bool {
	id := op.Identity
	w := id.WordPress
	if id.Version != 1 || op.Owner == "" || !phase4ResourceID.MatchString(op.SiteID) || w == nil || !phase4ResourceID.MatchString(w.DomainID) || w.Input.DatabaseID != op.DatabaseID || w.Input.DatabaseUserID != id.Database.PrimaryUserID || id.Site != nil || len(id.Attached) != 0 || id.BindingID != "" || id.TargetUserID != "" || id.PurgeFiles || id.PurgeDatabases {
		return false
	}
	nested := op
	nested.Kind = DatabaseCreate
	nested.Identity.WordPress = nil
	if !validOperationIdentity(nested) {
		return false
	}
	_, e := wordpress.BuildInstall(w.Input, w.Evidence)
	return e == nil
}

func checkWordPress(ctx context.Context, tx *sql.Tx, op Operation) error {
	w := op.Identity.WordPress
	var n int
	if e := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM sites WHERE id=? OR name=?)+(SELECT count(*) FROM domains WHERE id=? OR fqdn=?)+(SELECT count(*) FROM databases WHERE id=? OR name=?)+(SELECT count(*) FROM db_users WHERE id=? OR username=?)`, op.SiteID, w.Input.SiteName, w.DomainID, w.Input.PrimaryDomain, op.DatabaseID, op.Identity.Database.Name, op.Identity.Database.PrimaryUserID, op.Identity.Accounts[0].Username).Scan(&n); e != nil {
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
		ident := OperationIdentity{Version: 1, Database: Database{ID: r.DatabaseID, SiteID: r.SiteID, Name: "d_" + strings.ToLower(r.DatabaseID), Engine: "mariadb", PrimaryUserID: r.UserID}, Accounts: []OperationAccount{{ID: r.UserID, Username: "u_" + strings.ToLower(r.UserID), Host: "127.0.0.1"}}, WordPress: &w}
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
	w := op.Identity.WordPress
	plan, e := wordpress.BuildInstall(w.Input, w.Evidence)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO sites(id,name,type,docroot,php_version,status,created,updated) VALUES(?,?,'wordpress',?,'8.3','active',?,?)`, op.SiteID, w.Input.SiteName, plan.Docroot, now, now); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO domains(id,site_id,fqdn,is_primary,created) VALUES(?,?,?,1,?)", w.DomainID, op.SiteID, w.Input.PrimaryDomain, now); e != nil {
		return e
	}
	db := op.Identity.Database
	a := op.Identity.Accounts[0]
	if _, e = tx.ExecContext(ctx, "INSERT INTO databases(id,site_id,name,primary_user_id,created) VALUES(?,?,?,?,?)", db.ID, op.SiteID, db.Name, db.PrimaryUserID, now); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO db_users(id,db_id,username,host,created) VALUES(?,?,?,?,?)", a.ID, db.ID, a.Username, a.Host, now); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO wordpress_bindings(site_id,db_id) VALUES(?,?)", op.SiteID, db.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO site_php_limits(site_id) VALUES(?)", op.SiteID); e != nil {
		return e
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
