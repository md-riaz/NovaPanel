package jobs

import (
	"context"
	"path/filepath"
	"testing"

	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
	"novapanel/internal/wordpress/configcustody"
)

func TestConfigPreEffectPanicExpiresOriginalOperation(t *testing.T) {
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "local-root", Role: "owner", Effectful: true})
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	seal, _ := secret.New(make([]byte, 32))
	q := NewQueue(s, seal, &fakeAudit{})
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	m := wordpress.ApprovedArtifacts()
	r := store.WordPressReservation{Input: wordpress.InstallInput{SitesRoot: "/scratch/sites", SiteName: "example", PrimaryDomain: "example.test", UID: 23451, GID: 23452, Locale: "en_US", Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}}, Evidence: wordpress.ArtifactEvidence{CLISHA256: m.CLI.SHA256, WordPressSHA256: m.WordPress.SHA256, ChecksumsSHA256: m.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: m.CLISignerFingerprint}}
	credentials, err := GenerateWordPressCredentials()
	if err != nil {
		t.Fatal(err)
	}
	en, err := q.EnqueueWordPress(ctx, r, credentials)
	if err != nil {
		t.Fatal(err)
	}
	q.SetRunner(func(ctx context.Context, j *Job) error {
		if err := s.CheckpointOperation(ctx, j.ID, "prepared", "db_applied"); err != nil {
			return err
		}
		return q.CreateWordPressConfig(ctx, j.ID, "/missing", func(context.Context, configcustody.ConfigBinding, func(context.Context) error) error {
			panic("secret pre-effect failure")
		})
	})
	if err = q.execute(ctx, en.Job); err != nil {
		t.Fatal(err)
	}
	op, err := s.LoadOperation(ctx, en.Job.ID)
	if err != nil || op.Checkpoint != "expired" || len(op.Ciphertext) != 0 {
		t.Fatal("pre-effect panic retained inputs", err)
	}
	j, err := s.GetJob(ctx, en.Job.ID)
	if err != nil || j.State != "failed" || j.Code != "agent_error" {
		t.Fatal("wrong terminal result", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	q = NewQueue(s, seal, &fakeAudit{})
	q.SetRunner(func(context.Context, *Job) error { t.Fatal("terminal operation reran"); return nil })
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	j, err = s.GetJob(ctx, en.Job.ID)
	if err != nil || j.State != "failed" || j.Code != "agent_error" {
		t.Fatal("terminal result changed on reopen", err)
	}
}
