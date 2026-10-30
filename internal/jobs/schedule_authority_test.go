package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"testing"
)

func TestScheduleSetUsesBoundaryAuthorityAndNoOwnerOnlyGate(t *testing.T) {
	q, db, _, ctx := fixture(t)
	sch := NewScheduler(q, &fakeClock{60000}, map[string]SchedulePolicy{"reconcile": {}})
	row, err := sch.Set(ctx, ScheduleRequest{Verb: "reconcile", Cron: "daily"})
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithMetadata(context.Background(), Metadata{CreatedBy: "different-token", Scopes: []string{"system:write"}})
	row, err = sch.Set(ctx, ScheduleRequest{ID: row.ID, Verb: "reconcile", Cron: "hourly", Args: json.RawMessage(`{"role":"owner","created_by":"forged"}`)})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := db.GetSchedule(ctx, row.ID)
	if err != nil || saved.CreatedBy != "different-token" || saved.Role != "" {
		t.Fatalf("%+v %v", saved, err)
	}
	if err = sch.Enable(ctx, row.ID, false); err != nil {
		t.Fatal(err)
	}
	saved, err = db.GetSchedule(ctx, row.ID)
	if err != nil || saved.Enabled {
		t.Fatalf("%+v %v", saved, err)
	}
	if _, err = sch.Set(ctx, ScheduleRequest{Verb: "reconcile", Cron: "daily", Args: json.RawMessage(`[]`)}); err == nil {
		t.Fatal("non-object args")
	}
	if _, err = sch.Set(ctx, ScheduleRequest{Verb: "firewall.apply", Cron: "daily"}); err == nil {
		t.Fatal("dangerous verb accepted")
	}
}
func TestSchedulerMFARequiresStoredAutomation(t *testing.T) {
	q, _, _, ctx := fixture(t)
	sch := NewScheduler(q, &fakeClock{60000}, map[string]SchedulePolicy{"backup.create": {RequiresMFA: true}})
	_, err := sch.Set(ctx, ScheduleRequest{Verb: "backup.create", Cron: "daily"})
	var ce *rpcerr.CodedError
	if !errors.As(err, &ce) || ce.Code != "forbidden" {
		t.Fatalf("want forbidden got %v", err)
	}
	ctx = WithMetadata(ctx, Metadata{CreatedBy: "automation-token", Automation: true, Scopes: []string{"backups:write"}})
	if _, err = sch.Set(ctx, ScheduleRequest{Verb: "backup.create", Cron: "daily"}); err != nil {
		t.Fatal(err)
	}
}
func TestClaimPublicationRequiresHealthyAudit(t *testing.T) {
	q, _, a, ctx := fixture(t)
	r, err := q.Enqueue(ctx, "reconcile", map[string]any{}, "system")
	if err != nil {
		t.Fatal(err)
	}
	a.fail = true
	if err = q.PublishClaim(ctx, r.Job.ID, map[string]any{"password": "secret"}); err == nil {
		t.Fatal("published despite failed audit")
	}
}
func TestDelayedClaimTTLStartsAtPublication(t *testing.T) {
	clock := &fakeClock{1000}
	db, err := store.OpenWithClock(t.TempDir()+"/claims.db", clock)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q, _, _, ctx := fixture(t)
	q.store = db
	q.SetClock(clock)
	r, err := q.Enqueue(ctx, "reconcile", map[string]any{}, "system")
	if err != nil {
		t.Fatal(err)
	}
	clock.now += 600000
	if err = q.PublishClaim(ctx, r.Job.ID, map[string]any{"password": "secret"}); err != nil {
		t.Fatal(err)
	}
	clock.now += 299999
	fields, err := q.Claim(ctx, r.Job.ID, r.ClaimToken)
	if err != nil || fields["password"] != "secret" {
		t.Fatalf("%v %v", fields, err)
	}
	if _, err = q.Claim(ctx, r.Job.ID, r.ClaimToken); err == nil {
		t.Fatal("claim reused")
	}
}
