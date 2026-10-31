package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

type OperationKind string

const (
	WordPressInstall   OperationKind = "wordpress_install"
	SiteDelete         OperationKind = "site_delete"
	DatabaseCreate     OperationKind = "database_create"
	DatabaseRotate     OperationKind = "database_rotate"
	DBUserCreate       OperationKind = "dbuser_create"
	DatabaseDelete     OperationKind = "database_delete"
	DBUserDelete       OperationKind = "dbuser_delete"
	OperationTTLMillis int64         = 86400000
)

// OperationRequest contains trusted, pre-reserved identities, never RPC arguments.
// New resource IDs must be minted by the admitting agent before acquiring locks.
type OperationRequest struct {
	Kind                       OperationKind
	SiteID, DatabaseID, UserID string
	PurgeFiles, PurgeDatabases bool
	WordPress                  *WordPressReservation
}
type OperationDatabase struct {
	Database Database
	Accounts []OperationAccount
}
type OperationAccount struct{ ID, Username, Host string }
type OperationIdentity struct {
	Version                    int
	Database                   Database
	Accounts                   []OperationAccount
	TargetUserID               string
	Site                       *Site               `json:",omitempty"`
	Attached                   []OperationDatabase `json:",omitempty"`
	BindingID                  string              `json:",omitempty"`
	PurgeFiles, PurgeDatabases bool
	WordPress                  *WordPressReservation `json:",omitempty"`
}
type Operation struct {
	JobID, Owner                   string
	Kind                           OperationKind
	SiteID, DatabaseID, Checkpoint string
	Identity                       OperationIdentity
	Nonce, Ciphertext              []byte
	Expires, Updated               int64
	Reconciled                     bool
}

func (o Operation) Keys() []string {
	if o.Kind == WordPressInstall {
		return OrderedOperationKeys([]string{"site:" + o.SiteID, "site:" + o.Identity.WordPress.Input.SiteName, "db:" + o.DatabaseID, "db:" + o.Identity.Database.Name, "domain:" + o.Identity.WordPress.DomainID, "domain:" + o.Identity.WordPress.Input.PrimaryDomain})
	}
	if o.Kind == SiteDelete {
		keys := []string{"site:" + o.SiteID}
		for _, db := range o.Identity.Attached {
			keys = append(keys, "db:"+db.Database.ID)
		}
		return OrderedOperationKeys(keys)
	}
	keys := []string{"db:" + o.DatabaseID}
	if o.SiteID != "" {
		keys = append(keys, "site:"+o.SiteID)
	}
	if o.Kind == DatabaseCreate {
		keys = append(keys, "db:"+o.Identity.Database.Name)
	}
	return OrderedOperationKeys(keys)
}

// OrderedOperationKeys is shared with the queue; site locks always precede DB locks.
func OrderedOperationKeys(keys []string) []string {
	keys = append([]string(nil), keys...)
	rank := func(k string) string {
		if strings.HasPrefix(k, "site:") {
			return "0" + k
		}
		return "1" + k
	}
	sort.Slice(keys, func(i, j int) bool { return rank(keys[i]) < rank(keys[j]) })
	out := keys[:0]
	for _, k := range keys {
		if len(out) == 0 || out[len(out)-1] != k {
			out = append(out, k)
		}
	}
	return out
}

const operationColumns = `o.job_id,j.created_by,j.verb,o.kind,coalesce(o.site_id,''),coalesce(o.db_id,''),o.checkpoint,o.identities_json,o.nonce,o.ciphertext,o.expires,o.updated,o.reconciled`

