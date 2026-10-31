package jobs

import (
	"context"
	"errors"
	"novapanel/internal/rpcerr"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
)

func TestOperationQueueFailureCleanup(t *testing.T) {
	for _, code := range []string{"agent_error", "interrupted"} {
		t.Run(code, func(t *testing.T) {
			ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "local-root", Role: "owner"})
			s, err := store.Open(filepath.Join(t.TempDir(), "jobs.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			seal, err := secret.New(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			q := NewQueue(s, seal, &fakeAudit{})
			if e := q.PrepareOperations(context.Background()); e != nil {
				t.Fatal(e)
			}
			result, err := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.DatabaseCreate}, map[string]any{"password": "never-persist-plaintext"})
			if err != nil {
				t.Fatal(err)
			}
			q.SetRunner(func(context.Context, *Job) error { return rpcerr.Errf(code, "never-persist-plaintext") })
			if err = q.execute(ctx, result.Job); err != nil {
				t.Fatal(err)
			}
			op, err := s.LoadOperation(ctx, result.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			job, err := s.GetJob(ctx, result.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if job.State != "failed" || job.Code != code || job.Error != "job execution failed" {
				t.Fatalf("job: %+v", job)
			}
			if code == "interrupted" {
				if len(op.Ciphertext) == 0 || op.Checkpoint != "prepared" {
					t.Fatal("recoverable input destroyed")
				}
			} else {
				if len(op.Ciphertext) != 0 || op.Checkpoint != "expired" {
					t.Fatal("terminal recovery ciphertext retained")
				}
				if err = s.CheckOperation(ctx, result.Job.ID); !errors.Is(err, store.ErrOperationExpired) {
					t.Fatalf("terminal operation resumed: %v", err)
				}
			}
		})
	}
}
