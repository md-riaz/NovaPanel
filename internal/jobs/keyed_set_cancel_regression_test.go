package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCanceledLockSetReleasesEarlierKeys(t *testing.T) {
	var k keyedLock
	held := k.lock("site:b")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		u, err := k.lockSetContext(ctx, []string{"site:b", "site:a"})
		if u != nil {
			u()
		}
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		k.mu.Lock()
		a := k.keys["site:a"]
		b := k.keys["site:b"]
		ready := a != nil && a.refs == 1 && b != nil && b.refs == 2
		k.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			held()
			<-done
			t.Fatal("ordered waiter not registered")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("got %v", err)
		}
	case <-time.After(time.Second):
		held()
		<-done
		t.Fatal("canceled set blocked")
	}
	probe, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	unlock, err := k.lockContext(probe, "site:a")
	if err != nil {
		held()
		t.Fatal("earlier key not released:", err)
	}
	unlock()
	held()
	k.mu.Lock()
	remaining := len(k.keys)
	k.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("leaked %d keys", remaining)
	}
}
