package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTLSRecoveryObjectLockWaitHonorsCancellation(t *testing.T) {
	q := NewQueue(nil, nil, nil)
	release := q.locks.lock("site:example")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.recoverPublication(ctx, Job{ObjectKey: "site:example"}) }()
	deadline := time.Now().Add(time.Second)
	for {
		q.locks.mu.Lock()
		refs := q.locks.keys["site:example"].refs
		q.locks.mu.Unlock()
		if refs == 2 {
			break
		}
		if time.Now().After(deadline) {
			release()
			cancel()
			<-done
			t.Fatal("recovery did not register waiter")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		release()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		release()
		<-done
		t.Fatal("canceled TLS recovery remained blocked on object lock")
	}
}
