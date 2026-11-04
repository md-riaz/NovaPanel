package jobs

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestRecoveryWaitsForRunningWorkerEffect(t *testing.T) {
	q, _, _, ctx := fixture(t)
	if err := q.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var workers sync.WaitGroup
	q.SetRunner(func(context.Context, *Job) error { close(entered); <-release; return nil })
	if _, err := q.Enqueue(ctx, "reconcile", nil, "system"); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer func() { cancel(); unblock(); workers.Wait() }()
	worker := make(chan error, 1)
	workers.Add(1)
	go func() { defer workers.Done(); worker <- q.Run(runCtx) }()
	select {
	case <-entered:
	case err := <-worker:
		t.Fatalf("worker exited: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start")
	}
	recovered := make(chan error, 1)
	workers.Add(1)
	go func() { defer workers.Done(); recovered <- q.PrepareOperations(runCtx) }()
	select {
	case err := <-recovered:
		t.Fatalf("recovery overlapped live worker: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-recovered:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("recovery did not finish")
	}
	cancel()
	select {
	case err := <-worker:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not finish")
	}
}
