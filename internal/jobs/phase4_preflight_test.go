package jobs

import (
	"context"
	"database/sql"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
)

func TestOperationCredentialTamperingRefusesRunner(t *testing.T) {
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "owner-session", Role: "owner"})
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seal, _ := secret.New(make([]byte, 32))
	q := NewQueue(s, seal, &fakeAudit{})
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.DatabaseCreate}, map[string]any{"password": "bound-password"})
	if err != nil {
		t.Fatal(err)
	}
	// Change the trusted snapshot after sealing, as a durable integrity regression.
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE jobs SET automation=1 WHERE id=?", r.Job.ID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	called := false
	q.SetRunner(func(context.Context, *Job) error { called = true; return nil })
	if err = q.execute(ctx, r.Job); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("runner saw unbound authority")
	}
}
