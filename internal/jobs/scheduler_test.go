package jobs

import (
	"encoding/json"
	"novapanel/internal/store"
	"testing"
	"time"
)

type fakeClock struct{ now int64 }

func (c *fakeClock) Now() int64 { return c.now }
func TestCalendarSubset(t *testing.T) {
	after := time.Date(2026, 10, 30, 12, 30, 0, 0, time.UTC).UnixMilli()
	for _, expr := range []string{"minutely", "hourly", "daily", "weekly", "monthly", "yearly", "*-*-* 13:00:00 UTC", "Mon *-*-* 00:00:00 UTC"} {
		next, e := NextCalendar(expr, after)
		if e != nil || next <= after {
			t.Fatalf("%s %d %v", expr, next, e)
		}
	}
	for _, expr := range []string{"* * * * *", "tomorrow", "*-*-* 25:00:00 UTC", "*-*-* 12:00:00", "Mon..Fri *-*-* 00:00:00 UTC"} {
		if _, e := NextCalendar(expr, after); e == nil {
			t.Fatalf("unsupported %q accepted", expr)
		}
	}
	n, e := NextCalendar("daily", after)
	if e != nil || n != time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("daily %d %v", n, e)
	}
}
func TestSchedulerPersistedAuthorityNoOverlap(t *testing.T) {
	q, s, _, ctx := fixture(t)
	clock := &fakeClock{now: time.Date(2026, 10, 30, 12, 30, 0, 0, time.UTC).UnixMilli()}
	q.SetClock(clock)
	sched := NewScheduler(q, clock, map[string]SchedulePolicy{"reconcile": {}})
	row, e := sched.Set(ctx, ScheduleRequest{Verb: "reconcile", Args: json.RawMessage(`{}`), Cron: "minutely"})
	if e != nil {
		t.Fatal(e)
	}
	clock.now = row.NextRun
	if e = sched.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	rows, e := s.ListJobs(ctx, store.JobFilter{})
	if e != nil || len(rows) != 1 || rows[0].CreatedBy != "root@local" {
		t.Fatalf("jobs %v %v", rows, e)
	}
	row, e = s.GetSchedule(ctx, row.ID)
	if e != nil || row.LastRun == nil || row.NextRun <= clock.now {
		t.Fatalf("schedule %+v %v", row, e)
	}
	clock.now = row.NextRun
	if e = sched.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	rows, _ = s.ListJobs(ctx, store.JobFilter{})
	if len(rows) != 1 {
		t.Fatal("overlapping schedule")
	}
	if _, e = sched.Set(ctx, ScheduleRequest{Verb: "firewall.apply", Cron: "daily"}); e == nil {
		t.Fatal("dangerous verb schedulable")
	}
}
