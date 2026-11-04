package jobs

import (
	"context"
	"errors"
	"testing"
)

func TestFailedStartupCannotBeReopenedByOperationRecovery(t *testing.T) {
	q, _, _, ctx := fixture(t)
	fail := true
	q.SetStartupRecovery(func(context.Context) (*Job, error) {
		if fail {
			return nil, errors.New("TLS journal unresolved")
		}
		return nil, nil
	}, nil)
	if err := q.PrepareStartup(ctx); err == nil {
		t.Fatal("expected startup failure")
	}
	if err := q.PrepareOperations(ctx); !errors.Is(err, ErrRecoveryBlocked) {
		t.Fatalf("operation recovery bypassed startup: %v", err)
	}
	if _, err := q.Enqueue(ctx, "reconcile", nil, "system"); !errors.Is(err, ErrRecoveryBlocked) {
		t.Fatalf("failed startup reopened: %v", err)
	}
	fail = false
	if err := q.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(ctx, "reconcile", nil, "system"); err != nil {
		t.Fatal(err)
	}
}
