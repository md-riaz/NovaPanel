package store

import (
	"context"
	"path/filepath"
	"testing"

	"novapanel/internal/wordpress"
)

func TestFreshWordPressRollbackIntentSurvivesRecovery(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := wordpress.ApprovedArtifacts()
	r := WordPressReservation{SiteID: "01K8WMXZF57CGNZKX1FZ38DZMA", DomainID: "01K8WMXZF57CGNZKX1FZ38DZMB", Input: wordpress.InstallInput{SitesRoot: "/sites", SiteName: "example", PrimaryDomain: "example.test", DatabaseID: "01K8WMXZF57CGNZKX1FZ38DZMQ", DatabaseUserID: "01K8WMXZF57CGNZKX1FZ38DZMR", Locale: m.Locale, Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}}, Evidence: wordpress.ArtifactEvidence{CLISHA256: m.CLI.SHA256, WordPressSHA256: m.WordPress.SHA256, ChecksumsSHA256: m.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: m.CLISignerFingerprint}}
	v := "8.3"
	site := Site{ID: r.SiteID, Name: "example", Type: "wordpress", Docroot: "/sites/example/public", PHPVersion: &v}
	domain := Domain{ID: r.DomainID, FQDN: "example.test"}
	j := Job{ID: "rollback-job", Verb: "site.create", ObjectKey: "site:" + r.SiteID, CreatedBy: "owner", Role: "owner", State: "queued", Args: []byte(`{}`)}
	op, err := s.PrepareFreshWordPress(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, r, site, domain, PHPLimits{}, func(WordPressReservation) ([]byte, []byte, error) { return []byte{1}, []byte{2}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartJob(ctx, j.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.BeginFreshWordPressRollback(ctx, j.ID, "agent_error"); err != nil {
		t.Fatal(err)
	}
	if err = s.BeginFreshWordPressRollback(ctx, j.ID, "agent_error"); err != nil {
		t.Fatal("non-idempotent intent", err)
	}
	got, err := s.GetJob(ctx, j.ID)
	if err != nil || got.Step != FreshWordPressRollbackStep || got.State != "running" {
		t.Fatalf("job: %+v %v", got, err)
	}
	gotSite, err := s.SiteByID(ctx, r.SiteID)
	if err != nil || gotSite.Status != "failed" {
		t.Fatalf("site: %+v %v", gotSite, err)
	}
	s.clock = &authClock{now: op.Expires + 1}
	if n, err := s.ExpireOperations(ctx); err != nil || n != 0 {
		t.Fatalf("swept rollback: %d %v", n, err)
	}
	if err = s.RecoverJobs(ctx, s.clock.Now()); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetJob(ctx, j.ID)
	if err != nil || got.Step != FreshWordPressRollbackStep || got.State != "queued" {
		t.Fatalf("lost intent on recovery: %+v %v", got, err)
	}
	if phase4Count(t, s.db.raw, `SELECT count(*) FROM site_account_provenance WHERE site_id IS NOT NULL AND uid=2000000`) != 1 {
		t.Fatal("provenance retired before teardown")
	}
}
