package jobs

import (
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
			unlock := k.lock("same")
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
