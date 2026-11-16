package jobs

import (
	"context"
	"errors"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestSiteAdmissionCancellationReleasesRecoveryGuard(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q := NewQueue(s, nil, &fakeAudit{})
	if err = q.PrepareOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	release := q.locks.lock("site:site")
	defer release()
	base, cancel := context.WithCancel(WithMetadata(context.Background(), Metadata{CreatedBy: "local-root", Role: "owner"}))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, e := q.EnqueueOperation(base, store.OperationRequest{Kind: store.SiteDelete, SiteID: "site"}, nil)
		done <- e
	}()
	deadline := time.Now().Add(time.Second)
	for {
		q.locks.mu.Lock()
		ref := q.locks.keys["site:site"]
		waiting := ref != nil && ref.refs == 2
		q.locks.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("admission did not reach site lock")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("canceled admission remained on site lock")
	}
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatalf("recovery remained blocked: %v", err)
	}
}
