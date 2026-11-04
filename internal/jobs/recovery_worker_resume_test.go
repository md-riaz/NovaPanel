package jobs

import (
	"context"
	"errors"
	"novapanel/internal/audit"
	"novapanel/internal/store"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type observedWorkerStore struct {
	*store.Store
	entered chan struct{}
	once    sync.Once
	fail    atomic.Bool
}

func (s *observedWorkerStore) NextQueuedJob(ctx context.Context) (Job, error) {
	s.once.Do(func() { close(s.entered) })
	return s.Store.NextQueuedJob(ctx)
}

type switchAudit struct{ fail atomic.Bool }

func (*switchAudit) Record(audit.Event) error { return nil }
func (a *switchAudit) Healthy() error {
	if a.fail.Load() {
		return errors.New("audit unavailable")
	}
	return nil
}

func (s *observedWorkerStore) ExpireOperations(ctx context.Context) (int, error) {
	if s.fail.Load() {
		return 0, errors.New("operation recovery unavailable")
	}
	return s.Store.ExpireOperations(ctx)
}

func TestWorkerSurvivesFailedLiveRecoveryAndResumes(t *testing.T) {
	q, s, _, ctx := fixture(t)
	a := &switchAudit{}
	q.audit = a
	if err := q.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{}, 1)
	q.SetRunner(func(context.Context, *Job) error { ran <- struct{}{}; return nil })
	observed := &observedWorkerStore{Store: s, entered: make(chan struct{})}
	q.store = observed
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	exited := make(chan struct{})
	defer func() {
		cancel()
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
			t.Error("worker did not stop in cleanup")
		}
	}()
	go func() { defer close(exited); done <- q.Run(workerCtx) }()
	select {
	case <-observed.entered:
	case err := <-done:
		t.Fatalf("worker exited before loop: %v", err)
	case <-time.After(time.Second):
		t.Fatal("worker did not enter loop")
	}
	// Fail only the live recovery persistence call, after it owns the writer.
	observed.fail.Store(true)
	if err := q.PrepareOperations(ctx); err == nil {
		t.Fatal("expected recovery failure")
	}
	observed.fail.Store(false)
	// Insert a queued job directly because external admission is closed.
	j := Job{ID: "queued-recovery", Verb: "reconcile", ObjectKey: "system", State: "queued", CreatedBy: "root"}
	if err := s.CreateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		t.Fatalf("worker exited during failed recovery: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case err := <-done:
		t.Fatalf("worker exited: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not resume")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}
