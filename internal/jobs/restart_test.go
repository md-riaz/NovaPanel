package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestWorkerOldestFirstSuccessfulRestartRecovery(t *testing.T) {
	q, s, _, ctx := fixture(t)
	c := &fakeClock{now: 1000}
	q.SetClock(c)
	a, e := q.Enqueue(ctx, "reconcile", nil, "same")
	if e != nil {
		t.Fatal(e)
	}
	c.now++
	b, e := q.Enqueue(ctx, "reconcile", nil, "same")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.StartJob(ctx, a.Job.ID, c.now); e != nil {
		t.Fatal(e)
	}
	// Model an idempotent executor's existing external state after interrupted run.
	effects := map[string]bool{a.Job.ID: true}
	order := []string{}
	q.SetRunner(func(_ context.Context, j *Job) error {
		order = append(order, j.ID)
		effects[j.ID] = true
		j.Result = json.RawMessage(`{"ok":true}`)
		return nil
	})
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- q.Run(run) }()
	for n := 0; n < 100; n++ {
		j, _ := s.GetJob(ctx, b.Job.ID)
		if j.State == "done" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if len(order) != 2 || order[0] != a.Job.ID || order[1] != b.Job.ID || len(effects) != 2 {
		t.Fatalf("order=%v effects=%v", order, effects)
	}
	j, e := s.GetJob(ctx, a.Job.ID)
	if e != nil || j.State != "done" || j.Attempts != 2 {
		t.Fatalf("recovered %+v %v", j, e)
	}
}
