package jobs

import (
	"context"
	"novapanel/internal/store"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentStartupRunsRecoveryOnce(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q := NewQueue(s, nil, &fakeAudit{})
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	q.SetStartupRecovery(func(context.Context) (*Job, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return nil, nil
	}, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs <- q.PrepareStartup(context.Background()) }()
	<-entered
	go func() { defer wg.Done(); errs <- q.PrepareStartup(context.Background()) }()
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("startup recovery executed %d times", got)
	}
}
