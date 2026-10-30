package jobs

import (
	"context"
	"errors"
	"novapanel/internal/audit"
	"novapanel/internal/store"
	"sync/atomic"
	"testing"
	"time"
)

type schedulerHealthAudit struct {
	calls atomic.Int32
	err   error
}

func (a *schedulerHealthAudit) Healthy() error           { a.calls.Add(1); return a.err }
func (a *schedulerHealthAudit) Record(audit.Event) error { return a.err }

func TestSchedulerAuditFailureWaitsForCancellation(t *testing.T) {
	a := &schedulerHealthAudit{err: errors.New("sticky audit failure")}
	s := NewScheduler(NewQueue(nil, nil, a), nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	select {
	case e := <-done:
		t.Fatalf("scheduler stopped on health: %v", e)
	case <-time.After(30 * time.Millisecond):
	}
	if n := a.calls.Load(); n > 4 {
		t.Fatalf("health busy loop: %d", n)
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked")
	}
}

func TestSchedulerKeepsFatalErrors(t *testing.T) {
	p := &scheduledStore{rows: []store.Schedule{{Verb: "unsupported"}}}
	s := NewScheduler(NewQueue(p, nil, &fakeAudit{}), nil, nil)
	if e := s.Run(context.Background()); e == nil {
		t.Fatal("ignored fatal scheduler error")
	}
}
