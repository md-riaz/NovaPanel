package jobs

import (
	"context"
	"errors"
	"novapanel/internal/store"
	"testing"
)

func TestFailedStartupExcludesPendingOperationRecovery(t *testing.T) {
	q, s, _, ctx := fixture(t)
	if err := q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.DatabaseCreate}, map[string]any{"password": "private"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	q.SetRunner(func(c context.Context, j *Job) error {
		calls++
		if err := s.CheckpointOperation(c, j.ID, "prepared", "db_applied"); err != nil {
			return err
		}
		return s.CheckpointOperation(c, j.ID, "db_applied", "verified")
	})
	fail := true
	q.SetStartupRecovery(func(context.Context) (*Job, error) {
		if fail {
			return nil, errors.New("unresolved TLS")
		}
		return nil, nil
	}, nil)
	if err = q.PrepareStartup(ctx); err == nil {
		t.Fatal("expected startup failure")
	}
	if err = q.PrepareOperations(ctx); !errors.Is(err, ErrRecoveryBlocked) {
		t.Fatalf("operation recovery bypassed startup: %v", err)
	}
	j, err := s.GetJob(ctx, result.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || j.State != "queued" {
		t.Fatalf("pending operation executed: calls=%d state=%s", calls, j.State)
	}
	fail = false
	if err = q.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("successful startup did not recover operation: calls=%d", calls)
	}
}