func scanOperation(row scanner) (o Operation, err error) {
	var raw, verb string
	err = row.Scan(&o.JobID, &o.Owner, &verb, &o.Kind, &o.SiteID, &o.DatabaseID, &o.Checkpoint, &raw, &o.Nonce, &o.Ciphertext, &o.Expires, &o.Updated, &o.Reconciled)
	if err != nil {
		return
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&o.Identity); err != nil {
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF || verb != operationVerb(o.Kind) || !validOperationIdentity(o) {
		err = errors.New("operation identity corrupt")
	}
	return
}

func validOperationIdentity(o Operation) bool {
	if o.Kind == WordPressInstall {
		return validWordPressIdentity(o)
	}
	if o.Kind == SiteDelete {
		return validSiteDeleteIdentity(o)
	}
	d := o.Identity.Database
	if operationVerb(o.Kind) == "" || o.Owner == "" || o.Identity.Version != 1 || !phase4ResourceID.MatchString(d.ID) || d.ID != o.DatabaseID || d.SiteID != o.SiteID || d.Name != "d_"+strings.ToLower(d.ID) || d.Engine != "mariadb" || !phase4ResourceID.MatchString(d.PrimaryUserID) || len(o.Identity.Accounts) == 0 {
		return false
	}
	seen := make(map[string]bool)
	for _, a := range o.Identity.Accounts {
		if !phase4ResourceID.MatchString(a.ID) || seen[a.ID] || a.Username != "u_"+strings.ToLower(a.ID) || a.Host != "127.0.0.1" {
			return false
		}
		seen[a.ID] = true
	}
	if o.Kind == DatabaseCreate && (len(seen) != 1 || !seen[d.PrimaryUserID]) {
		return false
	}
	if o.Kind == DBUserCreate && (len(seen) != 1 || seen[d.PrimaryUserID]) {
		return false
	}
	if o.Kind != DatabaseCreate && o.Kind != DBUserCreate && !seen[d.PrimaryUserID] {
		return false
	}
	if o.Kind == DBUserDelete {
		return seen[o.Identity.TargetUserID] && o.Identity.TargetUserID != d.PrimaryUserID
	}
	return o.Identity.TargetUserID == ""
}
func (s *Store) LoadOperation(ctx context.Context, jid string) (Operation, error) {
	return scanOperation(s.db.QueryRowContext(ctx, "SELECT "+operationColumns+" FROM phase4_operations o JOIN jobs j ON j.id=o.job_id WHERE o.job_id=?", jid))
}
func loadOperation(ctx context.Context, tx *sql.Tx, jid string) (Operation, error) {
	return scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationColumns+" FROM phase4_operations o JOIN jobs j ON j.id=o.job_id WHERE o.job_id=?", jid))
}
func operationVerb(k OperationKind) string {
	switch k {
	case WordPressInstall:
		return "site.create"
	case SiteDelete:
		return "site.delete"
	case DatabaseCreate:
		return "database.create"
	case DatabaseRotate:
		return "database.rotate_password"
	case DBUserCreate:
		return "dbuser.create"
	case DatabaseDelete:
		return "database.delete"
	case DBUserDelete:
		return "dbuser.delete"
	}
	return ""
}
func operationSecrets(k OperationKind) bool {
	return k == DatabaseCreate || k == DatabaseRotate || k == DBUserCreate || k == WordPressInstall
}

