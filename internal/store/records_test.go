package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func fresh(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestJobLifecycleClaimsRecovery(t *testing.T) {
	s := fresh(t)
	ctx := context.Background()
	j := Job{ID: "a", Verb: "restart", Args: json.RawMessage(`{}`), ObjectKey: "system", Actor: "root", Role: "owner", CreatedBy: "root@local", State: "queued", Created: 1, Updated: 1, Effectful: true, Scopes: []string{"system:write"}}
	if e := s.CreateJob(ctx, j); e != nil {
		t.Fatal(e)
	}
	got, e := s.NextQueuedJob(ctx)
	if e != nil || got.CreatedBy != j.CreatedBy {
		t.Fatal(got, e)
	}
	got, e = s.StartJob(ctx, j.ID, 2)
	if e != nil || got.Attempts != 1 || got.State != "running" {
		t.Fatal(got, e)
	}
	if _, e = s.StartJob(ctx, j.ID, 3); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
	if e = s.RecoverJobs(ctx, 4); e != nil {
		t.Fatal(e)
	}
	got, e = s.GetJob(ctx, j.ID)
	if e != nil || got.Code != "interrupted" || got.State != "failed" {
		t.Fatal(got, e)
	}
	c := JobClaim{JobID: j.ID, ClaimHash: "hash", Nonce: []byte{}, Ciphertext: []byte{}, Expires: 100}
	if e = s.PutClaim(ctx, c); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConsumeClaim(ctx, j.ID, "hash", j.CreatedBy, 5); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("unpublished", e)
	}
	if e = s.PublishClaimAt(ctx, j.ID, []byte("nonce"), []byte("sealed"), 5); e != nil {
		t.Fatal(e)
	}
	for _, x := range []struct {
		hash, owner string
		now         int64
	}{{"bad", j.CreatedBy, 5}, {"hash", "other", 5}, {"hash", j.CreatedBy, 100}} {
		if _, e = s.ConsumeClaim(ctx, j.ID, x.hash, x.owner, x.now); !errors.Is(e, sql.ErrNoRows) {
			t.Fatal("unauthorized claim", e)
		}
	}
	var n atomic.Int32
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.ConsumeClaim(ctx, j.ID, "hash", j.CreatedBy, 5); e == nil {
				n.Add(1)
			}
		}()
	}
	wg.Wait()
	if n.Load() != 1 {
		t.Fatal(n.Load())
	}
}
