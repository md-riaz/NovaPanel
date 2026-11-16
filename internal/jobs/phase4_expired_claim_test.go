package jobs

import (
	"context"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
)

func TestExpiredOperationRetainsCredentialUntilClaim(t *testing.T) {
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "owner", Role: "owner"})
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seal, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueue(s, seal, &fakeAudit{})
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.DatabaseCreate}, map[string]any{"password": "retained"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.LoadOperation(ctx, result.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.OpenWithClock(path, operationFixedClock(op.Expires))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q = NewQueue(s, seal, &fakeAudit{})
	if _, err = s.ExpireOperations(ctx); err != nil {
		t.Fatal(err)
	}
	op, err = s.LoadOperation(ctx, result.Job.ID)
	if err != nil || len(op.Ciphertext) == 0 {
		t.Fatalf("lost encrypted input: %v", err)
	}
	if _, err = q.Claim(ctx, result.Job.ID, "wrong"); err == nil {
		t.Fatal("accepted wrong claim token")
	}
	op, err = s.LoadOperation(ctx, result.Job.ID)
	if err != nil || len(op.Ciphertext) == 0 {
		t.Fatalf("unauthorized attempt destroyed input: %v", err)
	}
	fields, err := q.Claim(ctx, result.Job.ID, result.ClaimToken)
	if err != nil || fields["password"] != "retained" {
		t.Fatalf("claim: %v %v", fields, err)
	}
	op, err = s.LoadOperation(ctx, result.Job.ID)
	if err != nil || len(op.Ciphertext) != 0 || op.Reconciled {
		t.Fatalf("unsafe recovery disposition: %+v %v", op, err)
	}
	if _, err = q.Claim(ctx, result.Job.ID, result.ClaimToken); err == nil {
		t.Fatal("claim replayed")
	}
}
