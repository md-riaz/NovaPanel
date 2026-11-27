package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runner "novapanel/internal/exec"
	"novapanel/internal/rpcerr"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
)

// This fixture starts after admission; production site.create supplies that state later.
func TestFreshLifetimeSQLiteActivationReplay(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	jid, siteID, domainID := "01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW", "01ARZ3NDEKTSV4RRFFQ69G5FAX"
	dbID, userID := "01ARZ3NDEKTSV4RRFFQ69G5FAY", "01ARZ3NDEKTSV4RRFFQ69G5FAZ"
	manifest := wordpress.ApprovedArtifacts()
	r := store.WordPressReservation{SiteID: siteID, DomainID: domainID, Input: wordpress.InstallInput{SitesRoot: "/sites", SiteName: "example", PrimaryDomain: "example.test", UID: 123, GID: 456, DatabaseID: dbID, DatabaseUserID: userID, Locale: "en_US", Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}}, Evidence: wordpress.ArtifactEvidence{CLISHA256: manifest.CLI.SHA256, WordPressSHA256: manifest.WordPress.SHA256, ChecksumsSHA256: manifest.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: manifest.CLISignerFingerprint}}
	ident := store.OperationIdentity{Version: 1, Database: store.OperationDatabaseRecord{ID: dbID, SiteID: siteID, Name: "d_" + strings.ToLower(dbID), Engine: "mariadb", PrimaryUserID: userID}, Accounts: []store.OperationAccount{{ID: userID, Username: "u_" + strings.ToLower(userID), Host: "127.0.0.1"}}, WordPress: &r}
	identJSON, err := json.Marshal(ident)
	if err != nil {
		t.Fatal(err)
	}
	credentials := WordPressCredentials{DatabasePassword: "db-secret", AdminPassword: "admin-secret"}
	for i := range credentials.Salts {
		credentials.Salts[i] = "salt"
	}
	seal, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	authority := Metadata{Actor: "fixture", Role: "owner", CreatedBy: "session", Effectful: true}
	raw, err := json.Marshal(wordPressInput{Version: 1, JobID: jid, SiteID: siteID, DatabaseID: dbID, Owner: "session", Authority: authority, Credentials: credentials, Reservation: r})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := seal.Seal(raw)
	clear(raw)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		queries := []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO sites(id,name,type,docroot,status,created,updated,lifetime_state) VALUES(?,'example','wordpress','/sites/example/public','creating',1,1,'provisioning')`, []any{siteID}},
			{`INSERT INTO domains(id,site_id,fqdn,is_primary,created) VALUES(?,?,'example.test',1,1)`, []any{domainID, siteID}},
			{`INSERT INTO jobs(id,verb,args_json,object_key,effectful,actor,role,created_by,state,created,updated) VALUES(?,'wp.install','{}',?,1,'fixture','owner','session','running',1,1)`, []any{jid, "site:" + siteID}},
			{`INSERT INTO site_account_provenance(site_id,account_name,uid,gid,allocating_job,evidence) VALUES(?,'example',123,456,?,'owned')`, []any{siteID, jid}},
			{`INSERT INTO phase4_operations(job_id,kind,site_id,db_id,checkpoint,identities_json,nonce,ciphertext,expires,updated) VALUES(?,'wordpress_install',?,?,'db_applied',?,?,?,?,1)`, []any{jid, siteID, dbID, string(identJSON), sealed.Nonce, sealed.Ciphertext, time.Now().Add(time.Hour).UnixMilli()}},
			{`INSERT INTO job_claims(job_id,claim_hash,nonce,ciphertext,expires) VALUES(?,'fixture',x'',x'',0)`, []any{jid}},
		}
		for _, q := range queries {
			if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueue(s, seal, &fakeAudit{})
	op, err := s.LoadOperation(ctx, jid)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckOperation(ctx, jid); err != nil {
		t.Fatalf("fresh check: %v", err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE domains SET fqdn='changed.test' WHERE id=?`, domainID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckOperation(ctx, jid); !errors.Is(err, store.ErrSitePrecondition) {
		t.Fatalf("changed domain: %v", err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE domains SET fqdn='example.test' WHERE id=?`, domainID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	identity, err := s.LoadSiteLifetime(ctx, siteID)
	if err != nil {
		t.Fatal(err)
	}
	held := map[string]bool{}
	for _, key := range op.Keys() {
		held[key] = true
	}
	workerBase, cancel := context.WithCancel(context.WithValue(ctx, heldLocksKey{}, held))
	defer cancel()
	lease := &configWorkerLease{queue: q, job: jid, ctx: workerBase, cancel: cancel}
	lease.active.Store(true)
	worker := context.WithValue(workerBase, configWorkerKey{}, lease)
	verify := func(context.Context, string, store.SiteLifetime) error { return nil }
	var domain *fakeInstallLifetime
	create := func(got string) (installLifetimeDomain, error) {
		if got != siteID {
			t.Fatal("changed domain identity")
		}
		domain = &fakeInstallLifetime{held: true, empty: true}
		return domain, nil
	}
	operation := func(l *WordPressLifetime) error {
		for _, kind := range []wordpress.StepKind{wordpress.Download, wordpress.VerifyChecksums, wordpress.CreateConfig} {
			child := reviewedInstallChild{kind: kind}
			if kind == wordpress.CreateConfig {
				child.options.Stdin = strings.NewReader("db-secret")
			}
			if _, _, _, err := l.runReviewed(worker, child); err != nil {
				return err
			}
		}
		if err := l.ConfigIsolation(worker, l.binding, func(context.Context) error { return nil }); err != nil {
			return err
		}
		if err := s.CheckpointOperation(worker, jid, "db_applied", "config_published"); err != nil {
			return err
		}
		if _, _, _, err := l.runReviewed(worker, reviewedInstallChild{kind: wordpress.InstallCore, options: runner.RunOpts{Stdin: strings.NewReader("admin-secret")}}); err != nil {
			return err
		}
		return s.CheckpointOperation(worker, jid, "config_published", "verified")
	}
	err = q.withFreshWordPressLifetime(worker, jid, identity, operation, func(*WordPressLifetime) error {
		if !domain.closed {
			t.Error("serving before drain")
		}
		if err := s.CheckActivatedSiteLifetime(ctx, identity, jid); err != nil {
			t.Errorf("serving before committed activation: %v", err)
		}
		return errors.New("injected failure after activation")
	}, verify, create)
	if err == nil {
		t.Fatal("interruption ignored")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q = NewQueue(s, seal, &fakeAudit{})
	lease.queue = q
	identity, err = s.LoadSiteLifetime(ctx, siteID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckOperation(ctx, jid); err != nil {
		t.Fatalf("replay check: %v", err)
	}
	released := false
	err = q.withFreshWordPressLifetime(worker, jid, identity, func(*WordPressLifetime) error { t.Error("replayed installation effects"); return nil }, func(*WordPressLifetime) error { released = true; return nil }, verify, create)
	if err != nil || !released {
		t.Fatalf("replay release: %v, released=%v", err, released)
	}
	if err := s.CompleteOperation(ctx, jid, &store.JobClaim{Nonce: []byte{1}, Ciphertext: []byte{2}}); err != nil {
		t.Fatalf("finish: %v", err)
	}
	job, err := s.GetJob(ctx, jid)
	if err != nil || job.State != "done" {
		t.Fatalf("job: %+v %v", job, err)
	}
	if err := s.CheckActivatedSiteLifetime(ctx, identity, jid); !errors.Is(err, store.ErrSitePrecondition) {
		t.Fatalf("terminal re-entry: %v", err)
	}
	var coded *rpcerr.CodedError
	err = q.withFreshWordPressLifetime(worker, jid, identity, operation, func(*WordPressLifetime) error { return nil }, verify, create)
	if !errors.As(err, &coded) || coded.Code != "precondition_failed" {
		t.Fatalf("terminal owner re-entry: %v", err)
	}
}
