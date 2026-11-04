package rpc

import (
	"context"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/store"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recoveryAudit struct{}

func (recoveryAudit) Record(audit.Event) error { return nil }
func (recoveryAudit) Healthy() error           { return nil }

func TestDirectMutationExcludesRecoveryAndFailedRecoveryClosesAdmission(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	q := jobs.NewQueue(st, nil, recoveryAudit{})
	if err := q.PrepareStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Jobs = q
	s := NewServer(a)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	defer func() { cancel(); unblock(); workers.Wait() }()
	var effects atomic.Int32
	s.Registry = map[string]Verb{"test": {Name: "test", Public: true, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) {
		if effects.Add(1) == 1 {
			close(entered)
			<-release
		}
		return nil, nil
	}}}
	done := make(chan Reply, 1)
	workers.Add(1)
	go func() { defer workers.Done(); done <- s.Dispatch(ctx, Request{Verb: "test", Args: []byte(`{}`)}) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	recovery := make(chan error, 1)
	workers.Add(1)
	go func() { defer workers.Done(); recovery <- q.PrepareOperations(ctx) }()
	select {
	case e := <-recovery:
		t.Fatalf("recovery overlapped RPC: %v", e)
	case <-time.After(250 * time.Millisecond):
	}
	unblock()
	select {
	case r := <-done:
		if !r.OK {
			t.Fatalf("dispatch: %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch did not finish")
	}
	select {
	case err := <-recovery:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("recovery did not finish")
	}
	// Force a fresh queue: PrepareStartup is idempotent after success.
	q2 := jobs.NewQueue(st, nil, recoveryAudit{})
	q2.SetStartupRecovery(func(context.Context) (*jobs.Job, error) { return nil, errors.New("failed recovery") }, nil)
	if err := q2.PrepareStartup(context.Background()); err == nil {
		t.Fatal("expected recovery failure")
	}
	a.Jobs = q2
	r := s.Dispatch(context.Background(), Request{Verb: "test", Args: []byte(`{}`)})
	if r.OK || r.Code != "agent_error" || effects.Load() != 1 {
		t.Fatalf("failed recovery reply=%+v handler calls=%d", r, effects.Load())
	}
}
