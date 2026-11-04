package jobs

import (
	"context"
	"errors"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestRepeatedStartupCancellationWhileRecoveryLocked(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q := NewQueue(s, nil, &fakeAudit{})
	if err = q.PrepareStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	q.operationMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.PrepareStartup(ctx) }()
	deadline := time.After(time.Second)
	for len(q.startupGate) == 0 {
		select {
		case <-deadline:
			q.operationMu.Unlock()
			cancel()
			t.Fatal("startup did not acquire gate")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-done:
		q.operationMu.Unlock()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		q.operationMu.Unlock()
		<-done
		t.Fatal("canceled startup waited for recovery read lock")
	}
}
