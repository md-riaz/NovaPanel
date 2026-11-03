package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMutationLeaseNestedWriterAndRevocation(t *testing.T) {
	q, _, _, ctx := fixture(t)
	if err := q.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	outer, release, err := q.AdmitMutation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	recovery := make(chan error, 1)
	go func() { recovery <- q.PrepareOperations(ctx) }()
	// Weighted semaphore rejects new readers once the writer is queued. The
	// outer lease consumes only one permit, so capacity alone cannot cause this.
	deadline := time.Now().Add(time.Second)
	for q.operationMu.TryRLock() {
		q.operationMu.RUnlock()
		if time.Now().After(deadline) {
			release()
			select {
			case <-recovery:
			case <-time.After(time.Second):
				t.Error("recovery did not exit")
			}
			t.Fatal("recovery writer did not queue")
		}
		time.Sleep(time.Millisecond)
	}
	// A waiting writer must not stop nested admission.
	nested, endNested, err := q.AdmitMutation(context.WithoutCancel(outer))
	if err != nil {
		release()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { release(); close(done) }()
	select {
	case <-nested.Done():
	case <-time.After(time.Second):
		t.Fatal("nested context not cancelled")
	}
	select {
	case <-done:
		t.Fatal("root returned before child drained")
	default:
	}
	endNested()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("root did not drain")
	}
	if _, _, err := q.AdmitMutation(context.WithoutCancel(outer)); !errors.Is(err, ErrRecoveryBlocked) {
		t.Fatalf("stale lease admitted: %v", err)
	}
	select {
	case err := <-recovery:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("recovery did not complete")
	}
}
