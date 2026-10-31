package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
)

// snapshotSiteDelete runs only within a transaction, with the caller's site lock held.
func snapshotSiteDelete(ctx context.Context, tx *sql.Tx, siteID string) (out OperationIdentity, err error) {
	out.Version = 1
	site, err := scanSite(tx.QueryRowContext(ctx, "SELECT "+siteColumns+" FROM sites WHERE id=?", siteID))
	if err != nil {
		return out, err
	}
	out.Site = &site
	if site.Status != "active" && site.Status != "failed" {
		return out, ErrSitePrecondition
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,coalesce(site_id,''),name,engine,primary_user_id,created FROM databases WHERE site_id=? ORDER BY id", siteID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var d OperationDatabase
		if err = rows.Scan(&d.Database.ID, &d.Database.SiteID, &d.Database.Name, &d.Database.Engine, &d.Database.PrimaryUserID, &d.Database.Created); err != nil {
			rows.Close()
			return out, err
		}
		out.Attached = append(out.Attached, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for i := range out.Attached {
		rows, err = tx.QueryContext(ctx, "SELECT id,username,host FROM db_users WHERE db_id=? ORDER BY id", out.Attached[i].Database.ID)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var a OperationAccount
			if err = rows.Scan(&a.ID, &a.Username, &a.Host); err != nil {
				rows.Close()
				return out, err
			}
			out.Attached[i].Accounts = append(out.Attached[i].Accounts, a)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	err = tx.QueryRowContext(ctx, "SELECT coalesce((SELECT db_id FROM wordpress_bindings WHERE site_id=?),'')", siteID).Scan(&out.BindingID)
	return
}

func validSiteDeleteIdentity(op Operation) bool {
	id := op.Identity
	if id.Version != 1 || op.Owner == "" || op.DatabaseID != "" || op.SiteID == "" || id.Site == nil || id.Site.ID != op.SiteID || id.Site.Name == "" || id.Site.Docroot == "" || (id.Site.Status != "active" && id.Site.Status != "failed") || id.Database != (Database{}) || len(id.Accounts) != 0 || id.TargetUserID != "" {
		return false
	}
	previous := ""
	bound := id.BindingID == ""
	for _, d := range id.Attached {
		if d.Database.ID <= previous || d.Database.SiteID != op.SiteID {
			return false
		}
		previous = d.Database.ID
		nested := Operation{Owner: op.Owner, Kind: DatabaseRotate, SiteID: op.SiteID, DatabaseID: d.Database.ID, Identity: OperationIdentity{Version: 1, Database: d.Database, Accounts: d.Accounts}}
		if !validOperationIdentity(nested) {
			return false
		}
		if d.Database.ID == id.BindingID {
			bound = true
		}
	}
	return bound
}

func (s *Store) prepareSiteDelete(ctx context.Context, j Job, r OperationRequest, nonce, ciphertext []byte) (op Operation, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if j.Verb != "site.delete" || j.CreatedBy == "" || j.State != "queued" || r.SiteID == "" || r.DatabaseID != "" || r.UserID != "" || len(nonce) != 0 || len(ciphertext) != 0 {
			return ErrSiteInvalid
		}
		ident, e := snapshotSiteDelete(ctx, tx, r.SiteID)
		if e != nil {
			return e
		}
		ident.PurgeFiles = r.PurgeFiles
		ident.PurgeDatabases = r.PurgeDatabases
		op = Operation{JobID: j.ID, Owner: j.CreatedBy, Kind: SiteDelete, SiteID: r.SiteID, Checkpoint: "prepared", Identity: ident}
		if !validSiteDeleteIdentity(op) {
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
		raw, e := json.Marshal(ident)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO phase4_operations(job_id,kind,site_id,checkpoint,identities_json,expires,updated) VALUES(?,?,?,'prepared',?,?,?)`, j.ID, SiteDelete, r.SiteID, string(raw), op.Expires, op.Updated)
		return e
	})
	return
}

func checkSiteDelete(ctx context.Context, tx *sql.Tx, op Operation) error {
	current, e := snapshotSiteDelete(ctx, tx, op.SiteID)
	if e != nil {
		if e == sql.ErrNoRows || e == ErrSitePrecondition {
			return ErrSiteConflict
		}
		return e
	}
	current.PurgeFiles = op.Identity.PurgeFiles
	current.PurgeDatabases = op.Identity.PurgeDatabases
	if !reflect.DeepEqual(current, op.Identity) {
		return ErrSiteConflict
	}
	return nil
}

func completeSiteDelete(ctx context.Context, tx *sql.Tx, op Operation) error {
	if _, e := tx.ExecContext(ctx, "DELETE FROM wordpress_bindings WHERE site_id=?", op.SiteID); e != nil {
		return e
	}
	if op.Identity.PurgeDatabases {
		for _, d := range op.Identity.Attached {
			if _, e := tx.ExecContext(ctx, "DELETE FROM databases WHERE id=? AND site_id=?", d.Database.ID, op.SiteID); e != nil {
				return e
			}
		}
	}
	// ON DELETE SET NULL retains every attached database when purge is false.
	_, e := tx.ExecContext(ctx, "DELETE FROM sites WHERE id=?", op.SiteID)
	return e
}
