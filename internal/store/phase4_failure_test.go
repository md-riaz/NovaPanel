package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestOperationTerminalFailureCleanup(t *testing.T) {
	for _, code := range []string{"agent_error", "interrupted"} {
		t.Run(code, func(t *testing.T) {
			s, e := Open(filepath.Join(t.TempDir(), "panel.db"))
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			ctx := context.Background()
			j := Job{ID: "failed-job", Verb: "database.create", CreatedBy: "local-root", Role: "owner", State: "queued", Args: []byte(`{}`)}
			_, e = s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, OperationRequest{Kind: DatabaseCreate, DatabaseID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR"}, []byte{1}, []byte{2})
			if e != nil {
				t.Fatal(e)
			}
			s.StartJob(ctx, j.ID, 1)
			if e = s.FailOperation(ctx, j.ID, code); e != nil {
				t.Fatal(e)
			}
			op, e := s.LoadOperation(ctx, j.ID)
			if e != nil {
				t.Fatal(e)
			}
			if code == "interrupted" {
				if len(op.Ciphertext) == 0 || op.Checkpoint == "expired" {
					t.Fatal("interrupted recovery destroyed")
				}
			} else if len(op.Ciphertext) != 0 || op.Checkpoint != "expired" {
				t.Fatal("terminal ciphertext retained")
			}
			if e = s.CheckOperation(ctx, j.ID); code != "interrupted" && !errors.Is(e, ErrOperationExpired) {
				t.Fatalf("terminal resumable %v", e)
			}
		})
	}
}
