package jobs

import (
	"context"
	"errors"
	"novapanel/internal/render/nginx"
	"testing"
	"time"
)

func TestWorkerReturnsUnresolvedEffectFailure(t *testing.T) {
	q, _, _, ctx := fixture(t)
	if err := q.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	q.SetRunner(func(context.Context, *Job) error { return nginx.ErrPHPRecoveryRequired })
	if _, err := q.Enqueue(ctx, "reconcile", nil, "system"); err != nil {
		t.Fatal(err)
	}
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- q.Run(run) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrRecoveryBlocked) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker failed to report unresolved effect")
	}
}
