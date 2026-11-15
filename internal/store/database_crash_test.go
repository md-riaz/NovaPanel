package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

// Reopening the on-disk database models process loss on either side of the
// atomic publication and terminal job update, not just an in-memory retry.
func TestDatabasePublicationAndTerminalRecovery(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_commit", true: "after_commit"}[committed], func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "panel.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			j := Job{ID: "job", Verb: "database.create", ObjectKey: "db:db-a", CreatedBy: "owner", State: "queued", Args: json.RawMessage(`{"db_id":"db-a","user_id":"user-a","name":"sample","username":"sample_user"}`)}
			if err = s.CreateJobWithClaim(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}); err != nil {
				t.Fatal(err)
			}
			if _, err = s.StartJob(ctx, j.ID, 1); err != nil {
				t.Fatal(err)
			}
			result := json.RawMessage(`{"db_id":"db-a"}`)
			commit := func() error {
				return s.CommitDatabaseClaim(ctx, j.ID, j.Verb, j.ObjectKey, result, []byte("nonce"), []byte("cipher"))
			}
			if committed {
				if err = commit(); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.RecoverJobs(ctx, 2); err != nil {
				t.Fatal(err)
			}
			recovered, err := s.StartJob(ctx, j.ID, 3)
			if err != nil {
				t.Fatal(err)
			}
			if committed {
				if recovered.Step != "database_committed" {
					t.Fatalf("lost commit: %+v", recovered)
				}
			} else {
				if recovered.Step != "" {
					t.Fatalf("unexpected commit: %+v", recovered)
				}
				if err = commit(); err != nil {
					t.Fatal(err)
				}
				recovered, err = s.GetJob(ctx, j.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, users, err := s.DatabaseWithUsers(ctx, "db-a"); err != nil || len(users) != 1 {
				t.Fatalf("metadata: %v %+v", err, users)
			}
			if _, err = s.ConsumeClaim(ctx, j.ID, "hash", "owner"); err != nil {
				t.Fatal(err)
			}
			recovered.State = "done"
			if err = s.UpdateJob(ctx, recovered); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.RecoverJobs(ctx, 4); err != nil {
				t.Fatal(err)
			}
			final, err := s.GetJob(ctx, j.ID)
			if err != nil || final.State != "done" || final.Step != "database_committed" {
				t.Fatalf("terminal: %+v %v", final, err)
			}
			if _, err = s.ConsumeClaim(ctx, j.ID, "hash", "owner"); err == nil {
				t.Fatal("consumed claim returned after restart")
			}
		})
	}
}
