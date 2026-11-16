package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestStartupRecoveryExcludesMutationAndCancellation(t *testing.T) {
	q, _, _, ctx := fixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	base, stop := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { stop(); unblock(); workers.Wait() }()
	q.SetStartupRecovery(func(context.Context) (*Job, error) { close(entered); <-release; return nil, nil }, nil)
	recovery := make(chan error, 1)
	workers.Add(1)
	go func() { defer workers.Done(); recovery <- q.PrepareStartup(base) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("recovery did not start")
	}
	waiting, cancel := context.WithCancel(base)
	done := make(chan error, 1)
	workers.Add(1)
	go func() { defer workers.Done(); _, err := q.Enqueue(waiting, "reconcile", nil, "system"); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("admission did not cancel")
	}
	unblock()
	select {
	case err := <-recovery:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("recovery did not finish")
	}
	if _, err := q.Enqueue(ctx, "reconcile", nil, "system"); err != nil {
		t.Fatal(err)
	}
}
