package store

import (
	"context"
	"testing"
)

func TestReconcileSchedulePersistsCadence(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	if err := s.PrepareReconcileSchedule(ctx, 100); err != nil {
		t.Fatal(err)
	}
	first, err := s.GetSchedule(ctx, "reconcile:auto")
	if err != nil {
		t.Fatal(err)
	}
	if first.NextRun != 100+24*60*60*1000 || first.Verb != "reconcile" || first.CreatedBy != "local-root" {
		t.Fatalf("unexpected schedule: %+v", first)
	}
	if err := s.SetScheduleEnabled(ctx, "reconcile:auto", false); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareReconcileSchedule(ctx, 500); err != nil {
		t.Fatal(err)
	}
	next, err := s.GetSchedule(ctx, "reconcile:auto")
	if err != nil || next.NextRun != first.NextRun || !next.Enabled {
		t.Fatalf("cadence reset: %+v %v", next, err)
	}
}
