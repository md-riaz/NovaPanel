package jobs

import (
	"context"
	"encoding/json"
	"novapanel/internal/store"
	"testing"
)

func TestTokenAuthorityDoesNotNeedRoleOrAuditLabel(t *testing.T) {
	q, s, _, ctx := fixture(t)
	ctx = WithMetadata(ctx, Metadata{CreatedBy: "token-id", Scopes: []string{"system:write"}, Automation: true})
	r, err := q.Enqueue(ctx, "reconcile", map[string]any{}, "system")
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.GetJob(ctx, r.Job.ID)
	if err != nil || j.CreatedBy != "token-id" || len(j.Scopes) != 1 {
		t.Fatalf("%+v %v", j, err)
	}
}
func TestUnhealthyAuditBlocksClaimsButNotStatus(t *testing.T) {
	q, _, a, ctx := fixture(t)
	r, err := q.Enqueue(ctx, "reconcile", map[string]any{}, "system")
	if err != nil {
		t.Fatal(err)
	}
	if err = q.PublishClaim(ctx, r.Job.ID, map[string]any{"password": "secret"}); err != nil {
		t.Fatal(err)
	}
	a.fail = true
	if _, err = q.Status(ctx, r.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = q.Claim(ctx, r.Job.ID, r.ClaimToken); err == nil {
		t.Fatal("claim mutated with failed audit")
	}
}
func TestBusyScheduledOccurrenceAdvancesWithoutRetry(t *testing.T) {
	q, db, _, ctx := fixture(t)
	clock := &fakeClock{60000}
	q.SetClock(clock)
	sch := NewScheduler(q, clock, map[string]SchedulePolicy{"reconcile": {ObjectKey: func(json.RawMessage) (string, error) { return "system", nil }}})
	row, err := sch.Set(ctx, ScheduleRequest{Verb: "reconcile", Cron: "minutely"})
	if err != nil {
		t.Fatal(err)
	}
	active, err := q.Enqueue(ctx, "reconcile", map[string]any{}, "system")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.StartJob(ctx, active.Job.ID, clock.now); err != nil {
		t.Fatal(err)
	}
	clock.now = row.NextRun
	if err = sch.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated, err := db.GetSchedule(ctx, row.ID)
	if err != nil || updated.NextRun <= clock.now {
		t.Fatalf("%+v %v", updated, err)
	}
	running, err := db.GetJob(ctx, active.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	running.State = "done"
	if err = db.UpdateJob(ctx, running); err != nil {
		t.Fatal(err)
	}
	if err = sch.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	all, err := db.ListJobs(ctx, store.JobFilter{})
	if err != nil || len(all) != 1 {
		t.Fatalf("skipped occurrence retried: %v %v", all, err)
	}
	clock.now = updated.NextRun
	if err = sch.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	all, err = db.ListJobs(ctx, store.JobFilter{})
	if err != nil || len(all) != 2 {
		t.Fatalf("next occurrence absent: %v %v", all, err)
	}
}
