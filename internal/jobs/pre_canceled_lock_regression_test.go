package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPreCanceledRecoveryDoesNotWaitForOperationLock(t *testing.T) {
	q := NewQueue(nil, nil, &fakeAudit{})
	q.operationMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- q.PrepareOperations(ctx) }()
	select {
	case err := <-done:
		q.operationMu.Unlock()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		q.operationMu.Unlock()
		<-done
		t.Fatal("pre-canceled recovery waited on operation lock")
	}
}
