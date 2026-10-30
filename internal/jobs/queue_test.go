package jobs

import (
	"context"
	"errors"
	"novapanel/internal/audit"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
	"time"
)

type fakeAudit struct {
	fail   bool
	events []audit.Event
}

func (a *fakeAudit) Record(e audit.Event) error {
	if a.fail {
		return errors.New("audit unavailable")
	}
	a.events = append(a.events, e)
	return nil
}
func (a *fakeAudit) Healthy() error {
	if a.fail {
		return errors.New("audit unavailable")
	}
	return nil
}
func fixture(t *testing.T) (*Queue, *store.Store, *fakeAudit, context.Context) {
	t.Helper()
	s, e := store.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	seal, e := secret.New(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	a := &fakeAudit{}
	q := NewQueue(s, seal, a)
	ctx := WithMetadata(context.Background(), Metadata{Actor: "root", Role: "owner", CreatedBy: "local-root"})
	return q, s, a, ctx
}
func TestEnqueueDurableAndTrustedMetadata(t *testing.T) {
	q, s, _, ctx := fixture(t)
	r, e := q.Enqueue(ctx, "reconcile", map[string]any{"actor": "attacker", "effectful": true}, "system")
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.GetJob(ctx, r.Job.ID)
	if e != nil {
		t.Fatal(e)
	}
	if j.Actor != "root" || j.Effectful || j.State != "queued" || r.ClaimToken == "" {
		t.Fatalf("bad enqueue: %+v", r)
	}
	if _, e = q.Enqueue(context.Background(), "reconcile", nil, ""); e == nil {
		t.Fatal("missing authority accepted")
	}
}
func TestWorkerRecoveryPanicAndClaims(t *testing.T) {
	q, s, a, ctx := fixture(t)
	first, e := q.Enqueue(ctx, "reconcile", nil, "a")
	if e != nil {
		t.Fatal(e)
	}
	second, e := q.Enqueue(WithMetadata(ctx, Metadata{Actor: "root", Role: "owner", CreatedBy: "local-root", Effectful: true}), "service.restart", nil, "b")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.StartJob(ctx, first.Job.ID, 1); e != nil {
		t.Fatal(e)
	}
	if _, e = s.StartJob(ctx, second.Job.ID, 1); e != nil {
		t.Fatal(e)
	}
	q.SetRunner(func(runCtx context.Context, j *Job) error {
		m, ok := MetadataFrom(runCtx)
		if !ok || m.CreatedBy != "local-root" {
			t.Error("lost identity")
		}
		panic("private secret")
	})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- q.Run(runCtx) }()
	for n := 0; n < 100; n++ {
		j, _ := q.Status(ctx, first.Job.ID)
		if j.State == "failed" {
			break
		}
		time.Sleep(time.Millisecond * 5)
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	j, e := q.Status(ctx, first.Job.ID)
	if e != nil || j.Code != "agent_error" || j.Attempts != 2 || j.Error == "private secret" {
		t.Fatalf("panic job %+v %v", j, e)
	}
	j, e = q.Status(ctx, second.Job.ID)
	if e != nil || j.Code != "interrupted" {
		t.Fatalf("effectful recovery %+v %v", j, e)
	}
	if len(a.events) < 3 {
		t.Fatal("missing audit")
	}
	if e = q.PublishClaim(ctx, first.Job.ID, map[string]any{"password": "secret"}); e != nil {
		t.Fatal(e)
	}
	wrong := WithMetadata(ctx, Metadata{CreatedBy: "someone"})
	if _, e = q.Claim(wrong, first.Job.ID, first.ClaimToken); e == nil {
		t.Fatal("wrong owner")
	}
	fields, e := q.Claim(ctx, first.Job.ID, first.ClaimToken)
	if e != nil || fields["password"] != "secret" {
		t.Fatalf("claim %v %v", fields, e)
	}
	if _, e = q.Claim(ctx, first.Job.ID, first.ClaimToken); e == nil {
		t.Fatal("claim reused")
	}
}
func TestAuditFailureBlocksEnqueue(t *testing.T) {
	q, s, a, ctx := fixture(t)
	a.fail = true
	if _, e := q.Enqueue(ctx, "service.restart", nil, "nginx"); e == nil {
		t.Fatal("audit failure ignored")
	}
	rows, e := s.ListJobs(ctx, store.JobFilter{})
	if e != nil || len(rows) != 0 {
		t.Fatalf("mutation despite audit failure: %v %v", rows, e)
	}
}