// PrepareOperation atomically reserves immutable identities, job authority and claim hash.
// No runtime effect or resource lock is taken while SQLite is held.
func (s *Store) PrepareOperation(ctx context.Context, j Job, c JobClaim, r OperationRequest, nonce, ciphertext []byte) (op Operation, err error) {
	if r.Kind == WordPressInstall {
		return s.prepareWordPress(ctx, j, c, r, nonce, ciphertext)
	}
	if r.Kind == SiteDelete {
		return s.prepareSiteDelete(ctx, j, r, nonce, ciphertext)
	}
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if operationVerb(r.Kind) == "" || j.Verb != operationVerb(r.Kind) || j.CreatedBy == "" || j.State != "queued" || !phase4ResourceID.MatchString(r.DatabaseID) {
			return ErrSiteInvalid
		}
		if operationSecrets(r.Kind) != (len(nonce) > 0 && len(ciphertext) > 0) || ((len(nonce) == 0) != (len(ciphertext) == 0)) {
			return ErrSiteInvalid
		}
		if r.Kind == DatabaseDelete || r.Kind == DBUserDelete {
			if len(ciphertext) != 0 {
				return ErrSiteInvalid
			}
		}
		var ident OperationIdentity
		ident.Version = 1
		if r.Kind == DatabaseCreate {
			if !phase4ResourceID.MatchString(r.UserID) {
				return ErrSiteInvalid
			}
			ident.Database = Database{ID: r.DatabaseID, SiteID: r.SiteID, Name: "d_" + strings.ToLower(r.DatabaseID), Engine: "mariadb", PrimaryUserID: r.UserID}
			ident.Accounts = []OperationAccount{{ID: r.UserID, Username: "u_" + strings.ToLower(r.UserID), Host: "127.0.0.1"}}
			var n int
			if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM databases WHERE id=? OR name=?", r.DatabaseID, ident.Database.Name).Scan(&n); e != nil {
				return e
			}
			if n != 0 {
				return ErrSiteConflict
			}
		} else {
			dbid := r.DatabaseID
			if r.Kind == DBUserDelete {
				if e := tx.QueryRowContext(ctx, "SELECT db_id FROM db_users WHERE id=?", r.UserID).Scan(&dbid); e != nil {
					return e
				}
				if dbid != r.DatabaseID {
					return ErrSiteConflict
				}
			}
			if e := tx.QueryRowContext(ctx, "SELECT id,coalesce(site_id,''),name,engine,primary_user_id,created FROM databases WHERE id=?", dbid).Scan(&ident.Database.ID, &ident.Database.SiteID, &ident.Database.Name, &ident.Database.Engine, &ident.Database.PrimaryUserID, &ident.Database.Created); e != nil {
				return e
			}
			if r.SiteID != "" && r.SiteID != ident.Database.SiteID {
				return ErrSiteConflict
			}
			rows, e := tx.QueryContext(ctx, "SELECT id,username,host FROM db_users WHERE db_id=? ORDER BY id", dbid)
			if e != nil {
				return e
			}
			for rows.Next() {
				var a OperationAccount
				if e = rows.Scan(&a.ID, &a.Username, &a.Host); e != nil {
					rows.Close()
					return e
				}
				ident.Accounts = append(ident.Accounts, a)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			if r.Kind == DBUserCreate {
				if !phase4ResourceID.MatchString(r.UserID) {
					return ErrSiteInvalid
				}
				ident.Accounts = []OperationAccount{{ID: r.UserID, Username: "u_" + strings.ToLower(r.UserID), Host: "127.0.0.1"}}
			}
			if r.Kind == DBUserDelete {
				if r.UserID == ident.Database.PrimaryUserID {
					return ErrSitePrecondition
				}
				ident.TargetUserID = r.UserID
			}
			if r.Kind == DatabaseDelete {
				var n int
				if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM wordpress_bindings WHERE db_id=?", dbid).Scan(&n); e != nil {
					return e
				}
				if n != 0 {
					return ErrSitePrecondition
				}
			}
		}
		if ident.Database.SiteID != "" {
			var status string
			if e := tx.QueryRowContext(ctx, "SELECT status FROM sites WHERE id=?", ident.Database.SiteID).Scan(&status); e != nil {
				return e
			}
			if status != "active" {
				return ErrSitePrecondition
			}
		}
		// All identities are durable before the queue acquires its complete lock set.
		op = Operation{JobID: j.ID, Owner: j.CreatedBy, Kind: r.Kind, SiteID: ident.Database.SiteID, DatabaseID: r.DatabaseID, Checkpoint: "prepared", Identity: ident, Nonce: nonce, Ciphertext: ciphertext}
		if e := operationBlocked(ctx, tx, op); e != nil {
			return e
		}
		if e := insertJob(ctx, tx, j); e != nil {
			return e
		}
		if operationSecrets(r.Kind) {
			if c.JobID != j.ID || c.ClaimHash == "" {
				return ErrSiteInvalid
			}
			c.Nonce = nil
			c.Ciphertext = nil
			c.Expires = 0
			if e := insertClaim(ctx, tx, c); e != nil {
				return e
			}
		}
		// First write above owns the write lock before sampling publication time.
		op.Updated = s.clock.Now()
		op.Expires = op.Updated + OperationTTLMillis
		raw, e := json.Marshal(ident)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO phase4_operations(job_id,kind,site_id,db_id,checkpoint,identities_json,nonce,ciphertext,expires,updated) VALUES(?,?,?,?,?,?,?,?,?,?)`, j.ID, r.Kind, nullableString(op.SiteID), r.DatabaseID, "prepared", string(raw), nilBytes(nonce), nilBytes(ciphertext), op.Expires, op.Updated)
		return e
	})
	return
}
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func nilBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
func operationBlocked(ctx context.Context, tx *sql.Tx, op Operation) error {
	// Include reservation aliases, not just rows that do not yet exist.
	rows, e := tx.QueryContext(ctx, "SELECT "+operationColumns+" FROM phase4_operations o JOIN jobs j ON j.id=o.job_id WHERE o.job_id<>? AND o.checkpoint<>'done' AND NOT(o.checkpoint='expired' AND o.reconciled=1)", op.JobID)
	if e != nil {
		return e
	}
	defer rows.Close()
	keys := op.Keys()
	for rows.Next() {
		other, err := scanOperation(rows)
		if err != nil {
			return err
		}
		for _, a := range keys {
			for _, b := range other.Keys() {
				if a == b {
					return ErrOperationLocked
				}
			}
		}
	}
	if e = rows.Err(); e != nil {
		return e
	}
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM phase4_operations WHERE job_id<>? AND checkpoint<>'done' AND NOT(checkpoint='expired' AND reconciled=1) AND (db_id=? OR (?<>'' AND site_id=?))`, op.JobID, op.DatabaseID, op.SiteID, op.SiteID).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrOperationLocked
	}
	return nil
}

