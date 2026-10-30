package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
)

func TestSchedulesEventsSettings(t *testing.T) {
	s := fresh(t)
	ctx := context.Background()
	v := Schedule{ID: "s", Verb: "reconcile", Args: json.RawMessage(`{}`), State: json.RawMessage(`{}`), Cron: "daily", NextRun: 10, Enabled: true, Actor: "root@local", Role: "owner", CreatedBy: "root@local", Scopes: []string{"system:write"}}
	if e := s.PutSchedule(ctx, v); e != nil {
		t.Fatal(e)
	}
	got, e := s.GetSchedule(ctx, "s")
	if e != nil || got.Role != "owner" || len(got.Scopes) != 1 {
		t.Fatal(got, e)
	}
	due, e := s.DueSchedules(ctx, 10)
	if e != nil || len(due) != 1 {
		t.Fatal(due, e)
	}
	if e = s.AdvanceSchedule(ctx, "s", 10, 20, 10, json.RawMessage(`{"n":1}`)); e != nil {
		t.Fatal(e)
	}
	if e = s.AdvanceSchedule(ctx, "s", 10, 20, 10, json.RawMessage(`{}`)); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
	if e = s.SetScheduleEnabled(ctx, "s", false); e != nil {
		t.Fatal(e)
	}
	due, e = s.DueSchedules(ctx, 30)
	if e != nil || len(due) != 0 {
		t.Fatal(due, e)
	}
	all, e := s.ListSchedules(ctx)
	if e != nil || len(all) != 1 {
		t.Fatal(all, e)
	}
	if e = s.SetSetting(ctx, "sealed", json.RawMessage(`{"ciphertext":"aaa"}`)); e != nil {
		t.Fatal(e)
	}
	b, e := s.Setting(ctx, "sealed")
	if e != nil || string(b) != `{"ciphertext":"aaa"}` {
		t.Fatal(string(b), e)
	}
	ev := Event{ID: "e", Actor: "root", Verb: "ping", Outcome: "ok", TS: 1, Detail: json.RawMessage(`{}`)}
	if e = s.InsertEvent(ctx, ev); e != nil {
		t.Fatal(e)
	}
	events, e := s.ListEvents(ctx, 10, "")
	if e != nil || len(events) != 1 || events[0].ID != "e" {
		t.Fatal(events, e)
	}
}
