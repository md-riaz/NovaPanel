package jobs

import (
	"context"
	"encoding/json"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestOperationRealQueueCrashReopenClaimProjection(t *testing.T) {
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "session-owner", Role: "owner"})
	path := filepath.Join(t.TempDir(), "queue.db")
	s, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	seal, _ := secret.New(make([]byte, 32))
	q := NewQueue(s, seal, &fakeAudit{})
	r, e := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.DatabaseCreate}, map[string]any{"password": "generated-password", "salts": "must-not-be-delivered", "protected_config": "private-config"})
	if e != nil {
		t.Fatal(e)
	}
	run, cancel := context.WithCancel(ctx)
	q.SetRunner(func(c context.Context, j *Job) error {
		if e := s.CheckpointOperation(c, j.ID, "prepared", "db_applied"); e != nil {
			return e
		}
		cancel()
		return context.Canceled
	})
	if e = q.Run(run); e != nil {
		t.Fatal(e)
	}
	before, e := s.LoadOperation(ctx, r.Job.ID)
	if e != nil {
		t.Fatal(e)
	}
	if before.Checkpoint != "db_applied" {
		t.Fatal(before.Checkpoint)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	q = NewQueue(s, seal, &fakeAudit{})
	q.SetRunner(func(c context.Context, j *Job) error {
		op, e := s.LoadOperation(c, j.ID)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(op.Identity, before.Identity) {
			t.Error("identity changed across restart")
		}
		return s.CheckpointOperation(c, j.ID, "db_applied", "verified")
	})
	run, cancel = context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- q.Run(run) }()
	deadline := time.After(3 * time.Second)
	for {
		j, e := s.GetJob(ctx, r.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == "done" {
			break
		}
		select {
		case e := <-done:
			t.Fatalf("worker exited: %v", e)
		case <-deadline:
			t.Fatal("completion timeout")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	got, e := q.Claim(ctx, r.Job.ID, r.ClaimToken)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(got, map[string]any{"password": "generated-password"}) {
		b, _ := json.Marshal(got)
		t.Fatalf("claim must contain only password, got %s", b)
	}
	if _, e = q.Claim(ctx, r.Job.ID, r.ClaimToken); e == nil {
		t.Fatal("claim replay accepted")
	}
}
