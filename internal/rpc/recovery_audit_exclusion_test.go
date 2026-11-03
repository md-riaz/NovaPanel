package rpc

import (
	"context"
	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/store"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMutationCompletionAuditExcludesRecoveryOnPanic(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	q := jobs.NewQueue(st, nil, recoveryAudit{})
	if err = q.PrepareStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Jobs = q
	s := NewServer(a)
	s.Registry = map[string]Verb{"test": {Name: "test", Public: true, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) { panic("test") }}}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	defer func() { cancel(); unblock(); workers.Wait() }()
	calls := 0
	s.DurableAudit = func(AuditEvent) error {
		calls++
		if calls == 2 {
			close(entered)
			<-release
		}
		return nil
	}
	done := make(chan Reply, 1)
	workers.Add(1)
	go func() { defer workers.Done(); done <- s.Dispatch(ctx, Request{Verb: "test", Args: []byte(`{}`)}) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("completion audit did not start")
	}
	recovery := make(chan error, 1)
	workers.Add(1)
	go func() { defer workers.Done(); recovery <- q.PrepareOperations(ctx) }()
	select {
	case e := <-recovery:
		t.Fatalf("recovery overlapped completion audit: %v", e)
	case <-time.After(250 * time.Millisecond):
	}
	unblock()
	select {
	case r := <-done:
		if r.OK || r.Code != "agent_error" {
			t.Fatalf("panic reply: %+v", r)
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
}
