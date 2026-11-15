package jobs

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"novapanel/internal/database"
	"novapanel/internal/exec"
	"novapanel/internal/store"
)

type reconcileSQL struct{ listings atomic.Int32 }

func (r *reconcileSQL) Run(_ context.Context, _ string, _ []string, o exec.RunOpts) ([]byte, []byte, int, error) {
	b, _ := io.ReadAll(o.Stdin)
	s := string(b)
	switch {
	case strings.Contains(s, "FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='novapanel'"):
		return []byte("1\n"), nil, 0, nil
	case strings.HasPrefix(s, "SELECT CONCAT("):
		return []byte("1:5:5:2:2:0:0:0\n"), nil, 0, nil
	case strings.HasPrefix(s, "SELECT (\n"):
		return []byte("0\n"), nil, 0, nil
	case strings.HasPrefix(s, "SELECT kind,name,obj_id"):
		r.listings.Add(1)
		return []byte("schema\tschema\t01ARZ3NDEKTSV4RRFFQ69G5FAV\n"), nil, 0, nil
	case strings.HasPrefix(s, "SELECT COALESCE"):
		return []byte("none\t0\n"), nil, 0, nil
	}
	return nil, nil, 0, nil
}

func TestReconcileContendedLockYieldsWorker(t *testing.T) {
	for _, key := range []string{"parent", "namespace"} {
		t.Run(key, func(t *testing.T) { testReconcileContendedLock(t, key) })
	}
}

func testReconcileContendedLock(t *testing.T, key string) {
	q, s, _, ctx := fixture(t)
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	args, _ := json.Marshal(map[string]string{"db_id": id, "name": "schema"})
	if err := s.CreateJob(ctx, store.Job{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAW", Verb: "database.create", ObjectKey: "db:" + id, State: "failed", Args: args}); err != nil {
		t.Fatal(err)
	}
	rec, err := q.Enqueue(WithMetadata(ctx, Metadata{Actor: "root", Role: "owner", CreatedBy: "local-root", Effectful: true}), "reconcile", nil, "system")
	if err != nil {
		t.Fatal(err)
	}
	later, err := q.Enqueue(ctx, "test.later", nil, "other")
	if err != nil {
		t.Fatal(err)
	}
	var release func()
	if key == "parent" {
		release, err = q.LockDatabase(ctx, id)
	} else {
		release, err = q.LockDatabaseName(ctx, "schema")
	}
	if err != nil {
		t.Fatal(err)
	}
	held := true
	defer func() {
		if held {
			release()
		}
	}()
	runner := &reconcileSQL{}
	type checkpoint struct {
		job store.Job
		err error
	}
	laterRan := make(chan checkpoint, 1)
	q.SetRunner(func(c context.Context, j *Job) error {
		if j.ID == rec.Job.ID {
			_, err := database.Reconcile(c, runner, s, q.LockReconcileDatabase)
			return err
		}
		if j.ID == later.Job.ID {
			// The single worker cannot start another reconcile attempt while this
			// runner is active, so this observes the preceding yield safely.
			job, err := s.GetJob(c, rec.Job.ID)
			laterRan <- checkpoint{job, err}
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
	select {
	case observed := <-laterRan:
		if observed.err != nil || observed.job.State != "queued" || observed.job.Code != "" || observed.job.Finished != nil || observed.job.Attempts < 1 || runner.listings.Load() < 1 {
			t.Fatalf("yielded job: %+v %v", observed.job, observed.err)
		}
	case <-time.After(time.Second):
		t.Fatal("reconcile blocked unrelated job")
	}
	time.Sleep(250 * time.Millisecond)
	if n := runner.listings.Load(); n > 5 {
		t.Fatalf("retry spun: %d", n)
	}
	// The worker must stop promptly even while the lock remains held. A restart
	// then retries the durable queued attempt with the same provenance checks.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not cancel")
	}
	worker, cancel = context.WithCancel(ctx)
	defer cancel()
	done = make(chan error, 1)
	go func() { done <- q.Run(worker) }()
	release()
	held = false
	deadline := time.After(2 * time.Second)
	for {
		j, err := s.GetJob(ctx, rec.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == "done" {
			break
		}
		if j.State == "failed" {
			t.Fatalf("reconcile failed: %+v", j)
		}
		select {
		case <-deadline:
			t.Fatal("reconcile did not retry")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
