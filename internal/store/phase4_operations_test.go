package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestOperationReservationAndAtomicPublication(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	j := Job{ID: "01K8WMXZF57CGNZKX1FZ38DZMA", Verb: "database.create", ObjectKey: "db:reserved", CreatedBy: "local-root", Role: "owner", Args: []byte(`{}`), State: "queued"}
	req := OperationRequest{Kind: DatabaseCreate, DatabaseID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR"}
	op, err := s.PrepareOperation(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, req, []byte{1}, []byte{2})
	if err != nil {
		t.Fatal(err)
	}
	if op.Expires-op.Updated != OperationTTLMillis || len(op.Keys()) != 2 {
		t.Fatalf("reservation: %+v", op)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.LoadOperation(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if op.Identity.Database.ID != req.DatabaseID || op.Owner != "local-root" {
		t.Fatalf("identity/owner: %+v", op)
	}
	if _, err = s.StartJob(ctx, j.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointOperation(ctx, j.ID, "prepared", "db_applied"); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointOperation(ctx, j.ID, "db_applied", "verified"); err != nil {
		t.Fatal(err)
	}
	// Inject a genuine SQLite failure after metadata mutation. No partial publication survives.
	phase4Exec(t, s.db.raw, `CREATE TRIGGER fail_terminal BEFORE UPDATE OF state ON jobs WHEN NEW.state='done' BEGIN SELECT RAISE(ABORT,'injected'); END`)
	claim := &JobClaim{Nonce: []byte{3}, Ciphertext: []byte{4}}
	if err = s.CompleteOperation(ctx, j.ID, claim); err == nil {
		t.Fatal("injected failure ignored")
	}
	if _, err = s.GetDatabase(ctx, req.DatabaseID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("metadata leaked: %v", err)
	}
	op, err = s.LoadOperation(ctx, j.ID)
	if err != nil || op.Checkpoint != "verified" || len(op.Ciphertext) == 0 {
		t.Fatalf("journal leaked: %+v %v", op, err)
	}
	phase4Exec(t, s.db.raw, "DROP TRIGGER fail_terminal")
	if err = s.CompleteOperation(ctx, j.ID, claim); err != nil {
		t.Fatal(err)
	}
	db, err := s.GetDatabase(ctx, req.DatabaseID)
	if err != nil || db.PrimaryUserID != req.UserID {
		t.Fatalf("metadata: %+v %v", db, err)
	}
	got, err := s.GetJob(ctx, j.ID)
	if err != nil || got.State != "done" {
		t.Fatalf("job: %+v %v", got, err)
	}
	op, err = s.LoadOperation(ctx, j.ID)
	if err != nil || op.Checkpoint != "done" || len(op.Ciphertext) != 0 {
		t.Fatalf("terminal: %+v %v", op, err)
	}
	if _, err = s.ConsumeClaim(ctx, j.ID, "hash", "attacker"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("wrong owner accepted")
	}
	if _, err = s.ConsumeClaim(ctx, j.ID, "hash", "local-root"); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteOperation(ctx, j.ID, claim); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConsumeClaim(ctx, j.ID, "hash", "local-root"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("claim regenerated")
	}
}
