package jobs

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestAttemptDeadlineBoundsLockWait(t *testing.T) {
	q, s, _, ctx := fixture(t)
	r, err := q.Enqueue(ctx, "test.slow", nil, "object")
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := q.locks.lock(ctx, "object")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	q.SetAttemptDeadline(func(string) time.Duration { return 10 * time.Millisecond })
	if err := q.execute(ctx, r.Job); err != nil {
		t.Fatal(err)
	}
	j, err := s.GetJob(ctx, r.Job.ID)
	if err != nil || j.State != "queued" || j.Attempts != 0 {
		t.Fatalf("lock timeout: %+v %v", j, err)
	}
}

func TestWorkerDeadlineRequeuesDatabaseIntentUnchanged(t *testing.T) {
	q, s, _, ctx := fixture(t)
	r, err := q.EnqueueDatabase(ctx, "database.create", map[string]string{"name": "example"}, "db:example", "preserve", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	nonce, ciphertext, err := s.DatabaseIntentSecret(ctx, r.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	q.SetAttemptDeadline(func(string) time.Duration { return 25 * time.Millisecond })
	second := make(chan Job, 1)
	release := make(chan struct{})
	q.SetRunner(func(ctx context.Context, j *Job) error {
		if j.Attempts == 1 {
			<-ctx.Done()
			return ctx.Err()
		}
		second <- *j
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- q.Run(workerCtx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case j := <-second:
		n, c, e := s.DatabaseIntentSecret(ctx, j.ID)
		if e != nil || !bytes.Equal(nonce, n) || !bytes.Equal(ciphertext, c) || !bytes.Equal(j.Args, r.Job.Args) {
			t.Fatalf("database intent changed on retry: %v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not retry")
	}
	close(release)
	deadline := time.After(5 * time.Second)
	for {
		j, err := s.GetJob(ctx, r.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == "done" && j.Attempts == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("retry did not finish: %+v", j)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestAttemptDeadlineRequeuesSealedInput(t *testing.T) {
	q, s, _, ctx := fixture(t)
	r, err := q.EnqueueSealed(ctx, "test.slow", map[string]string{"secret": "preserve"}, "object")
	if err != nil {
		t.Fatal(err)
	}
	q.SetAttemptDeadline(func(string) time.Duration { return 10 * time.Millisecond })
	q.SetRunner(func(ctx context.Context, _ *Job) error { <-ctx.Done(); return ctx.Err() })
	if err = q.execute(ctx, r.Job); err != nil {
		t.Fatal(err)
	}
	j, err := s.GetJob(ctx, r.Job.ID)
	if err != nil || j.State != "queued" || !bytes.Equal(j.Args, r.Job.Args) {
		t.Fatalf("timeout lost retry state: %+v %v", j, err)
	}
	q.SetRunner(func(ctx context.Context, _ *Job) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("missing fresh deadline")
		}
		return nil
	})
	if err = q.execute(ctx, j); err != nil {
		t.Fatal(err)
	}
	j, err = s.GetJob(ctx, j.ID)
	if err != nil || j.State != "done" || j.Attempts != 2 {
		t.Fatalf("retry: %+v %v", j, err)
	}
}
