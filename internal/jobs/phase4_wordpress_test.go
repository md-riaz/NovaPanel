package jobs

import (
	"context"
	"database/sql"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
	"path/filepath"
	"testing"
	"time"
)

func TestEnqueueFreshWordPress(t *testing.T) {
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "session", Role: "owner", Actor: "label", Effectful: true})
	ctx = WithIdempotency(ctx, "key", "hash")
	s, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sealer, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueue(s, sealer, &fakeAudit{})
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	m := wordpress.ApprovedArtifacts()
	r := store.WordPressReservation{SiteID: "01K8WMXZF57CGNZKX1FZ38DZMA", DomainID: "01K8WMXZF57CGNZKX1FZ38DZMB", Input: wordpress.InstallInput{SitesRoot: "/scratch/sites", SiteName: "example", PrimaryDomain: "example.test", Locale: "en_US", Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}}, Evidence: wordpress.ArtifactEvidence{CLISHA256: m.CLI.SHA256, WordPressSHA256: m.WordPress.SHA256, ChecksumsSHA256: m.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: m.CLISignerFingerprint}}
	p := WordPressCredentials{DatabasePassword: "database-secret", AdminPassword: "admin-secret"}
	for i := range p.Salts {
		p.Salts[i] = "salt"
	}
	version := "8.3"
	site := store.Site{ID: r.SiteID, Name: "example", Type: "wordpress", Docroot: "/scratch/sites/example/public", PHPVersion: &version}
	domain := store.Domain{ID: r.DomainID, FQDN: "example.test"}
	out, err := q.EnqueueFreshWordPress(ctx, r, site, domain, store.PHPLimits{}, p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := q.OpenWordPressCredentials(ctx, out.Job.ID)
	if err != nil || got != p {
		t.Fatalf("credentials not bound: %v", err)
	}
	replayed, found, err := q.Replay(ctx)
	if err != nil || !found || replayed.Job.ID != out.Job.ID || replayed.ClaimToken != out.ClaimToken {
		t.Fatalf("replay: %+v %v %v", replayed, found, err)
	}
}

