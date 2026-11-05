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

func TestDatabaseGrandparentLockKey(t *testing.T) {
	for _, verb := range []string{"database.create", "database.delete", "database.rotate_password", "dbuser.create", "dbuser.delete"} {
		j := Job{Verb: verb, Args: []byte(`{"site_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV"}`)}
		if got := databaseSiteKey(j); got != "site:01ARZ3NDEKTSV4RRFFQ69G5FAV" {
			t.Fatalf("%s: %q", verb, got)
		}
	}
}

// Exercise concurrent worker execution, not merely key derivation: database.create
// must wait for the same site lock held by site.delete.
func TestDatabaseCreateWaitsForSiteDelete(t *testing.T) {
	q, _, _, ctx := fixture(t)
	const site = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	del, err := q.Enqueue(ctx, "site.delete", map[string]string{"site_id": site}, "site:"+site)
	if err != nil {
		t.Fatal(err)
	}
	create, err := q.Enqueue(ctx, "database.create", map[string]string{"site_id": site}, "db:example")
	if err != nil {
		t.Fatal(err)
	}
	entered, release, created := make(chan struct{}), make(chan struct{}), make(chan struct{})
	q.SetRunner(func(_ context.Context, j *Job) error {
		if j.Verb == "site.delete" {
			close(entered)
			<-release
		} else {
			close(created)
		}
		return nil
	})
	done := make(chan error, 2)
	go func() { done <- q.execute(ctx, del.Job) }()
	<-entered
	go func() { done <- q.execute(ctx, create.Job) }()
	select {
	case <-created:
		t.Fatal("database create overlapped site delete")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-created:
	default:
		t.Fatal("database create did not run")
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
