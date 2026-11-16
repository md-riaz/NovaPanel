package jobs

import (
	"context"
	"errors"
	"testing"
)

func TestScheduleMutationsRefuseFailedStartup(t *testing.T) {
	q, _, _, ctx := fixture(t)
	s := NewScheduler(q, nil, map[string]SchedulePolicy{"reconcile": {}})
	row, err := s.Set(ctx, ScheduleRequest{Verb: "reconcile", Cron: "minutely"})
	if err != nil {
		t.Fatal(err)
	}
	fail := true
	q.SetStartupRecovery(func(context.Context) (*Job, error) {
		if fail {
			return nil, errors.New("unresolved TLS")
		}
		return nil, nil
	}, nil)
	if err = q.PrepareStartup(ctx); err == nil {
		t.Fatal("expected startup failure")
	}
	if _, err = s.Set(ctx, ScheduleRequest{Verb: "reconcile", Cron: "minutely"}); !errors.Is(err, ErrRecoveryBlocked) {
		t.Errorf("Set: %v", err)
	}
	if err = s.Enable(ctx, row.ID, false); !errors.Is(err, ErrRecoveryBlocked) {
		t.Errorf("Enable: %v", err)
	}
	rows, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].Enabled {
		t.Fatalf("schedule mutated during failed recovery: %+v", rows)
	}
	fail = false
	if err = q.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Enable(ctx, row.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Set(ctx, ScheduleRequest{Verb: "reconcile", Cron: "minutely"}); err != nil {
		t.Fatal(err)
	}
}
