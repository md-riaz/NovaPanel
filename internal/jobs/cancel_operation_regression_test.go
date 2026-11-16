package jobs

import (
	"context"
	"errors"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
)

func TestCanceledOperationRecoveryDoesNotInvalidateReadyQueue(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q := NewQueue(s, nil, &fakeAudit{})
	if err = q.PrepareStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = q.PrepareOperations(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
	if err = q.PrepareStartup(context.Background()); err != nil {
		t.Fatalf("canceled no-op invalidated ready queue: %v", err)
	}
}
