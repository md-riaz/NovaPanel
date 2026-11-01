package jobs

import (
	"context"
	"database/sql"
	"errors"

	"novapanel/internal/audit"
	"novapanel/internal/id"
	"novapanel/internal/store"
)

type stableAuditor interface {
	RecordWithID(string, audit.Event) error
}
type publicationStore interface {
	operationStore
	PreparePublicationAudit(context.Context, string, string, string) (store.PublicationAudit, error)
	AcknowledgePublicationAudit(context.Context, string) error
	PendingPublicationAudits(context.Context) ([]store.Job, error)
}

func (q *Queue) latchPublication(err error) error {
	q.mu.Lock()
	if q.publicationErr == nil {
		q.publicationErr = err
	}
	q.mu.Unlock()
	return err
}
func (q *Queue) publicationRecord(j Job, eid, publication, outcome, code string) error {
	a, ok := q.audit.(stableAuditor)
	if !ok {
		return q.latchPublication(errors.New("jobs: stable audit unavailable"))
	}
	if err := q.healthy(); err != nil {
		return err
	}
	err := a.RecordWithID(eid, audit.Event{Principal: audit.JobPrincipal(j.CreatedBy, j.Role, j.Scopes), Actor: j.Actor, Role: j.Role, Verb: j.Verb, ObjectKey: j.ObjectKey, Outcome: outcome, Code: code, Detail: map[string]any{"job_id": j.ID, "stage": "exec", "publication": publication}})
	if err != nil {
		return q.latchPublication(err)
	}
	return nil
}

// finishPublication selects the new protocol only for a capable audit owner or
// an already typed journal. Legacy audit implementations retain their old path.
func (q *Queue) finishPublication(ctx context.Context, j Job) (bool, error) {
	s, ok := q.store.(publicationStore)
	if !ok {
		return false, nil
	}
	op, err := s.LoadOperation(ctx, j.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, q.latchPublication(err)
	}
	_, capable := q.audit.(stableAuditor)
	if !capable && op.Identity.PublicationAudit == nil {
		return false, nil
	}
	if op.Checkpoint == "done" {
		return true, q.ackPublication(ctx, j, op)
	}
	intent, err := id.New()
	if err != nil {
		return true, err
	}
	final, err := id.New()
	if err != nil {
		return true, err
	}
	p, err := s.PreparePublicationAudit(ctx, j.ID, intent, final)
	if err != nil {
		return true, operationError(err)
	}
	if err = q.publicationRecord(j, p.IntentEventID, "intent", "ok", ""); err != nil {
		return true, err
	}
	_, err = q.operationTerminal(ctx, j)
	if err != nil {
		// A commit error does not prove rollback. Re-read the exact operation on a
		// new SQL transaction before selecting any failure record or retry.
		committed, readErr := s.LoadOperation(context.WithoutCancel(ctx), j.ID)
		if readErr != nil {
			return true, q.latchPublication(errors.Join(err, readErr))
		}
		if committed.Checkpoint != "done" {
			code := "agent_error"
			if errors.Is(err, store.ErrOperationExpired) {
				code = "interrupted"
			}
			if errors.Is(err, store.ErrSiteConflict) {
				code = "conflict"
			}
			eid, e := id.New()
			if e != nil {
				return true, q.latchPublication(e)
			}
			fs, ok := q.store.(interface {
				PreparePublicationFailureAudit(context.Context, string, string, string) (store.PublicationAudit, error)
			})
			if !ok {
				return true, q.latchPublication(ErrRecoveryBlocked)
			}
			failure, e := fs.PreparePublicationFailureAudit(context.WithoutCancel(ctx), j.ID, eid, code)
			if e != nil {
				return true, q.latchPublication(e)
			}
			if e = q.publicationRecord(j, failure.FailureEventID, "not_committed", "error", failure.FailureCode); e != nil {
				return true, e
			}
			if code == "agent_error" {
				return true, q.latchPublication(err)
			}
			return true, operationError(err)
		}
		// Confirmed commit is recoverable, but an uncertain storage response must
		// keep admission and serving restoration closed until a clean reopen.
		return true, q.latchPublication(err)
	}
	op, err = s.LoadOperation(ctx, j.ID)
	if err != nil {
		return true, q.latchPublication(err)
	}
	return true, q.ackPublication(ctx, j, op)
}
func (q *Queue) ackPublication(ctx context.Context, j Job, op store.Operation) error {
	p := op.Identity.PublicationAudit
	if p == nil || op.Checkpoint != "done" || len(op.Nonce) != 0 || len(op.Ciphertext) != 0 {
		return q.latchPublication(store.ErrPublicationPending)
	}
	if p.State != "acknowledged" {
		if err := q.publicationRecord(j, p.FinalEventID, "committed", "ok", ""); err != nil {
			return err
		}
		if err := q.store.(publicationStore).AcknowledgePublicationAudit(ctx, j.ID); err != nil {
			return q.latchPublication(err)
		}
	}
	return q.reconcileConfigRecord(ctx, j.ID)
}
func (q *Queue) recoverOperationPublications(ctx context.Context) error {
	s, ok := q.store.(publicationStore)
	if !ok {
		return nil
	}
	for {
		pending, err := s.PendingPublicationAudits(ctx)
		if err != nil {
			return q.latchPublication(err)
		}
		if len(pending) == 0 {
			return nil
		}
		for _, j := range pending {
			unlock, err := q.lockJob(ctx, j)
			if err != nil {
				return err
			}
			op, e := s.LoadOperation(ctx, j.ID)
			if e == nil {
				e = q.ackPublication(ctx, j, op)
			}
			unlock()
			if e != nil {
				return e
			}
		}
	}
}
