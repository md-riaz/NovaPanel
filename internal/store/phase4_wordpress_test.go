package store

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/wordpress"
	"path/filepath"
	"slices"
	"testing"
)

func TestPrepareFreshWordPress(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	m := wordpress.ApprovedArtifacts()
	r := WordPressReservation{SiteID: "01K8WMXZF57CGNZKX1FZ38DZMA", DomainID: "01K8WMXZF57CGNZKX1FZ38DZMB", Input: wordpress.InstallInput{SitesRoot: "/scratch/sites", SiteName: "example", PrimaryDomain: "example.test", DatabaseID: "01K8WMXZF57CGNZKX1FZ38DZMQ", DatabaseUserID: "01K8WMXZF57CGNZKX1FZ38DZMR", Locale: "en_US", Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}}, Evidence: wordpress.ArtifactEvidence{CLISHA256: m.CLI.SHA256, WordPressSHA256: m.WordPress.SHA256, ChecksumsSHA256: m.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: m.CLISignerFingerprint}}
	version := "8.3"
	site := Site{ID: r.SiteID, Name: r.Input.SiteName, Type: "wordpress", Docroot: "/scratch/sites/example/public", PHPVersion: &version}
	domain := Domain{ID: r.DomainID, FQDN: r.Input.PrimaryDomain}
	j := Job{ID: "job", Verb: "site.create", ObjectKey: "site:" + r.SiteID, CreatedBy: "session", Role: "owner", State: "queued", Args: []byte(`{}`)}
	claim := JobClaim{JobID: j.ID, ClaimHash: "hash"}
	_, err = s.PrepareFreshWordPress(ctx, j, claim, r, site, domain, PHPLimits{}, func(WordPressReservation) ([]byte, []byte, error) { return nil, nil, errors.New("seal failed") })
	if err == nil || phase4Count(t, s.db.raw, `SELECT count(*) FROM sites`) != 0 || phase4Count(t, s.db.raw, `SELECT count(*) FROM jobs`) != 0 {
		t.Fatal("failed sealing did not roll back admission")
	}
	op, err := s.PrepareFreshWordPress(ctx, j, claim, r, site, domain, PHPLimits{}, func(got WordPressReservation) ([]byte, []byte, error) {
		if got.Input.UID != 2000000 || got.Input.GID != 2000000 {
			t.Fatalf("identity: %+v", got.Input)
		}
		return []byte{1}, []byte{2}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CheckOperation(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if op.Identity.WordPress.Input.UID != 2000000 {
		t.Fatal("UID not bound")
	}
	if phase4Count(t, s.db.raw, `SELECT count(*) FROM sites s JOIN site_account_provenance p ON p.site_id=s.id JOIN jobs j ON j.id=p.allocating_job WHERE s.status='creating' AND s.lifetime_state='provisioning' AND j.verb='site.create'`) != 1 {
		t.Fatal("reservation missing")
	}
	if err = s.MarkWordPressDownloadStarted(ctx, j.ID); !errors.Is(err, ErrSitePrecondition) {
		t.Fatalf("early marker: %v", err)
	}
	if err = s.CheckpointOperation(ctx, j.ID, "prepared", "db_applied"); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkWordPressDownloadStarted(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkWordPressDownloadStarted(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	op, err = s.LoadOperation(ctx, j.ID)
	if err != nil || !op.Identity.DownloadStarted {
		t.Fatalf("marker missing after reload: %v", err)
	}
}

func TestWordPressReservationBeforeResourceRows(t *testing.T) {
	ctx := context.Background()
	s, e := Open(filepath.Join(t.TempDir(), "panel.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	manifest := wordpress.ApprovedArtifacts()
	r := OperationRequest{Kind: WordPressInstall, SiteID: "01K8WMXZF57CGNZKX1FZ38DZMA", DatabaseID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR", WordPress: &WordPressReservation{
		SiteID: "01K8WMXZF57CGNZKX1FZ38DZMA", DomainID: "01K8WMXZF57CGNZKX1FZ38DZMB",
		Input:    wordpress.InstallInput{SitesRoot: "/scratch/sites", SiteName: "example", PrimaryDomain: "example.test", UID: 1001, GID: 1001, Locale: "en_US", Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}},
		Evidence: wordpress.ArtifactEvidence{CLISHA256: manifest.CLI.SHA256, WordPressSHA256: manifest.WordPress.SHA256, ChecksumsSHA256: manifest.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: manifest.CLISignerFingerprint},
	}}
	phase4Exec(t, s.db.raw, `INSERT INTO sites(id,name,type,docroot,php_version,status,created,updated) VALUES(?,?,'wordpress',?,'8.3','active',0,0)`, r.SiteID, r.WordPress.Input.SiteName, filepath.Join(r.WordPress.Input.SitesRoot, r.WordPress.Input.SiteName, "public"))
	phase4Exec(t, s.db.raw, `INSERT INTO domains(id,site_id,fqdn,is_primary,created) VALUES(?,?,?,1,0)`, r.WordPress.DomainID, r.SiteID, r.WordPress.Input.PrimaryDomain)
	j := Job{ID: "wp-job", Verb: "wp.install", CreatedBy: "session", Role: "owner", State: "queued", Args: []byte(`{}`)}
	op, e := s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, r, []byte{1}, []byte{2})
	if e != nil {
		t.Fatal(e)
	}
	if !slices.Contains(op.Keys(), "site:"+r.SiteID) || !slices.Contains(op.Keys(), "db:"+r.DatabaseID) || !slices.Contains(op.Keys(), "dbuser:"+op.Identity.Accounts[0].Username) || op.Identity.WordPress.Input.DatabaseID != r.DatabaseID {
		t.Fatalf("reservation: %+v", op)
	}
	if _, e = s.SiteByID(ctx, r.SiteID); e != nil {
		t.Fatal("provisioned site missing", e)
	}
	if _, e = s.GetDatabase(ctx, r.DatabaseID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("DB created before runner", e)
	}
	if e = s.CheckOperation(ctx, j.ID); e != nil {
		t.Fatal(e)
	}
	j.ID = "collision"
	r.SiteID = "01K8WMXZF57CGNZKX1FZ38DZMC"
	r.WordPress.SiteID = r.SiteID
	r.DatabaseID = "01K8WMXZF57CGNZKX1FZ38DZMD"
	r.UserID = "01K8WMXZF57CGNZKX1FZ38DZME"
	_, e = s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "other"}, r, []byte{1}, []byte{2})
	if !errors.Is(e, ErrOperationLocked) {
		t.Fatalf("reservation collision: %v", e)
	}
	// A different site name must not reserve the same domain before rows exist.
	r.WordPress.Input.SiteName = "other"
	r.WordPress.DomainID = "01K8WMXZF57CGNZKX1FZ38DZMF"
	_, e = s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "other"}, r, []byte{1}, []byte{2})
	if !errors.Is(e, ErrOperationLocked) {
		t.Fatalf("primary domain reservation collision: %v", e)
	}
	r.WordPress.Input.PrimaryDomain = "other.test"
	r.WordPress.DomainID = op.Identity.WordPress.DomainID
	_, e = s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "other"}, r, []byte{1}, []byte{2})
	if !errors.Is(e, ErrOperationLocked) {
		t.Fatalf("domain ID reservation collision: %v", e)
	}
	if _, e = s.GetJob(ctx, j.ID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatalf("rejected reservation published a job: %v", e)
	}
	r.SiteID = op.SiteID
	r.WordPress.SiteID = r.SiteID
	r.DatabaseID = op.DatabaseID
	if _, e = s.StartJob(ctx, "wp-job", 1); e != nil {
		t.Fatal(e)
	}
	for _, step := range [][2]string{{"prepared", "db_applied"}, {"db_applied", "config_published"}, {"config_published", "verified"}} {
		if e = s.CheckpointOperation(ctx, "wp-job", step[0], step[1]); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.CompleteOperation(ctx, "wp-job", &JobClaim{Nonce: []byte{3}, Ciphertext: []byte{4}}); e != nil {
		t.Fatal(e)
	}
	site, e := s.SiteByID(ctx, r.SiteID)
	if e != nil || site.Type != "wordpress" || site.Status != "active" {
		t.Fatalf("site: %+v %v", site, e)
	}
	var bound string
	if e = s.db.QueryRowContext(ctx, "SELECT db_id FROM wordpress_bindings WHERE site_id=?", r.SiteID).Scan(&bound); e != nil || bound != r.DatabaseID {
		t.Fatal("binding", e)
	}
}