var ErrOperationLocked = errors.New("operation locked")
var ErrOperationExpired = errors.New("operation interrupted by expiry")

// CheckOperation must run under the complete lock set before each external mutation.
func (s *Store) CheckOperation(ctx context.Context, jid string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		op, e := loadOperation(ctx, tx, jid)
		if e != nil {
			return e
		}
		if op.Checkpoint == "done" {
			return nil
		}
		if op.Checkpoint == "expired" || s.clock.Now() >= op.Expires {
			return ErrOperationExpired
		}
		if e = operationBlocked(ctx, tx, op); e != nil {
			return e
		}
		if op.Kind == SiteDelete {
			return checkSiteDelete(ctx, tx, op)
		}
		if op.Kind == WordPressInstall {
			return checkWordPress(ctx, tx, op)
		}
		if op.Kind == DatabaseCreate {
			if e = checkOperationSite(ctx, tx, op); e != nil {
				return e
			}
			var n int
			e = tx.QueryRowContext(ctx, "SELECT count(*) FROM databases WHERE id=? OR name=?", op.DatabaseID, op.Identity.Database.Name).Scan(&n)
			if e != nil {
				return e
			}
			if n != 0 {
				return ErrSiteConflict
			}
		} else {
			var site string
			e = tx.QueryRowContext(ctx, "SELECT coalesce(site_id,'') FROM databases WHERE id=?", op.DatabaseID).Scan(&site)
			if e != nil {
				return e
			}
			if site != op.SiteID {
				return ErrSiteConflict
			}
			var primary string
			if e = tx.QueryRowContext(ctx, "SELECT primary_user_id FROM databases WHERE id=?", op.DatabaseID).Scan(&primary); e != nil {
				return e
			}
			if primary != op.Identity.Database.PrimaryUserID {
				return ErrSiteConflict
			}
			if e = checkOperationMembership(ctx, tx, op); e != nil {
				return e
			}
		}
		return nil
	})
}
func checkOperationSite(ctx context.Context, tx *sql.Tx, op Operation) error {
	if op.SiteID == "" {
		return nil
	}
	var status string
	if e := tx.QueryRowContext(ctx, "SELECT status FROM sites WHERE id=?", op.SiteID).Scan(&status); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return ErrSiteConflict
		}
		return e
	}
	if status != "active" {
		return ErrSiteConflict
	}
	return nil
}

func checkOperationMembership(ctx context.Context, tx *sql.Tx, op Operation) error {
	var db Database
	if e := tx.QueryRowContext(ctx, "SELECT id,coalesce(site_id,''),name,engine,primary_user_id,created FROM databases WHERE id=?", op.DatabaseID).Scan(&db.ID, &db.SiteID, &db.Name, &db.Engine, &db.PrimaryUserID, &db.Created); e != nil {
		return e
	}
	if db != op.Identity.Database {
		return ErrSiteConflict
	}
	if e := checkOperationSite(ctx, tx, op); e != nil {
		return e
	}
	if op.Kind == DatabaseDelete {
		var n int
		if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM wordpress_bindings WHERE db_id=?", op.DatabaseID).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return ErrSitePrecondition
		}
	}
	if op.Kind == DBUserCreate {
		var n int
		a := op.Identity.Accounts[0]
		if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM db_users WHERE id=? OR username=?", a.ID, a.Username).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return ErrSiteConflict
		}
		return nil
	}
	rows, e := tx.QueryContext(ctx, "SELECT id,username,host FROM db_users WHERE db_id=? ORDER BY id", op.DatabaseID)
	if e != nil {
		return e
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var a OperationAccount
		if e = rows.Scan(&a.ID, &a.Username, &a.Host); e != nil {
			return e
		}
		if i >= len(op.Identity.Accounts) || a != op.Identity.Accounts[i] {
			return ErrSiteConflict
		}
		i++
	}
	if e = rows.Err(); e != nil {
		return e
	}
	if i != len(op.Identity.Accounts) {
		return ErrSiteConflict
	}
	return nil
}

func (s *Store) CheckpointOperation(ctx context.Context, jid, from, to string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		op, e := loadOperation(ctx, tx, jid)
		if e != nil {
			return e
		}
		allowed := from == "prepared" && to == "db_applied" || from == "db_applied" && to == "verified" || from == "db_applied" && to == "config_published" || from == "config_published" && to == "verified"
		if !allowed || op.Checkpoint != from {
			return ErrSiteConflict
		}
		if op.Checkpoint == "expired" || s.clock.Now() >= op.Expires {
			return ErrOperationExpired
		}
		r, e := tx.ExecContext(ctx, "UPDATE phase4_operations SET checkpoint=?,updated=? WHERE job_id=? AND checkpoint=?", to, s.clock.Now(), jid, from)
		return affected(r, e)
	})
}

