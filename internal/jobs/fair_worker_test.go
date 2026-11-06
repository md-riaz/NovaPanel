package jobs

import (
	"context"
	"novapanel/internal/store"
	"testing"
	"time"
)

func TestFairWorkerSkipsRetryAndContendedSecondary(t *testing.T) {
	q, _, _, ctx := fixture(t)
	first, err := q.Enqueue(ctx, "test.retry", nil, "one")
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := q.Enqueue(ctx, "dbuser.create", map[string]string{"username": "shared"}, "db:blocked")
	if err != nil {
		t.Fatal(err)
	}
	last, err := q.Enqueue(ctx, "test.later", nil, "other")
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := q.LockDatabaseUser(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	seen := make(chan string, 20)
	q.SetRunner(func(_ context.Context, j *Job) error {
		seen <- j.ID
		if j.ID == first.Job.ID {
			return ErrRetry
		}
		return nil
	})
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- q.Run(worker) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for _, want := range []string{first.Job.ID, last.Job.ID} {
		select {
		case got := <-seen:
			if got != want {
				t.Fatalf("got %s want %s (blocked %s)", got, want, blocked.Job.ID)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("worker starved later job")
		}
	}
}

// Every invocation admits a replacement, keeping every live page full. The
// original yielder must still be retried at the start of the next pass.
func TestFairWorkerPassEndsUnderContinuousAdmissions(t *testing.T) {
	q, _, _, ctx := fixture(t)
	first, err := q.Enqueue(ctx, "test.retry", nil, "first")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < store.QueuedJobsPageSize; i++ {
		if _, err := q.Enqueue(ctx, "test.later", nil, "later"); err != nil {
			t.Fatal(err)
		}
	}
	worker, cancel := context.WithCancel(ctx)
	retried := make(chan struct{})
	attempts := 0
	q.SetRunner(func(_ context.Context, j *Job) error {
		if j.ID == first.Job.ID {
			attempts++
			if attempts == 2 {
				close(retried)
				cancel()
				return nil
			}
		}
		if _, err := q.Enqueue(ctx, "test.new", nil, "new"); err != nil {
			return err
		}
		if j.ID == first.Job.ID {
			return ErrRetry
		}
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- q.Run(worker) }()
	select {
	case <-retried:
	case <-time.After(5 * time.Second):
		cancel()
		t.Error("continuous admissions prevented retry")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDeleteEnumeratesCurrentUsersUnderParent(t *testing.T) {
	q, s, _, ctx := fixture(t)
	d := store.Database{ID: "dbid", Name: "schema", Engine: "mariadb"}
	if err := s.ReserveDatabase(ctx, d, store.DatabaseUser{ID: "owner", DatabaseID: d.ID, Username: "owner"}); err != nil {
		t.Fatal(err)
	}
	r, err := q.Enqueue(ctx, "database.delete", map[string]string{"db_id": d.ID}, "db:"+d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveDatabaseUser(ctx, store.DatabaseUser{ID: "later", DatabaseID: d.ID, Username: "later"}); err != nil {
		t.Fatal(err)
	}
	release, err := q.LockDatabaseUser(ctx, "later")
	if err != nil {
		t.Fatal(err)
	}
	q.SetRunner(func(context.Context, *Job) error { t.Error("ran with contended dynamic user"); return nil })
	worked, err := q.executeEligible(ctx, r.Job)
	if err != nil || worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	release()
	q.SetRunner(func(ctx context.Context, _ *Job) error {
		unlock, err := q.LockDatabaseUser(ctx, "later")
		if err != nil {
			return err
		}
		unlock()
		return nil
	})
	worked, err = q.executeEligible(ctx, r.Job)
	if err != nil || !worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
}

func TestEligibleSecondaryLockHandoff(t *testing.T) {
	q, _, _, ctx := fixture(t)
	r, err := q.Enqueue(ctx, "database.create", map[string]string{"name": "schema", "username": "user"}, "db:parent")
	if err != nil {
		t.Fatal(err)
	}
	q.SetRunner(func(ctx context.Context, _ *Job) error {
		for _, fn := range []func(context.Context) (func(), error){func(ctx context.Context) (func(), error) { return q.LockDatabaseName(ctx, "schema") }, func(ctx context.Context) (func(), error) { return q.LockDatabaseUser(ctx, "user") }} {
			release, err := fn(ctx)
			if err != nil {
				return err
			}
			release()
		}
		if _, ok := q.locks.tryLock("dbuser:user"); ok {
			t.Error("secondary lock released during handoff")
		}
		return nil
	})
	worked, err := q.executeEligible(ctx, r.Job)
	if err != nil || !worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
}
