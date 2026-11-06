package jobs_test

import (
	"context"
	"path/filepath"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	_ "novapanel/internal/verbs"
)

type clock struct{ now int64 }

func (c *clock) Now() int64 { return c.now }

func TestReservedReconcileRPCAndRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "panel.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	log, err := audit.Open(st, filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{now: 1000}
	a := agent.New()
	a.Store = st
	a.Audit = log
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	a.Scheduler = jobs.NewScheduler(a.Jobs, c, map[string]jobs.SchedulePolicy{"reconcile": {}})
	if err := a.Scheduler.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	admin := rpc.Caller{Kind: "user", Role: "admin", Session: "admin-session"}
	r := server.Dispatch(rpc.WithCaller(ctx, admin), rpc.Request{Verb: "schedule.disable", Args: []byte(`{"id":"reconcile:auto"}`)})
	if r.OK || r.Code != "forbidden" {
		t.Fatalf("disable: %+v", r)
	}
	row, err := st.GetSchedule(ctx, "reconcile:auto")
	if err != nil || !row.Enabled {
		t.Fatalf("row: %+v %v", row, err)
	}
	// Simulate a row disabled by the old release and restart the actual store and scheduler.
	if err := st.SetScheduleEnabled(ctx, row.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err = audit.Open(st, filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	q := jobs.NewQueue(st, nil, log)
	sch := jobs.NewScheduler(q, c, map[string]jobs.SchedulePolicy{"reconcile": {}})
	if err := sch.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	healed, err := st.GetSchedule(ctx, row.ID)
	if err != nil || !healed.Enabled || healed.NextRun != row.NextRun {
		t.Fatalf("healed: %+v %v", healed, err)
	}
	c.now = row.NextRun
	if err := sch.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	queued, err := st.ListJobs(ctx, store.JobFilter{Verb: "reconcile"})
	if err != nil || len(queued) != 1 || queued[0].CreatedBy != "local-root" {
		t.Fatalf("queued: %+v %v", queued, err)
	}
}
