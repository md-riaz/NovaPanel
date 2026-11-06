package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/audit"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

// Requeue here represents an effect-specific reconciliation decision, not an
// automatic retry policy for effectful jobs.
func TestInterruptedEffectfulSealedInputClaimRecovery(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	log, err := audit.Open(s, filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	seal, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueue(s, seal, log)
	ctx := WithMetadata(context.Background(), Metadata{Actor: "root", Role: "owner", CreatedBy: "local-root", Effectful: true})
	const marker = "effectful-private-secret-regression-marker"
	r, err := q.EnqueueSealed(ctx, "test.effectful", map[string]string{"password": marker}, "system")
	if err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), r.Job.Args...)
	if bytes.Contains(original, []byte(marker)) {
		t.Fatal("plaintext in sealed input")
	}

	// The first real worker attempt reaches its effect and is interrupted by shutdown.
	firstCtx, cancelFirst := context.WithCancel(ctx)
	q.SetRunner(func(run context.Context, j *Job) error {
		raw, e := q.OpenInput(j)
		if e != nil {
			return e
		}
		defer clear(raw)
		if !bytes.Contains(raw, []byte(marker)) {
			return errors.New("first attempt lost input")
		}
		cancelFirst()
		return run.Err()
	})
	if err = q.execute(firstCtx, r.Job); err != nil {
		t.Fatal(err)
	}
	j, err := s.GetJob(ctx, r.Job.ID)
	if err != nil || j.State != "running" || j.Attempts != 1 || !bytes.Equal(j.Args, original) {
		t.Fatalf("interrupted attempt: state=%s attempts=%d err=%v", j.State, j.Attempts, err)
	}

	// Startup must first mark it interrupted, without blindly running it. An
	// explicit reconciliation decision requeues that same durable job.
	reconciled := false
	q.SetPostRecovery(func(c context.Context) error {
		j, e := s.GetJob(c, r.Job.ID)
		if e != nil {
			return e
		}
		if j.State != "failed" || j.Code != "interrupted" || j.Attempts != 1 || !bytes.Equal(j.Args, original) {
			return errors.New("bad recovery state or input")
		}
		reconciled = true
		j.State, j.Code, j.Error, j.Finished = "queued", "", "", nil
		return s.UpdateJob(c, j)
	})
	published := make(chan error, 1)
	q.SetRunner(func(c context.Context, j *Job) error {
		if !bytes.Equal(j.Args, original) {
			return errors.New("requeued ciphertext changed")
		}
		raw, e := q.OpenInput(j)
		if e != nil {
			return e
		}
		defer clear(raw)
		var input map[string]string
		if e = json.Unmarshal(raw, &input); e != nil {
			return e
		}
		if input["password"] != marker {
			return errors.New("requeued plaintext changed")
		}
		e = q.PublishClaim(c, j.ID, map[string]any{"password": input["password"]})
		published <- e
		return e
	})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- q.Run(runCtx) }()
	defer func() { cancel(); <-done }()
	select {
	case err = <-published:
		if err != nil {
			t.Fatal(err)
		}
	case err = <-done:
		done <- err
		t.Fatalf("worker stopped before publication: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not publish")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, err = s.GetJob(ctx, r.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not finish: %s", j.State)
		}
		time.Sleep(time.Millisecond)
	}
	if !reconciled || j.Attempts != 2 || string(j.Args) != "{}" {
		t.Fatal("recovery, retry, or terminal cleanup failed")
	}
	fields, err := q.Claim(ctx, j.ID, r.ClaimToken)
	if err != nil || fields["password"] != marker {
		t.Fatal("claim did not return original secret", err)
	}
	if _, err = q.Claim(ctx, j.ID, r.ClaimToken); err == nil {
		t.Fatal("claim was reusable")
	}
	if err = q.PublishClaim(ctx, j.ID, map[string]any{"password": marker}); err == nil {
		t.Fatal("consumed claim was republished")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, e := os.ReadFile(filepath.Join(dir, entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(data, []byte(marker)) || bytes.Contains(data, []byte(r.ClaimToken)) {
			t.Fatalf("secret or claim token leaked into %s", entry.Name())
		}
	}
}
