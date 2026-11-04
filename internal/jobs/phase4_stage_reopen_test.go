package jobs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/audit"
	runner "novapanel/internal/exec"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
)

// This is an opt-in real-byte, real-SQLite Queue.Run test, not PHP acceptance.
func TestWordPressStageOfficialQueueReopen(t *testing.T) {
	artifacts := os.Getenv("NOVAPANEL_WP_STAGED_ARTIFACTS")
	if artifacts == "" {
		t.Skip("approved staged artifacts not supplied")
	}
	if os.Geteuid() != 0 {
		t.Skip("production stage custody requires root")
	}
	p := wordpress.StagedPaths{CLI: filepath.Join(artifacts, "cli.phar"), Signature: filepath.Join(artifacts, "cli.asc"), Keyring: filepath.Join(artifacts, "key.gpg"), Archive: filepath.Join(artifacts, "wordpress.zip"), Checksums: filepath.Join(artifacts, "checksums.json"), VerifierHome: filepath.Join(artifacts, "gnupg")}
	root := t.TempDir()
	custody := filepath.Join(root, "stages")
	if err := os.Mkdir(custody, 0700); err != nil {
		t.Fatal(err)
	}
	dbpath := filepath.Join(root, "panel.db")
	s, err := store.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	a, err := audit.Open(s, filepath.Join(root, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.Close() }()
	seal, _ := secret.New(make([]byte, 32))
	q := NewQueue(s, seal, a)
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "session", Role: "owner", Effectful: true})
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	evidence, err := wordpress.VerifyStaged(ctx, p, runner.Real{})
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := GenerateWordPressCredentials()
	if err != nil {
		t.Fatal(err)
	}
	reservation := store.WordPressReservation{Input: wordpress.InstallInput{SitesRoot: "/scratch/sites", SiteName: "example", PrimaryDomain: "example.test", UID: 23451, GID: 23452, Locale: "en_US", Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}}, Evidence: evidence}
	en, err := q.EnqueueWordPress(ctx, reservation, credentials)
	if err != nil {
		t.Fatalf("enqueue %q: %v", root, err)
	}
	reached := make(chan error, 1)
	q.SetRunner(func(ctx context.Context, j *Job) error {
		if err := q.StageWordPress(ctx, j.ID, custody, p, runner.Real{}); err == nil {
			t.Error("prepared operation staged before DB checkpoint")
		}
		// Injected DB completion, not a claim of actual MariaDB provisioning.
		if err := s.CheckpointOperation(ctx, j.ID, "prepared", "db_applied"); err != nil {
			return err
		}
		err := q.StageWordPress(ctx, j.ID, custody, p, runner.Real{})
		reached <- err
		if err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	})
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- q.Run(run) }()
	select {
	case err = <-reached:
		if err != nil {
			cancel()
			<-done
			t.Fatal(err)
		}
	case <-time.After(150 * time.Second):
		cancel()
		<-done
		t.Fatal("stage not reached")
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	stageFile := filepath.Join(custody, en.Job.ID, "tree", "wp-includes", "version.php")
	first, err := os.Stat(stageFile)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	a, err = audit.Open(s, filepath.Join(root, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	q = NewQueue(s, seal, a)
	reached = make(chan error, 1)
	q.SetRunner(func(ctx context.Context, j *Job) error {
		if j.ID != en.Job.ID {
			t.Error("replacement operation used")
		}
		err := q.StageWordPress(ctx, j.ID, custody, p, runner.Real{})
		reached <- err
		if err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	})
	run, cancel = context.WithCancel(ctx)
	go func() { done <- q.Run(run) }()
	select {
	case err = <-reached:
	case <-time.After(150 * time.Second):
		cancel()
		<-done
		t.Fatal("restart not reached")
	}
	cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if err != nil {
		t.Fatal(err)
	}
	again, err := os.Stat(stageFile)
	if err != nil || !os.SameFile(first, again) {
		t.Fatal("restart replaced owned file", err)
	}
	op, err := s.LoadOperation(ctx, en.Job.ID)
	if err != nil || op.Checkpoint != "db_applied" || len(op.Ciphertext) == 0 {
		t.Fatal("stage invented checkpoint or destroyed input", err)
	}
	j, err := s.GetJob(ctx, en.Job.ID)
	if err != nil || j.State == "done" {
		t.Fatal("stage published installation", err)
	}
	t.Log("real gpgv, canonical archive, original Queue.Run job and SQLite/audit reopen passed; no PHP or live-docroot publication")
}
