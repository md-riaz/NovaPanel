package jobs

import (
	"context"
	"encoding/json"
	"novapanel/internal/store"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerSkipsOtherScheduleSameObject(t *testing.T) {
	q, s, _, ctx := fixture(t)
	c := &fakeClock{now: 100000}
	q.SetClock(c)
	sch := NewScheduler(q, c, map[string]SchedulePolicy{"reconcile": {ObjectKey: func(json.RawMessage) (string, error) { return "shared", nil }}})
	a, e := sch.Set(ctx, ScheduleRequest{Verb: "reconcile", Cron: "minutely"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = sch.Set(ctx, ScheduleRequest{Verb: "reconcile", Cron: "minutely"})
	if e != nil {
		t.Fatal(e)
	}
	c.now = a.NextRun
	if e = sch.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	rows, e := s.ListJobs(ctx, store.JobFilter{})
	if e != nil || len(rows) != 1 {
		t.Fatalf("same-object firings %d %v", len(rows), e)
	}
}
func TestSchedulerAdminAuthorityPreserved(t *testing.T) {
	q, s, _, ctx := fixture(t)
	c := &fakeClock{now: 100000}
	q.SetClock(c)
	ctx = WithMetadata(ctx, Metadata{Actor: "admin", Role: "admin", CreatedBy: "user-admin"})
	sch := NewScheduler(q, c, map[string]SchedulePolicy{"reconcile": {}})
	row, e := sch.Set(ctx, ScheduleRequest{Verb: "reconcile", Cron: "minutely"})
	if e != nil {
		t.Fatal(e)
	}
	c.now = row.NextRun
	if e = sch.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	jobs, e := s.ListJobs(ctx, store.JobFilter{})
	if e != nil || len(jobs) != 1 || jobs[0].Role != "admin" {
		t.Fatalf("authority %v %v", jobs, e)
	}
}
func TestWorkerAuditFailureNeverRuns(t *testing.T) {
	q, _, a, ctx := fixture(t)
	_, e := q.Enqueue(ctx, "service.restart", nil, "x")
	if e != nil {
		t.Fatal(e)
	}
	a.fail = true
	ran := false
	q.SetRunner(func(context.Context, *Job) error { ran = true; return nil })
	if e = q.Run(ctx); e == nil || ran {
		t.Fatalf("failed open: ran=%v err=%v", ran, e)
	}
}
func TestClaimExpiry(t *testing.T) {
	q, _, _, ctx := fixture(t)
	c := &fakeClock{now: 1000}
	q.SetClock(c)
	r, e := q.Enqueue(ctx, "reconcile", nil, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = q.PublishClaim(ctx, r.Job.ID, map[string]any{"password": "s"}); e != nil {
		t.Fatal(e)
	}
	c.now += int64((6 * time.Minute) / time.Millisecond)
	if _, e = q.Claim(ctx, r.Job.ID, r.ClaimToken); e == nil {
		t.Fatal("expired claim accepted")
	}
}
func TestKeyedLockSerializesAndReleasesReferences(t *testing.T) {
	var k keyedLock
	var active atomic.Int32
	var overlap atomic.Bool
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := k.lock("same")
			defer unlock()
			if active.Add(1) != 1 {
				overlap.Store(true)
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
		}()
	}
	wg.Wait()
	if overlap.Load() || len(k.keys) != 0 {
		t.Fatalf("overlap=%v retained=%d", overlap.Load(), len(k.keys))
	}
}
