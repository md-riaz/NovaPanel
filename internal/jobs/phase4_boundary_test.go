package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/id"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
)

func TestOperationStartupDrainsMultipleExpiryBatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := store.OpenWithClock(path, operationFixedClock(100))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	ctx := context.Background()
	for i := 0; i < 205; i++ {
		jid, _ := id.New()
		dbid, _ := id.New()
		uid, _ := id.New()
		j := store.Job{ID: jid, Verb: "database.create", CreatedBy: "local-root", Role: "owner", ObjectKey: "db:" + dbid, State: "queued", Args: json.RawMessage(`{}`)}
		if _, err = s.PrepareOperation(ctx, j, store.JobClaim{JobID: jid, ClaimHash: "hash"}, store.OperationRequest{Kind: store.DatabaseCreate, DatabaseID: dbid, UserID: uid}, []byte{1}, []byte{2}); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.OpenWithClock(path, operationFixedClock(100+store.OperationTTLMillis))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueue(s, nil, &fakeAudit{})
	q.SetRunner(func(context.Context, *Job) error { t.Error("expired runner called"); return nil })
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	if pending, e := s.PendingOperations(ctx); e != nil || len(pending) != 0 {
		t.Fatalf("remaining journals: %d %v", len(pending), e)
	}
	for _, state := range []string{"queued", "running"} {
		rows, e := s.ListJobs(ctx, store.JobFilter{State: state})
		if e != nil || len(rows) != 0 {
			t.Fatalf("%s jobs: %d %v", state, len(rows), e)
		}
	}
}

func TestOperationQueueTerminalCommitFailureReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "owner-session", Role: "owner"})
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	seal, _ := secret.New(make([]byte, 32))
	q := NewQueue(s, seal, &fakeAudit{})
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.DatabaseCreate}, map[string]any{"password": "rollback-password"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `CREATE TRIGGER reject_operation_done BEFORE UPDATE OF checkpoint ON phase4_operations WHEN NEW.checkpoint='done' BEGIN SELECT RAISE(ABORT,'terminal failure'); END`)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	q.SetRunner(func(c context.Context, j *Job) error {
		if e := s.CheckpointOperation(c, j.ID, "prepared", "db_applied"); e != nil {
			return e
		}
		return s.CheckpointOperation(c, j.ID, "db_applied", "verified")
	})
	if err = q.Run(ctx); err == nil {
		t.Fatal("terminal failure ignored")
	}
	op, err := s.LoadOperation(ctx, r.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.GetJob(ctx, r.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if op.Checkpoint != "verified" || len(op.Ciphertext) == 0 || j.State != "running" {
		t.Fatalf("partial commit: %s %s", op.Checkpoint, j.State)
	}
	if _, err = q.Claim(ctx, r.Job.ID, r.ClaimToken); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("claim survived rollback: %v", err)
	}
	_, err = s.GetDatabase(ctx, op.DatabaseID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("metadata survived rollback: %v", err)
	}
	if err = s.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.ExecContext(ctx, "DROP TRIGGER reject_operation_done"); return e }); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	q = NewQueue(s, seal, &fakeAudit{})
	run, cancel := context.WithCancel(ctx)
	q.SetRunner(func(context.Context, *Job) error { return nil })
	// PrepareOperations uses the same under-lock queue execution/terminal path.
	if err = q.PrepareOperations(run); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	j, err = s.GetJob(ctx, r.Job.ID)
	if err != nil || j.State != "done" {
		t.Fatalf("retry: %s %v", j.State, err)
	}
	if got, e := q.Claim(ctx, r.Job.ID, r.ClaimToken); e != nil || got["password"] != "rollback-password" {
		t.Fatalf("retry claim: %v %v", got, e)
	}
}
