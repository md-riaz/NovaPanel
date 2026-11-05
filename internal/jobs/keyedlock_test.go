package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeyedLockSerializesAndReleasesReferences(t *testing.T) {
	var k keyedLock
	var active atomic.Int32
	var overlap atomic.Bool
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := k.lock(context.Background(), "same")
			if err != nil {
				t.Error(err)
				return
			}
			defer unlock()
			if active.Add(1) != 1 {
				overlap.Store(true)
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
		}()
	}
	wg.Wait()
	if overlap.Load() || len(k.keys) != 0 {
		t.Fatalf("overlap=%v retained=%d", overlap.Load(), len(k.keys))
	}
}

func TestKeyedLockContextContention(t *testing.T) {
	for _, key := range []string{"db:parent", "dbname:example", "dbuser:example"} {
		t.Run(key, func(t *testing.T) {
			var k keyedLock
			unlock, err := k.lock(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			start := time.Now()
			if got, err := k.lock(ctx, key); !errors.Is(err, context.DeadlineExceeded) || got != nil {
				t.Fatalf("lock = %v, %v", got != nil, err)
			}
			if time.Since(start) > time.Second {
				t.Fatal("lock did not respect deadline")
			}
			unlock()
			next, err := k.lock(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			next()
			if len(k.keys) != 0 {
				t.Fatalf("retained %d keys", len(k.keys))
			}
		})
	}
}
