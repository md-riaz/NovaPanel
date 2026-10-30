package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"novapanel/internal/store"
	"testing"
)

type fakeClock struct{ now int64 }

func (c *fakeClock) Now() int64 { return c.now }

type scheduledStore struct {
	Persistence
	rows                []store.Schedule
	calls               int
	job                 Job
	claim               *store.JobClaim
	expected, next, now int64
	state               json.RawMessage
	err                 error
	enqueue             bool
}

func (s *scheduledStore) DueSchedules(context.Context, int64) ([]store.Schedule, error) {
	return s.rows, nil
}
func (s *scheduledStore) DeleteExpiredClaims(context.Context) error { return nil }
func (s *scheduledStore) EnqueueScheduledJob(_ context.Context, id string, expected, next, now int64, state json.RawMessage, j Job, c *store.JobClaim) (bool, error) {
	s.calls++
	s.job = j
	s.claim = c
	s.expected = expected
	s.next = next
	s.now = now
	s.state = state
	return s.enqueue, s.err
}
func TestSchedulerUsesSingleAtomicFiringAndStoredAuthority(t *testing.T) {
	p := &scheduledStore{enqueue: true, rows: []store.Schedule{{ID: "schedule", Verb: "reconcile", Cron: "minutely", NextRun: 60000, Args: json.RawMessage(`{"actor":"forged"}`), State: json.RawMessage(`{"n":3}`), Actor: "stored", Role: "owner", CreatedBy: "expired-session", Scopes: []string{"system:write"}, Automation: true}}}
	a := &fakeAudit{}
	q := NewQueue(p, nil, a)
	c := &fakeClock{60000}
	q.SetClock(c)
	s := NewScheduler(q, c, map[string]SchedulePolicy{"reconcile": {Effectful: true, ObjectKey: func(json.RawMessage) (string, error) { return "system", nil }}})
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 || p.expected != 60000 || p.next != 120000 || p.job.Actor != "stored" || p.job.CreatedBy != "expired-session" || !p.job.Effectful || !p.job.Automation || p.job.ObjectKey != "system" {
		t.Fatalf("bad firing %+v", p)
	}
	var state map[string]any
	if err := json.Unmarshal(p.state, &state); err != nil {
		t.Fatal(err)
	}
	if state["n"] != float64(3) || state["job_id"] != p.job.ID {
		t.Fatalf("lost state %s", p.state)
	}
	if len(a.events) != 1 || a.events[0].Outcome != "ok" || a.events[0].Detail["stage"] != "enqueue" {
		t.Fatalf("audit %+v", a.events)
	}
}
func TestSchedulerCASLossOrBusyDoesNotAuditEnqueue(t *testing.T) {
	p := &scheduledStore{rows: []store.Schedule{{ID: "s", Verb: "reconcile", Cron: "minutely", NextRun: 60000, Args: json.RawMessage(`{}`), State: json.RawMessage(`{}`), Role: "owner", CreatedBy: "local-root"}}}
	a := &fakeAudit{}
	q := NewQueue(p, nil, a)
	s := NewScheduler(q, &fakeClock{60000}, map[string]SchedulePolicy{"reconcile": {}})
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 || p.next != 120000 || len(a.events) != 0 {
		t.Fatalf("bad skipped firing %+v", p)
	}
	p.err = errors.New("transaction failed")
	if err := s.Tick(context.Background()); err == nil {
		t.Fatal("ignored transaction failure")
	}
}
