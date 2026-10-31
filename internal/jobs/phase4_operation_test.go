package jobs

import (
	"context"
	"encoding/json"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestOperationQueueReopenDeleteAndOrderedLocks(t *testing.T) {
	for _, kind := range []store.OperationKind{store.DatabaseDelete, store.DBUserDelete} {
		t.Run(string(kind), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "jobs.db")
			s, e := store.Open(path)
			if e != nil {
				t.Fatal(e)
			}
			pair := store.DatabasePrimary{ID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR"}
			if e = s.CreateDatabasePrimary(context.Background(), pair); e != nil {
				t.Fatal(e)
			}
			target := "01K8WMXZF57CGNZKX1FZ38DZMS"
			if kind == store.DBUserDelete {
				// Create the secondary through the production typed journal, not direct SQL.
				j := store.Job{ID: "01K8WMXZF57CGNZKX1FZ38DZMT", Verb: "dbuser.create", CreatedBy: "local-root", Role: "owner", State: "queued", Args: json.RawMessage(`{}`)}
				_, e = s.PrepareOperation(context.Background(), j, store.JobClaim{JobID: j.ID, ClaimHash: "hash"}, store.OperationRequest{Kind: store.DBUserCreate, DatabaseID: pair.ID, UserID: target}, []byte{1}, []byte{2})
				if e != nil {
					t.Fatal(e)
				}
				s.StartJob(context.Background(), j.ID, 1)
				s.CheckpointOperation(context.Background(), j.ID, "prepared", "db_applied")
				s.CheckpointOperation(context.Background(), j.ID, "db_applied", "verified")
				if e = s.CompleteOperation(context.Background(), j.ID, &store.JobClaim{Nonce: []byte{1}, Ciphertext: []byte{2}}); e != nil {
					t.Fatal(e)
				}
			}
			seal, _ := secret.New(make([]byte, 32))
			q := NewQueue(s, seal, &fakeAudit{})
			if e := q.PrepareOperations(context.Background()); e != nil {
				t.Fatal(e)
			}
			ctx := WithMetadata(context.Background(), Metadata{Actor: "forged-owner-label", CreatedBy: "local-root", Role: "owner"})
			r, e := q.EnqueueOperation(ctx, store.OperationRequest{Kind: kind, DatabaseID: pair.ID, UserID: target}, nil)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.StartJob(ctx, r.Job.ID, 1); e != nil {
				t.Fatal(e)
			}
			if e = s.CheckpointOperation(ctx, r.Job.ID, "prepared", "db_applied"); e != nil {
				t.Fatal(e)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			s, e = store.Open(path)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			q = NewQueue(s, seal, &fakeAudit{})
			calls := 0
			q.SetRunner(func(run context.Context, j *Job) error {
				calls++
				m, _ := MetadataFrom(run)
				if m.CreatedBy != "local-root" {
					t.Error("lost stored authority")
				}
				op, e := s.LoadOperation(run, j.ID)
				if e != nil {
					return e
				}
				if op.Checkpoint != "db_applied" {
					t.Error("lost checkpoint")
				}
				return nil
			})
			run, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- q.Run(run) }()
			for i := 0; i < 200; i++ {
				j, _ := s.GetJob(ctx, r.Job.ID)
				if j.State == "done" {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
			if e = <-done; e != nil {
				t.Fatal(e)
			}
			j, e := s.GetJob(ctx, r.Job.ID)
			if e != nil || j.State != "done" || calls != 1 {
				t.Fatalf("queue: %+v calls=%d %v", j, calls, e)
			}
			op, e := s.LoadOperation(ctx, j.ID)
			if e != nil || op.Checkpoint != "done" {
				t.Fatalf("journal: %+v %v", op, e)
			}
			if _, e = s.ConsumeClaim(ctx, j.ID, "", "local-root"); e == nil {
				t.Fatal("delete created claim")
			}
		})
	}
}

func TestOrderedLockSet(t *testing.T) {
	got := store.OrderedOperationKeys([]string{"db:z", "site:z", "site:a", "db:a", "site:a"})
	want := []string{"site:a", "site:z", "db:a", "db:z"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatal(got)
		}
	}
	var locks keyedLock
	release := locks.lockSet([]string{"db:z", "site:a"})
	done := make(chan struct{})
	go func() { u := locks.lockSet([]string{"site:a", "db:z"}); u(); close(done) }()
	select {
	case <-done:
		t.Fatal("overlapping sets did not serialize")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deadlocked")
	}
}
