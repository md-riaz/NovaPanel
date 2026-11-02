package jobs

import (
	"context"
	"errors"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestStartupWaiterHonorsCancellation(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q := NewQueue(s, nil, &fakeAudit{})
	entered, release := make(chan struct{}), make(chan struct{})
	q.SetStartupRecovery(func(context.Context) (*Job, error) { close(entered); <-release; return nil, nil }, nil)
	first := make(chan error, 1)
	go func() { first <- q.PrepareStartup(context.Background()) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.PrepareStartup(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			close(release)
			<-first
			t.Fatalf("expected cancellation: %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		close(release)
		<-first
		<-done
		t.Fatal("canceled waiter remained blocked behind startup")
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}