func TestWordPressQueueRecoveryAndClaim(t *testing.T) {
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "session", Role: "owner", Actor: "label", Effectful: true})
	path := filepath.Join(t.TempDir(), "panel.db")
	s, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	seal, _ := secret.New(make([]byte, 32))
	q := NewQueue(s, seal, &fakeAudit{})
	if e = q.PrepareOperations(ctx); e != nil {
		t.Fatal(e)
	}
	m := wordpress.ApprovedArtifacts()
	reservation := store.WordPressReservation{Input: wordpress.InstallInput{SitesRoot: "/scratch/sites", SiteName: "example", PrimaryDomain: "example.test", UID: 1001, GID: 1001, Locale: "en_US", Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}}, Evidence: wordpress.ArtifactEvidence{CLISHA256: m.CLI.SHA256, WordPressSHA256: m.WordPress.SHA256, ChecksumsSHA256: m.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: m.CLISignerFingerprint}}
	payload := WordPressCredentials{DatabasePassword: "database-secret", AdminPassword: "admin-secret"}
	for i := range payload.Salts {
		payload.Salts[i] = "salt-value"
	}
	provisionWordPressFixture(t, q, &reservation)
	r, e := q.EnqueueWordPress(ctx, reservation, payload)
	if e != nil {
		t.Fatal(e)
	}
	// A syntactically valid administrator change must not inherit sealed authority.
	if e = s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE phase4_operations SET identities_json=json_set(identities_json,'$.WordPress.Input.Administrator.Username','OtherAdmin') WHERE job_id=?`, r.Job.ID)
		return err
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = q.OpenWordPressCredentials(ctx, r.Job.ID); e == nil {
		t.Fatal("unbound administrator accepted")
	}
	if e = s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE phase4_operations SET identities_json=json_set(identities_json,'$.WordPress.Input.Administrator.Username','Admin') WHERE job_id=?`, r.Job.ID)
		return err
	}); e != nil {
		t.Fatal(e)
	}
	run, cancel := context.WithCancel(ctx)
	applied := make(chan struct{})
	effects := 0
	q.SetRunner(func(c context.Context, j *Job) error {
		got, e := q.OpenWordPressCredentials(c, j.ID)
		if e != nil {
			return e
		}
		if got != payload {
			t.Error("changed credential")
		}
		effects++
		if e = s.CheckpointOperation(c, j.ID, "prepared", "db_applied"); e != nil {
			return e
		}
		close(applied)
		<-c.Done()
		return c.Err()
	})
	done := make(chan error, 1)
	go func() { done <- q.Run(run) }()
	select {
	case <-applied:
	case <-time.After(3 * time.Second):
		t.Fatal("no runner")
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	q = NewQueue(s, seal, &fakeAudit{})
	q.SetRunner(func(c context.Context, j *Job) error {
		got, e := q.OpenWordPressCredentials(c, j.ID)
		if e != nil {
			return e
		}
		if got != payload {
			t.Error("lost sealed secrets")
		}
		authority, _ := MetadataFrom(c)
		if authority.CreatedBy != "session" || !authority.Effectful {
			t.Error("lost authority")
		}
		op, e := s.LoadOperation(c, j.ID)
		if e != nil {
			return e
		}
		if op.Checkpoint != "db_applied" {
			t.Error("lost checkpoint")
		}
		if e = s.CheckpointOperation(c, j.ID, "db_applied", "config_published"); e != nil {
			return e
		}
		return s.CheckpointOperation(c, j.ID, "config_published", "verified")
	})
	if e = s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER fail_wp_done BEFORE UPDATE OF checkpoint ON phase4_operations WHEN NEW.checkpoint='done' BEGIN SELECT RAISE(ABORT,'injected'); END`)
		return err
	}); e != nil {
		t.Fatal(e)
	}
	if e = q.PrepareOperations(ctx); e == nil {
		t.Fatal("terminal failure ignored")
	}
	opBefore, e := s.LoadOperation(ctx, r.Job.ID)
	if e != nil || opBefore.Checkpoint != "verified" || len(opBefore.Ciphertext) == 0 {
		t.Fatal("terminal rollback lost recovery", e)
	}
	if _, e = s.GetDatabase(ctx, opBefore.DatabaseID); e == nil {
		t.Fatal("database metadata escaped rollback")
	}
	if e = s.Tx(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, "DROP TRIGGER fail_wp_done"); return err }); e != nil {
		t.Fatal(e)
	}
	q.SetRunner(func(c context.Context, j *Job) error {
		op, err := s.LoadOperation(c, j.ID)
		if err == nil && op.Checkpoint != "verified" {
			t.Error("verified checkpoint lost")
		}
		return err
	})
	if e = q.PrepareOperations(ctx); e != nil {
		t.Fatal(e)
	}
	if effects != 1 {
		t.Fatal("duplicate effects", effects)
	}
	claim, e := q.Claim(ctx, r.Job.ID, r.ClaimToken)
	if e != nil {
		t.Fatal(e)
	}
	if len(claim) != 2 {
		t.Fatalf("claim projection: %v", claim)
	}
	db := claim["database"].(map[string]any)
	admin := claim["wordpress"].(map[string]any)
	if db["password"] != payload.DatabasePassword || admin["admin_password"] != payload.AdminPassword || admin["admin_username"] != "Admin" {
		t.Fatal("wrong projection")
	}
	op, e := s.LoadOperation(ctx, r.Job.ID)
	if e != nil || len(op.Ciphertext) != 0 || op.Checkpoint != "done" {
		t.Fatal("not cleaned", e)
	}
	if e = s.CompleteOperation(ctx, r.Job.ID, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = q.Claim(ctx, r.Job.ID, r.ClaimToken); e == nil {
		t.Fatal("duplicate claim")
	}
}
