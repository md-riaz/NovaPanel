package agent

import (
	"context"

	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestStartupGateRealQueueReopen(t *testing.T) {
	ctx := context.Background()
	d := t.TempDir()
	path := filepath.Join(d, "db")
	logpath := filepath.Join(d, "audit")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	log, err := audit.Open(st, logpath)
	if err != nil {
		t.Fatal(err)
	}
	seal, _ := secret.New(make([]byte, 32))
	q := jobs.NewQueue(st, seal, log)
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	owner := jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "departed-token", Role: "token", Scopes: []string{"databases:write"}})
	r, err := q.EnqueueOperation(owner, store.OperationRequest{Kind: store.DatabaseCreate}, map[string]any{"password": "private-password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.StartJob(ctx, r.Job.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = log.Close(); err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err = audit.Open(st, logpath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	q = jobs.NewQueue(st, seal, log)
	a := &Agent{Store: st, Jobs: q}
	q.SetMutationAdmission(a.CheckMutationAdmission)
	entered, release := make(chan struct{}), make(chan struct{})
	q.SetRunner(func(c context.Context, j *jobs.Job) error {
		m, _ := jobs.MetadataFrom(c)
		if m.CreatedBy != "departed-token" || m.Role != "token" {
			t.Error("recovery elevated authority")
		}
		close(entered)
		<-release
		if e := st.CheckpointOperation(c, j.ID, "prepared", "db_applied"); e != nil {
			return e
		}
		return st.CheckpointOperation(c, j.ID, "db_applied", "verified")
	})
	done := make(chan error, 1)
	go func() {
		done <- a.PrepareStartup(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "local-root", Role: "owner"}))
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("recovery did not start")
	}
	if a.CheckMutationAdmission() == nil {
		t.Error("mutation admission opened before recovery")
	}
	if _, err = q.Enqueue(owner, "reconcile", map[string]any{}, "all"); err == nil {
		t.Error("enqueue bypass")
	}
	if _, err = q.Status(ctx, r.Job.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("recovery deadlock")
	}
	if err = a.CheckMutationAdmission(); err != nil {
		t.Fatal(err)
	}
	got, err := q.Status(ctx, r.Job.ID)
	if err != nil || got.State != "done" {
		t.Fatalf("terminal: %s %v", got.State, err)
	}
	// The worker reuses the completed startup pass and remains cancelable.
	run, cancel := context.WithCancel(ctx)
	worker := make(chan error, 1)
	go func() { worker <- q.Run(run) }()
	cancel()
	select {
	case err = <-worker:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker deadlock")
	}

}
