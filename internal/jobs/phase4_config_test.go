package jobs

import (
	"context"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
	"novapanel/internal/wordpress/configcustody"
	"path/filepath"
	"testing"
)

func TestWordPressConfigRequiresWorkerAndIsolation(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seal, _ := secret.New(make([]byte, 32))
	q := NewQueue(s, seal, &fakeAudit{})
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "session", Role: "owner", Effectful: true})
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	m := wordpress.ApprovedArtifacts()
	r := store.WordPressReservation{Input: wordpress.InstallInput{SitesRoot: "/scratch/sites", SiteName: "example", PrimaryDomain: "example.test", UID: 23451, GID: 23452, Locale: "en_US", Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}}, Evidence: wordpress.ArtifactEvidence{CLISHA256: m.CLI.SHA256, WordPressSHA256: m.WordPress.SHA256, ChecksumsSHA256: m.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: m.CLISignerFingerprint}}
	c, err := GenerateWordPressCredentials()
	if err != nil {
		t.Fatal(err)
	}
	en, err := q.EnqueueWordPress(ctx, r, c)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	isolation := func(ctx context.Context, b configcustody.ConfigBinding, effect func(context.Context) error) error {
		called = true
		return effect(ctx)
	}
	if err = q.CreateWordPressConfig(ctx, en.Job.ID, "/missing", isolation); err == nil || called {
		t.Fatal("unlocked call reached isolation")
	}
	q.SetRunner(func(ctx context.Context, j *Job) error {
		if err := s.CheckpointOperation(ctx, j.ID, "prepared", "db_applied"); err != nil {
			return err
		}
		if err := q.CreateWordPressConfig(ctx, j.ID, "/missing", nil); err == nil {
			t.Error("missing isolation accepted")
		}
		if err := q.CreateWordPressConfig(ctx, j.ID, "/missing", func(ctx context.Context, _ configcustody.ConfigBinding, effect func(context.Context) error) error {
			_ = effect(ctx)
			return nil
		}); err == nil {
			t.Error("swallowed publication failure accepted")
		}
		return q.CreateWordPressConfig(ctx, j.ID, "/missing", isolation)
	})
	if err = q.execute(ctx, en.Job); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("worker did not reach isolation")
	}
	q.SetRunner(func(ctx context.Context, j *Job) error {
		err := q.CreateWordPressConfig(ctx, j.ID, "/missing", func(ctx context.Context, _ configcustody.ConfigBinding, effect func(context.Context) error) error {
			_ = effect(ctx)
			return nil
		})
		if err == nil {
			t.Error("isolation swallowed publication failure")
		}
		return err
	})
	if err = q.PrepareOperations(ctx); err == nil {
		t.Fatal("missing custody recovered")
	}
	op, err := s.LoadOperation(ctx, en.Job.ID)
	if err != nil || op.Checkpoint != "db_applied" || len(op.Ciphertext) == 0 {
		t.Fatal("failure lost recovery", err)
	}
}