// CompleteOperation publishes verified metadata, claim, job and journal in one commit.
// A done replay never recreates a consumed or expired claim.
func (s *Store) CompleteOperation(ctx context.Context, jid string, claim *JobClaim) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE phase4_operations SET updated=updated WHERE job_id=?", jid); e != nil {
			return e
		}
		op, e := loadOperation(ctx, tx, jid)
		if e != nil {
			return e
		}
		if op.Checkpoint == "done" {
			return nil
		}
		now := s.clock.Now()
		if op.Checkpoint == "expired" || now >= op.Expires {
			return ErrOperationExpired
		}
		if op.Checkpoint != "verified" && !((op.Kind == DatabaseDelete || op.Kind == DBUserDelete || op.Kind == SiteDelete) && op.Checkpoint == "db_applied") {
			return ErrSitePrecondition
		}
		if op.Kind == SiteDelete {
			if claim != nil {
				return ErrSiteInvalid
			}
			if e = checkSiteDelete(ctx, tx, op); e != nil {
				return e
			}
			if e = completeSiteDelete(ctx, tx, op); e != nil {
				return e
			}
			return finishOperation(ctx, tx, op, now, []byte(`{}`))
		}
		if op.Kind == WordPressInstall {
			return completeWordPress(ctx, tx, op, now, claim)
		}
		if e = checkOperationSite(ctx, tx, op); e != nil {
			return e
		}
		var result any = map[string]any{}
		if op.Kind != DatabaseCreate {
			if e = checkOperationMembership(ctx, tx, op); e != nil {
				return e
			}
		}
		db := op.Identity.Database
		switch op.Kind {
		case DatabaseCreate:
			_, e = tx.ExecContext(ctx, "INSERT INTO databases(id,site_id,name,primary_user_id,created) VALUES(?,?,?,?,?)", db.ID, nullableString(db.SiteID), db.Name, db.PrimaryUserID, now)
			if e != nil {
				return siteWriteError(e)
			}
			a := op.Identity.Accounts[0]
			_, e = tx.ExecContext(ctx, "INSERT INTO db_users(id,db_id,username,host,created) VALUES(?,?,?,?,?)", a.ID, db.ID, a.Username, a.Host, now)
			result = map[string]string{"db_id": db.ID, "db_name": db.Name, "db_user": a.Username}
		case DBUserCreate:
			a := op.Identity.Accounts[0]
			_, e = tx.ExecContext(ctx, "INSERT INTO db_users(id,db_id,username,host,created) VALUES(?,?,?,?,?)", a.ID, db.ID, a.Username, a.Host, now)
			result = map[string]string{"dbuser_id": a.ID}
		case DatabaseDelete:
			_, e = tx.ExecContext(ctx, "DELETE FROM databases WHERE id=?", db.ID)
		case DBUserDelete:
			_, e = tx.ExecContext(ctx, "DELETE FROM db_users WHERE id=? AND db_id=? AND id<>?", op.Identity.TargetUserID, db.ID, db.PrimaryUserID)
		case DatabaseRotate:
		default:
			return ErrSiteInvalid
		}
		if e != nil {
			return e
		}
		if operationSecrets(op.Kind) {
			if claim == nil || len(claim.Nonce) == 0 || len(claim.Ciphertext) == 0 {
				return ErrSiteInvalid
			}
			r, e := tx.ExecContext(ctx, "UPDATE job_claims SET nonce=?,ciphertext=?,expires=? WHERE job_id=? AND length(ciphertext)=0 AND claimed=0", claim.Nonce, claim.Ciphertext, now+ClaimTTLMillis, jid)
			if e = affected(r, e); e != nil {
				return e
			}
		} else if claim != nil {
			return ErrSiteInvalid
		}
		raw, e := json.Marshal(result)
		if e != nil {
			return e
		}
		return finishOperation(ctx, tx, op, now, raw)
	})
}

func finishOperation(ctx context.Context, tx *sql.Tx, op Operation, now int64, raw []byte) error {
	r, e := tx.ExecContext(ctx, `UPDATE jobs SET state='done',step='phase4_committed',progress=1,result_json=?,error=NULL,code=NULL,updated=?,finished=? WHERE id=? AND state='running'`, string(raw), now, now, op.JobID)
	if e = affected(r, e); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "UPDATE phase4_operations SET checkpoint='done',nonce=NULL,ciphertext=NULL,updated=? WHERE job_id=?", now, op.JobID)
	return e
}
