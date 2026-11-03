package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSchedulerReturnsRecoveryBlockedFromTickEffect(t *testing.T) {
	q, _, _, ctx := fixture(t)
	if err := q.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	q.SetMutationAdmission(func() error { return errors.Join(ErrRecoveryBlocked, errors.New("unresolved publication")) })
	s := NewScheduler(q, nil, nil)
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(run) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrRecoveryBlocked) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler suppressed fatal tick error")
	}
}
