package jobs

import (
	"context"
	"errors"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"testing"
)

func TestPublicationPendingErrorsUseCanonicalCode(t *testing.T) {
	q, s, _, ctx := fixture(t)
	r, err := q.Enqueue(ctx, "reconcile", map[string]any{}, "system")
	if err != nil {
		t.Fatal(err)
	}
	q.store = publicationRefusalStore{s}
	if _, err = q.Status(ctx, r.Job.ID); !publicationAgentError(err) {
		t.Fatalf("status code: %v", err)
	}
	if _, err = q.Claim(ctx, r.Job.ID, r.ClaimToken); !publicationAgentError(err) {
		t.Fatalf("claim code: %v", err)
	}
}
func publicationAgentError(err error) bool {
	var coded *rpcerr.CodedError
	return errors.As(err, &coded) && coded.Code == "agent_error"
}

type publicationRefusalStore struct{ *store.Store }

func (s publicationRefusalStore) GetJob(context.Context, string) (store.Job, error) {
	return store.Job{}, store.ErrPublicationPending
}
func (s publicationRefusalStore) ConsumeClaim(context.Context, string, string, string) (store.JobClaim, error) {
	return store.JobClaim{}, store.ErrPublicationPending
}
