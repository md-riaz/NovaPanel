package jobs

import (
	"bytes"
	"context"
	"errors"
	"novapanel/internal/rpcerr"
	"testing"
	"time"
)

func TestWorkerMixedErrorPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		state, code string
		effectful   bool
	}{
		{"retry before terminal", errors.Join(ErrRetry, rpcerr.Errf("precondition_failed", "secret")), "queued", "", false},
		{"terminal before retry", errors.Join(rpcerr.Errf("agent_error", "secret"), ErrRetry), "queued", "", false},
		{"terminal with deadline", errors.Join(rpcerr.Errf("precondition_failed", "secret"), context.DeadlineExceeded), "failed", "precondition_failed", false},
		{"interrupted with deadline and retry", errors.Join(rpcerr.Errf("interrupted", "secret"), context.DeadlineExceeded, ErrRetry), "queued", "", false},
		{"interrupted after other code", errors.Join(rpcerr.Errf("agent_error", "secret"), ErrRetry, rpcerr.Errf("interrupted", "secret")), "queued", "", false},
		{"journal recovery", errors.Join(ErrRecoveryRequired, rpcerr.Errf("interrupted", "secret")), "failed", "interrupted", false},
		{"effectful interruption", rpcerr.Errf("interrupted", "secret"), "failed", "interrupted", true},
		{"coded deadline first", errors.Join(rpcerr.Errf("deadline_exceeded", "secret"), rpcerr.Errf("precondition_failed", "secret")), "failed", "precondition_failed", false},
		{"coded deadline last", errors.Join(rpcerr.Errf("precondition_failed", "secret"), rpcerr.Errf("deadline_exceeded", "secret")), "failed", "precondition_failed", false},
		{"effectful deadline first", errors.Join(rpcerr.Errf("deadline_exceeded", "secret"), rpcerr.Errf("precondition_failed", "secret")), "failed", "interrupted", true},
		{"effectful deadline last", errors.Join(rpcerr.Errf("precondition_failed", "secret"), rpcerr.Errf("deadline_exceeded", "secret")), "failed", "interrupted", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, s, _, ctx := fixture(t)
			admission := ctx
			if tc.effectful {
				admission = WithMetadata(ctx, Metadata{CreatedBy: "owner", Effectful: true})
			}
			r, err := q.EnqueueSealed(admission, "test.slow", map[string]string{"secret": "preserve"}, "object")
			if err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{}, 1)
			q.SetRunner(func(context.Context, *Job) error {
				select {
				case entered <- struct{}{}:
				default:
				}
				return tc.err
			})
			workerCtx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- q.Run(workerCtx) }()
			defer func() {
				cancel()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not run")
			}
			limit := time.After(5 * time.Second)
			for {
				j, err := s.GetJob(ctx, r.Job.ID)
				if err != nil {
					t.Fatal(err)
				}
				if j.Attempts > 0 && j.State == tc.state {
					if j.Code != tc.code || j.Error == "secret" {
						t.Fatalf("outcome: %+v", j)
					}
					if (tc.state == "queued" || tc.code == "interrupted") && !bytes.Equal(j.Args, r.Job.Args) {
						t.Fatal("recovery input changed")
					}
					break
				}
				select {
				case <-limit:
					t.Fatalf("outcome: %+v", j)
				default:
					time.Sleep(time.Millisecond)
				}
			}
		})
	}
}

func TestExpiredAttemptPreservesTerminalErrors(t *testing.T) {
	for _, code := range []string{"precondition_failed", "agent_error"} {
		t.Run(code, func(t *testing.T) {
			q, s, _, ctx := fixture(t)
			r, err := q.Enqueue(ctx, "test.slow", nil, "object")
			if err != nil {
				t.Fatal(err)
			}
			q.SetAttemptDeadline(func(string) time.Duration { return time.Millisecond })
			q.SetRunner(func(ctx context.Context, _ *Job) error {
				<-ctx.Done()
				return rpcerr.Errf(code, "terminal error")
			})
			if err := q.execute(ctx, r.Job); err != nil {
				t.Fatal(err)
			}
			j, err := s.GetJob(ctx, r.Job.ID)
			if err != nil || j.State != "failed" || j.Code != code {
				t.Fatalf("terminal outcome: %+v %v", j, err)
			}
		})
	}
}
