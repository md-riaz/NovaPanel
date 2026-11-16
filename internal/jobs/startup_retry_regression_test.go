package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"novapanel/internal/store"
)

type retryExpiryStore struct {
	*store.Store
	refuse bool
}

func (s *retryExpiryStore) ExpireOperations(ctx context.Context) (int, error) {
	if s.refuse {
		return 0, errors.New("recovery unavailable")
	}
	return s.Store.ExpireOperations(ctx)
}
func TestStartupDoesNotBypassFailedRecoveryRetry(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	wrapped := &retryExpiryStore{Store: s}
	q := NewQueue(wrapped, nil, &fakeAudit{})
	ctx := context.Background()
	if err = q.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	wrapped.refuse = true
	if err = q.PrepareOperations(ctx); err == nil {
		t.Fatal("missing failed recovery")
	}
	if err = q.PrepareStartup(ctx); err == nil {
		t.Fatal("startup bypassed failed recovery retry")
	}
}
