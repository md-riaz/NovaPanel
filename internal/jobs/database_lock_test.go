package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDatabaseLockSharesWorkerKeyAndHonorsContext(t *testing.T) {
	q, _, _, ctx := fixture(t)
	unlock, err := q.LockDatabase(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	wait, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if release, err := q.locks.lock(wait, "db:example"); !errors.Is(err, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		t.Fatalf("lock wait: %v", err)
	}
}

func TestPostRecoveryRunsAfterF6BeforeWork(t *testing.T) {
	q, s, _, ctx := fixture(t)
	r, err := q.Enqueue(ctx, "test.slow", nil, "object")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartJob(ctx, r.Job.ID, q.clock.Now()); err != nil {
		t.Fatal(err)
	}
	called := false
	q.SetPostRecovery(func(ctx context.Context) error {
		j, err := s.GetJob(ctx, r.Job.ID)
		if err != nil {
			return err
		}
		if j.State != "queued" {
			t.Errorf("recovery state = %s", j.State)
		}
		called = true
		return errors.New("stop after recovery")
	})
	q.SetRunner(func(context.Context, *Job) error {
		t.Error("job ran before hook")
		return nil
	})
	if err = q.Run(ctx); !errors.Is(err, ErrRecoveryBlocked) || !called {
		t.Fatalf("hook: called=%v err=%v", called, err)
	}
}
