package store

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/wordpress"
	"path/filepath"
	"testing"
)

func TestWordPressReservationBeforeResourceRows(t *testing.T) {
	ctx := context.Background()
	s, e := Open(filepath.Join(t.TempDir(), "panel.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	manifest := wordpress.ApprovedArtifacts()
	r := OperationRequest{Kind: WordPressInstall, SiteID: "01K8WMXZF57CGNZKX1FZ38DZMA", DatabaseID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR", WordPress: &WordPressReservation{
		DomainID: "01K8WMXZF57CGNZKX1FZ38DZMB",
		Input:    wordpress.InstallInput{SitesRoot: "/scratch/sites", SiteName: "example", PrimaryDomain: "example.test", UID: 1001, GID: 1001, Locale: "en_US", Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}},
		Evidence: wordpress.ArtifactEvidence{CLISHA256: manifest.CLI.SHA256, WordPressSHA256: manifest.WordPress.SHA256, ChecksumsSHA256: manifest.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: manifest.CLISignerFingerprint},
	}}
	j := Job{ID: "wp-job", Verb: "site.create", CreatedBy: "session", Role: "owner", State: "queued", Args: []byte(`{}`)}
	op, e := s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, r, []byte{1}, []byte{2})
	if e != nil {
		t.Fatal(e)
	}
	if len(op.Keys()) != 6 || op.Identity.WordPress.Input.DatabaseID != r.DatabaseID {
		t.Fatalf("reservation: %+v", op)
	}
	if _, e = s.SiteByID(ctx, r.SiteID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("site created before runner", e)
	}
	if _, e = s.GetDatabase(ctx, r.DatabaseID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("DB created before runner", e)
	}
	if e = s.CheckOperation(ctx, j.ID); e != nil {
		t.Fatal(e)
	}
	j.ID = "collision"
	r.SiteID = "01K8WMXZF57CGNZKX1FZ38DZMC"
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
