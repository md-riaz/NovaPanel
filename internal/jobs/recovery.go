package jobs

import (
	"context"
	"errors"
	"novapanel/internal/rpcerr"
	"time"
)

var ErrRecoveryBlocked = errors.New("jobs: startup recovery unresolved")

// SetStartupRecovery is trusted wiring, installed before Run. It cannot be invoked by RPC.
// Prepare validates the journal before the queue acquires its original running job.
func (q *Queue) SetStartupRecovery(prepare func(context.Context) (*Job, error), resume func(context.Context, *Job) error) {
	q.startup, q.resume = prepare, resume
}

func (q *Queue) recoverPublication(ctx context.Context, j Job) error {
	unlock, err := q.locks.lockContext(ctx, j.ObjectKey)
	if err != nil {
		return err
	}
	defer unlock()
	if err := q.healthy(); err != nil {
		return err
	}
	s, ok := q.store.(interface {
		ResumeTLSJob(context.Context, Job, int64) (Job, error)
	})
	if !ok {
		return errors.New("jobs: TLS recovery unavailable")
	}
	j, err = s.ResumeTLSJob(ctx, j, q.clock.Now())
	if err != nil {
		return err
	}
	err = invoke(WithMetadata(ctx, metadata(j)), q.resume, &j)
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if e := q.finish(finishCtx, j, err); e != nil {
		return e
	}
	// Only unresolved publication blocks subsequent work. Resolved failure is terminal.
	if err != nil {
		var coded *rpcerr.CodedError
		if errors.As(err, &coded) && coded.Code == "interrupted" {
			return err
		}
	}
	return nil
}

// SetMutationAdmission installs the daemon startup gate before concurrent use.
func (q *Queue) SetMutationAdmission(check func() error) { q.admission = check }

// PrepareStartup runs recovery synchronously before the daemon publishes listeners.
func (q *Queue) PrepareStartup(ctx context.Context) error {
	if err := q.rejectRecoveryReentry(ctx); err != nil {
		return err
	}
	select {
	case q.startupGate <- struct{}{}:
		defer func() { <-q.startupGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if q.startupReady {
		if err := q.operationMu.RLockContext(ctx); err != nil {
			return err
		}
		ready := q.operationsReady
		blocked := q.recoveryBlocked || q.startupBlocked
		q.operationMu.RUnlock()
		if _, ok := q.store.(operationStore); blocked || (ok && !ready) {
			return ErrRecoveryBlocked
		}
		return q.healthy()
	}
	if err := q.operationMu.LockContext(ctx); err != nil {
		return err
	}
	defer q.operationMu.Unlock()
	q.startupBlocked = true
	leaseCtx, cancelLease := context.WithCancel(ctx)
	lease := &mutationLease{queue: q, active: true, refs: 1, drained: make(chan struct{}), exclusive: true, ctx: leaseCtx, cancel: cancelLease}
	ctx = context.WithValue(leaseCtx, mutationKey{}, lease)
	defer lease.release(true)
	// Refuse even recovery mutations if the audit sinks have failed.
	if q.audit == nil {
		return errors.New("jobs: audit not configured")
	}
	if e := q.audit.Healthy(); e != nil {
		return e
	}
	if e := q.expireOperations(ctx); e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return e
	}
	for q.startup != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		j, err := q.startup(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.Join(ErrRecoveryBlocked, err)
		}
		if j == nil {
			break
		}
		if j != nil {
			if err = q.recoverPublication(ctx, *j); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return errors.Join(ErrRecoveryBlocked, err)
			}
		}
	}
	if _, ok := q.store.(operationStore); ok {
		if e := q.prepareOperationsLocked(ctx); e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return e
		}
	}
	if e := q.store.RecoverJobs(ctx, q.clock.Now()); e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return e
	}
	q.startupReady = true
	q.startupBlocked = false
	q.recoveryBlocked = false
	return nil
}
