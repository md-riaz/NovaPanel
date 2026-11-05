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
