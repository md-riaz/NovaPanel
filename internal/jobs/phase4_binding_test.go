package jobs

import (
	"context"
	"database/sql"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
)

func TestOperationRecoveryRejectsAuthorityTampering(t *testing.T) {
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "stored-token", Role: "token", Scopes: []string{"databases:write"}})
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
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE jobs SET automation=1 WHERE id=?", r.Job.ID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.OpenOperationCredentials(ctx, r.Job.ID); err == nil {
		t.Fatal("changed durable authority accepted")
	}
}
