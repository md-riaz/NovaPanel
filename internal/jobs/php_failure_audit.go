package jobs

import (
	"context"
	"errors"
	"novapanel/internal/id"
	"novapanel/internal/store"
)

func (q *Queue) finishRestoredPHP(ctx context.Context, j Job) error {
	s, ok := q.store.(interface {
		PreparePHPFailureAudit(context.Context, string, string) (string, error)
		FailRestoredPHP(context.Context, string, string) error
		LoadOperation(context.Context, string) (store.Operation, error)
	})
	if !ok {
		return q.latchPublication(ErrRecoveryBlocked)
	}
	eid, err := id.New()
	if err != nil {
		return q.latchPublication(err)
	}
	eid, err = s.PreparePHPFailureAudit(ctx, j.ID, eid)
	if err != nil {
		return q.latchPublication(err)
	}
	if err = q.publicationRecord(j, eid, "restored", "error", j.Code); err != nil {
		return err
	}
	if err = s.FailRestoredPHP(ctx, j.ID, j.Code); err != nil {
		op, readErr := s.LoadOperation(context.WithoutCancel(ctx), j.ID)
		if readErr != nil {
			return q.latchPublication(errors.Join(err, readErr))
		}
		// Regardless of outcome, an uncertain terminal write must close admission.
		if op.Checkpoint == "expired" && op.Reconciled {
			return q.latchPublication(err)
		}
		return q.latchPublication(err)
	}
	return q.reconcileConfigRecord(ctx, j.ID)
}
