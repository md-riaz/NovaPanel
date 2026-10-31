package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestOperationExpiryBlocksUntilReconciled(t *testing.T) {
	ctx := context.Background()
	s, e := Open(filepath.Join(t.TempDir(), "panel.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	clock := &authClock{now: 1000}
	s.clock = clock
	j := Job{ID: "expiry-one", Verb: "database.create", CreatedBy: "owner-session", Role: "owner", State: "queued", Args: []byte(`{}`)}
	req := OperationRequest{Kind: DatabaseCreate, DatabaseID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR"}
	op, e := s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, req, []byte{1}, []byte{2})
	if e != nil {
		t.Fatal(e)
	}
	clock.now = op.Expires
	if e = s.CheckOperation(ctx, j.ID); !errors.Is(e, ErrOperationExpired) {
		t.Fatalf("expiry: %v", e)
	}
	if n, e := s.ExpireOperations(ctx); e != nil || n != 1 {
		t.Fatalf("expire %d %v", n, e)
	}
	op, e = s.LoadOperation(ctx, j.ID)
	if e != nil || op.Checkpoint != "expired" || len(op.Ciphertext) != 0 {
		t.Fatalf("expiry state %+v %v", op, e)
	}
	got, e := s.GetJob(ctx, j.ID)
	if e != nil || got.Code != "interrupted" {
		t.Fatalf("job %+v %v", got, e)
	}
	j.ID = "expiry-two"
	if _, e = s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, req, []byte{1}, []byte{2}); !errors.Is(e, ErrOperationLocked) {
		t.Fatalf("blocker lost: %v", e)
	}
	if e = s.ReconcileOperation(ctx, op.JobID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, req, []byte{1}, []byte{2}); e != nil {
		t.Fatal(e)
	}
	if e = s.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.Exec("DELETE FROM jobs WHERE id=?", op.JobID); return e }); e == nil {
		t.Fatal("journal job retention guard lost")
	}
}
