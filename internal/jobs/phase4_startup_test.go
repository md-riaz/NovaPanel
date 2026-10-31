package jobs

import (
	"context"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
	"time"
)

type operationFixedClock int64

func (c operationFixedClock) Now() int64 { return int64(c) }

func TestOperationRealQueueStartupExpiry(t *testing.T) {
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "local-root", Role: "owner"})
	path := filepath.Join(t.TempDir(), "queue.db")
	s, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	seal, _ := secret.New(make([]byte, 32))
	q := NewQueue(s, seal, &fakeAudit{})
	r, e := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.DatabaseCreate}, map[string]any{"password": "expired-password"})
	if e != nil {
		t.Fatal(e)
	}
	op, e := s.LoadOperation(ctx, r.Job.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = store.OpenWithClock(path, operationFixedClock(op.Expires))
	if e != nil {
		t.Fatal(e)
	}
	q = NewQueue(s, seal, &fakeAudit{})
	q.SetClock(operationFixedClock(op.Expires))
	called := false
	q.SetRunner(func(context.Context, *Job) error { called = true; return nil })
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- q.Run(run) }()
	deadline := time.After(time.Second)
	for {
		j, _ := s.GetJob(ctx, r.Job.ID)
		if j.State == "failed" {
			break
		}
		select {
		case e := <-done:
			cancel()
			t.Fatalf("worker stopped before bounded expiry cleanup: %v", e)
		case <-deadline:
			cancel()
			t.Fatal("expiry timeout")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	op, e = s.LoadOperation(ctx, r.Job.ID)
	if e != nil {
		t.Fatal(e)
	}
	j, _ := s.GetJob(ctx, r.Job.ID)
	if called || op.Checkpoint != "expired" || len(op.Ciphertext) != 0 || j.Code != "interrupted" {
		t.Fatalf("expiry not safe: called=%v checkpoint=%s ciphertext=%d code=%s", called, op.Checkpoint, len(op.Ciphertext), j.Code)
	}
}
