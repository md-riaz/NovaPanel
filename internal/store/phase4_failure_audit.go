package store

import (
	"context"
	"database/sql"
	"novapanel/internal/id"
)

// PreparePHPFailureAudit durably binds a restored-PHP terminal event before append.
func (s *Store) PreparePHPFailureAudit(ctx context.Context, jid, eventID string) (out string, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE phase4_operations SET updated=updated WHERE job_id=?", jid); e != nil {
			return e
		}
		op, e := loadOperation(ctx, tx, jid)
		if e != nil {
			return e
		}
		if op.Kind != PHPSet || op.Identity.PHP == nil || op.Checkpoint == "done" || op.Checkpoint == "expired" {
			return ErrSitePrecondition
		}
		if op.Identity.PHP.FailureEventID == "" {
			op.Identity.PHP.FailureEventID = eventID
		}
		if !validPHPSetIdentity(op) {
			return ErrSiteInvalid
		}
		out = op.Identity.PHP.FailureEventID
		return savePublication(ctx, tx, op)
	})
	return
}

// PreparePublicationFailureAudit binds a rollback observation and its payload.
func (s *Store) PreparePublicationFailureAudit(ctx context.Context, jid, eventID, code string) (out PublicationAudit, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE phase4_operations SET updated=updated WHERE job_id=?", jid); e != nil {
			return e
		}
		op, e := loadOperation(ctx, tx, jid)
		if e != nil {
			return e
		}
		p := op.Identity.PublicationAudit
		if p == nil || op.Checkpoint == "done" {
			return ErrSitePrecondition
		}
		if p.FailureEventID == "" {
			p.FailureEventID, p.FailureCode = eventID, code
		}
		if !id.Valid(p.FailureEventID) || !validPublication(op) || op.Kind == PHPSet && !validPHPSetIdentity(op) {
			return ErrSiteInvalid
		}
		out = *p
		return savePublication(ctx, tx, op)
	})
	return
}
