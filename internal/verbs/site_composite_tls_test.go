package verbs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"novapanel/internal/render/nginx"
	"novapanel/internal/store"
)

func TestCompositePrimaryTLSPublishesAndAcknowledgesAudit(t *testing.T) {
	f := newPrimaryRestart(t, t.TempDir(), false)
	defer f.close(t)
	ctx := context.Background()
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- f.a.Jobs.Run(run) }()
	defer func() { cancel(); <-done }()
	var j store.Job
	var err error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err = f.st.GetJob(ctx, f.job)
		if err == nil && (j.State == "done" || j.State == "failed") {
			break
		}
		if err != nil && !errors.Is(err, store.ErrPublicationPending) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || j.State != "done" || j.Step != "phase4_committed" {
		t.Fatalf("parent: %+v %v", j, err)
	}
	children, err := f.st.OperationChildren(ctx, f.job)
	if err != nil || len(children) != 2 || children[0].Kind != "domain_tls" || children[0].Status != "committed" || children[1].Status != "committed" {
		t.Fatalf("children: %+v %v", children, err)
	}
	intent, err := f.st.TLSIntent(ctx)
	if err != nil || len(intent) != 0 {
		t.Fatalf("TLS intent: %s %v", intent, err)
	}
	op, err := f.st.LoadOperation(ctx, f.job)
	if err != nil || op.Identity.PublicationAudit == nil || op.Identity.PublicationAudit.State != "acknowledged" {
		t.Fatalf("audit: %+v %v", op, err)
	}
	domain, err := f.st.DomainByID(ctx, f.newID)
	if err != nil || !domain.IsPrimary {
		t.Fatalf("primary: %+v %v", domain, err)
	}
}

func TestCompositePrimaryTLSReplayAfterInterruptedPublication(t *testing.T) {
	root := t.TempDir()
	f := newPrimaryRestart(t, root, false)
	f.a.Nginx.Probe = func(context.Context, nginx.Desired) error { panic("interruption") }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = f.a.Jobs.Run(ctx)
	cancel()
	intent, err := f.st.TLSIntent(context.Background())
	if err != nil || len(intent) == 0 {
		t.Fatalf("TLS intent missing: %s %v", intent, err)
	}
	f.close(t)
	f.reopen(t, root)
	defer f.close(t)
	f.a.Nginx.Probe = func(context.Context, nginx.Desired) error { return nil }
	run, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.a.Jobs.Run(run) }()
	defer func() { stop(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, e := f.st.GetJob(context.Background(), f.job)
		if e == nil && j.State == "done" {
			intent, e = f.st.TLSIntent(context.Background())
			if e != nil || len(intent) != 0 {
				t.Fatalf("TLS intent remains: %s %v", intent, e)
			}
			op, e := f.st.LoadOperation(context.Background(), f.job)
			if e != nil || op.Identity.PublicationAudit == nil || op.Identity.PublicationAudit.State != "acknowledged" {
				t.Fatalf("audit: %+v %v", op, e)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("composite TLS replay did not finish")
}
