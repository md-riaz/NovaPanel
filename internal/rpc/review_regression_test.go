package rpc_test

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	_ "novapanel/internal/verbs"
	"path/filepath"
	"testing"
)

func regressionServer(t *testing.T) *rpc.Server {
	t.Helper()
	st, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	log, e := audit.Open(st, filepath.Join(t.TempDir(), "audit"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { log.Close() })
	seal, e := secret.New(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	a := agent.New()
	a.Store = st
	a.Audit = log
	a.Secret = seal
	s := rpc.NewServer(a)
	s.ConfigureJobs()
	return s
}
func TestRegressionMissingScheduleUpdate(t *testing.T) {
	s := regressionServer(t)
	r := s.Dispatch(rpc.WithCaller(context.Background(), rpc.RootCaller(0)), rpc.Request{Verb: "schedule.set", Args: json.RawMessage(`{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","verb":"reconcile","args":{},"cron":"daily"}`)})
	if r.Code != "not_found" {
		t.Fatalf("missing schedule update returned %+v; want not_found", r)
	}
}
func TestRegressionClaimAuditStage(t *testing.T) {
	s := regressionServer(t)
	ctx := context.Background()
	en, e := s.Agent.Jobs.Enqueue(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "local-root", Role: "owner"}), "reconcile", map[string]any{}, "system")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Agent.Jobs.PublishClaim(ctx, en.Job.ID, map[string]any{"password": "one"}); e != nil {
		t.Fatal(e)
	}
	var last rpc.AuditEvent
	s.Audit = rpc.AuditFunc(func(e rpc.AuditEvent) { last = e })
	raw, _ := json.Marshal(map[string]string{"job_id": en.Job.ID, "claim": en.ClaimToken})
	r := s.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "job.claim", Args: raw})
	if !r.OK {
		t.Fatal(r)
	}
	if last.Detail["stage"] != "claim" {
		t.Fatalf("claim audit stage = %v; want claim", last.Detail["stage"])
	}
}
