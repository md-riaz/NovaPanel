package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestJobLockWaitHonorsCancellation(t *testing.T) {
	q := NewQueue(nil, nil, nil)
	unlock := q.locks.lock("site:example")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		release, err := q.lockJob(ctx, Job{ObjectKey: "site:example"})
		if release != nil {
			release()
		}
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		q.locks.mu.Lock()
		r := q.locks.keys["site:example"]
		refs := r.refs
		q.locks.mu.Unlock()
		if refs == 2 {
			break
		}
		if time.Now().After(deadline) {
			unlock()
			cancel()
			<-done
			t.Fatal("waiter not registered")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		unlock()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		unlock()
		<-done
		t.Fatal("job lock wait ignored cancellation")
	}
}
