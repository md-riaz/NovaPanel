package rpc_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	_ "novapanel/internal/verbs"
	"path/filepath"
	"strings"
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

type completionFailSink struct {
	bytes.Buffer
	writes int
}

func (s *completionFailSink) Write(p []byte) (int, error) {
	s.writes++
	if s.writes == 2 {
		return 0, errors.New("completion audit unavailable")
	}
	return s.Buffer.Write(p)
}
func (*completionFailSink) Sync() error  { return nil }
func (*completionFailSink) Close() error { return nil }

func TestRegressionClaimReplySurvivesCompletionAuditFailure(t *testing.T) {
	s := regressionServer(t)
	ctx := rpc.WithCaller(context.Background(), rpc.RootCaller(0))
	en, err := s.Agent.Jobs.Enqueue(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "local-root", Role: "owner"}), "reconcile", map[string]any{}, "system")
	if err != nil {
		t.Fatal(err)
	}
	const secretValue = "show-once-audit-regression-secret"
	if err = s.Agent.Jobs.PublishClaim(ctx, en.Job.ID, map[string]any{"password": secretValue}); err != nil {
		t.Fatal(err)
	}
	sink := &completionFailSink{}
	s.Agent.Audit = audit.New(s.Agent.Store, sink)
	t.Cleanup(func() { s.Agent.Audit.Close() })
	s = rpc.NewServer(s.Agent)
	s.ConfigureJobs()
	var logs bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldWriter) })
	var events []rpc.AuditEvent
	s.Audit = rpc.AuditFunc(func(e rpc.AuditEvent) { events = append(events, e) })
	raw, err := json.Marshal(map[string]string{"job_id": en.Job.ID, "claim": en.ClaimToken})
	if err != nil {
		t.Fatal(err)
	}
	r := s.Dispatch(ctx, rpc.Request{ID: "claim-reply", Verb: "job.claim", Args: raw})
	if !r.OK || r.ID != "claim-reply" {
		t.Fatalf("consumed claim reply lost: %+v", r)
	}
	fields, ok := r.Data.(map[string]any)
	if !ok || fields["password"] != secretValue {
		t.Fatal("claim secret not delivered")
	}
	var count int
	if err = s.Agent.Store.Tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT count(*) FROM job_claims WHERE job_id = ?", en.Job.ID).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("claim was not consumed")
	}
	if s.Agent.Audit.Healthy() == nil {
		t.Fatal("audit failure did not latch")
	}
	if !strings.Contains(logs.String(), "rpc: completion audit unavailable") {
		t.Fatalf("missing out-of-band failure: %q", logs.String())
	}
	if len(events) != 1 || events[0].Outcome != "ok" || events[0].Detail["stage"] != "claim" {
		t.Fatalf("claim audit = %+v", events)
	}
	// Recover the injected sink; the real audit health must remain sticky.
	sink.writes = 0
	r = s.Dispatch(ctx, rpc.Request{Verb: "reconcile", Args: json.RawMessage(`{}`)})
	if r.OK || r.Code != "agent_error" {
		t.Fatalf("future mutation admitted: %+v", r)
	}
	r = s.Dispatch(ctx, rpc.Request{Verb: "job.status", Args: json.RawMessage(`{"id":"` + en.Job.ID + `"}`)})
	if !r.OK {
		t.Fatalf("read blocked: %+v", r)
	}
	b, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{logs.String(), sink.String(), string(b)} {
		if strings.Contains(text, secretValue) || strings.Contains(text, en.ClaimToken) {
			t.Fatal("secret leaked to audit or log")
		}
	}
	if sink.writes != 0 {
		t.Fatal("sticky audit failure retried the recovered sink")
	}
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
