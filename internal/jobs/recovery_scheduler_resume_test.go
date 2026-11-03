package jobs

import (
	"context"
	"encoding/json"
	"novapanel/internal/store"
	"testing"
	"time"
)

type resumeScheduleStore struct {
	*store.Store
	fired chan struct{}
}

func (s *resumeScheduleStore) DueSchedules(context.Context, int64) ([]store.Schedule, error) {
	return []store.Schedule{{ID: "s", Verb: "reconcile", Cron: "minutely", NextRun: 60000, Args: json.RawMessage(`{}`), State: json.RawMessage(`{}`), Role: "owner", CreatedBy: "root"}}, nil
}
func (s *resumeScheduleStore) EnqueueScheduledJob(context.Context, string, int64, int64, int64, json.RawMessage, Job, *store.JobClaim) (bool, error) {
	select {
	case s.fired <- struct{}{}:
	default:
	}
	return false, nil
}

func TestSchedulerSurvivesFailedRecoveryAndFiresAfterRetry(t *testing.T) {
	q, st, _, ctx := fixture(t)
	a := &switchAudit{}
	q.audit = a
	if err := q.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	a.fail.Store(true)
	if err := q.PrepareOperations(ctx); err == nil {
		t.Fatal("expected failure")
	}
	a.fail.Store(false)
	fired := make(chan struct{}, 1)
	q.store = &resumeScheduleStore{Store: st, fired: fired}
	s := NewScheduler(q, &fakeClock{60000}, map[string]SchedulePolicy{"reconcile": {}})
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(run) }()
	select {
	case err := <-done:
		t.Fatalf("scheduler exited: %v", err)
	case <-time.After(1200 * time.Millisecond):
	}
	if err := q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fired:
	case err := <-done:
		t.Fatalf("scheduler exited: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not resume")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
