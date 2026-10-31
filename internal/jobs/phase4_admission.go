package jobs

import (
	"context"
	"errors"

	"novapanel/internal/store"
)

// PrepareOperations is a synchronous startup boundary, before consumers are wired.
// Failed recovery keeps admission closed; retry requires a new startup pass.
func (q *Queue) PrepareOperations(ctx context.Context) error {
	q.operationMu.Lock()
	defer q.operationMu.Unlock()
	q.operationsReady = false
	s, ok := q.store.(interface {
		ExpireOperations(context.Context) (int, error)
		PendingOperations(context.Context) ([]store.Job, error)
	})
	if !ok {
		return errors.Join(ErrRecoveryBlocked, errors.New("operation recovery unavailable"))
	}
	if err := q.healthy(); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := s.ExpireOperations(ctx)
		if err != nil {
			return errors.Join(ErrRecoveryBlocked, err)
		}
		if n == 0 {
			break
		}
	}
	if err := q.store.RecoverJobs(ctx, q.clock.Now()); err != nil {
		return errors.Join(ErrRecoveryBlocked, err)
	}
	for {
		pending, err := s.PendingOperations(ctx)
		if err != nil {
			return errors.Join(ErrRecoveryBlocked, err)
		}
		if len(pending) == 0 {
			break
		}
		for _, j := range pending {
			if err := ctx.Err(); err != nil {
				return err
			}
			if j.State != "queued" {
				return ErrRecoveryBlocked
			}
			if err := q.execute(ctx, j); err != nil {
				return errors.Join(ErrRecoveryBlocked, err)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			op, err := q.store.(operationStore).LoadOperation(ctx, j.ID)
			if err != nil || op.Checkpoint != "done" {
				return errors.Join(ErrRecoveryBlocked, err)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := q.reconcileConfigRecords(ctx); err != nil {
		return errors.Join(ErrRecoveryBlocked, err)
	}
	q.operationsReady = true
	return nil
}
