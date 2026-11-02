package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCanceledKeyedWaitersDoNotSplitLockIdentity(t *testing.T) {
	var locks keyedLock
	release := locks.lock("site")
	const n = 20
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := locks.lockContext(ctx, "site")
			if err == nil {
				unlock()
			}
			errs <- err
		}()
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		locks.mu.Lock()
		r := locks.keys["site"]
		refs := r.refs
		locks.mu.Unlock()
		if refs == n+1 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			release()
			wg.Wait()
			t.Fatal("waiters did not register")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter: %v", err)
		}
	}
	locks.mu.Lock()
	r := locks.keys["site"]
	if r == nil || r.refs != 1 {
		locks.mu.Unlock()
		release()
		t.Fatal("held lock reference lost")
	}
	locks.mu.Unlock()
	acquired := make(chan func(), 1)
	go func() { acquired <- locks.lock("site") }()
	select {
	case unlock := <-acquired:
		unlock()
		release()
		t.Fatal("second holder acquired before release")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case unlock := <-acquired:
		unlock()
	case <-time.After(time.Second):
		t.Fatal("waiter not released")
	}
	locks.mu.Lock()
	defer locks.mu.Unlock()
	if len(locks.keys) != 0 {
		t.Fatalf("leaked refs: %d", len(locks.keys))
	}
}
