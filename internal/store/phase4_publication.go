package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/id"
)

// PublicationAudit binds both file-first records to the original operation.
// IDs are minted by the trusted queue before the first append.
type PublicationAudit struct {
	Version                     int
	IntentEventID, FinalEventID string
	State                       string
}

var ErrPublicationPending = errors.New("operation publication audit pending")

// A legacy done journal without typed acknowledgement is also in doubt. A
// historical pre-terminal success record alone cannot authorize delivery.
const publicationPendingSQL = `EXISTS(SELECT 1 FROM phase4_operations po WHERE po.job_id=jobs.id AND po.checkpoint='done' AND (json_type(po.identities_json,'$.PublicationAudit') IS NOT NULL AND coalesce(json_extract(po.identities_json,'$.PublicationAudit.State'),'')<>'acknowledged' OR json_type(po.identities_json,'$.PublicationAudit') IS NULL AND (jobs.state<>'done' OR jobs.step<>'phase4_committed' OR length(coalesce(po.ciphertext,x''))>0 OR length(coalesce(po.nonce,x''))>0)))`

type publicationScanner struct {
	scanner
	pending *bool
}

func (s publicationScanner) Scan(dest ...any) error {
	return s.scanner.Scan(append(dest, s.pending)...)
}

func publicationPending(ctx context.Context, tx interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, jid string) error {
	var pending bool
	if err := tx.QueryRowContext(ctx, "SELECT "+publicationPendingSQL+" FROM jobs WHERE id=?", jid).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return ErrPublicationPending
	}
	return nil
}

func validPublication(op Operation) bool {
	p := op.Identity.PublicationAudit
	return p == nil || p.Version == 1 && id.Valid(p.IntentEventID) && id.Valid(p.FinalEventID) && p.IntentEventID != p.FinalEventID && (p.State == "intent" && op.Checkpoint != "done" || (p.State == "committed" || p.State == "acknowledged") && op.Checkpoint == "done")
}

func (s *Store) PreparePublicationAudit(ctx context.Context, jid, intent, final string) (p PublicationAudit, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE phase4_operations SET updated=updated WHERE job_id=?", jid); e != nil {
			return e
		}
		op, e := loadOperation(ctx, tx, jid)
		if e != nil {
			return e
		}
		if op.Identity.PublicationAudit != nil {
			p = *op.Identity.PublicationAudit
			return nil
		}
		if op.Checkpoint == "done" {
			return ErrPublicationPending
		}
		if s.clock.Now() >= op.Expires {
			return ErrOperationExpired
		}
		if op.Checkpoint != "verified" && !((op.Kind == DatabaseDelete || op.Kind == DBUserDelete || op.Kind == SiteDelete) && op.Checkpoint == "db_applied") {
			return ErrSitePrecondition
		}
		if e = operationBlocked(ctx, tx, op); e != nil {
			return e
		}
		if op.Kind == PHPSet {
			e = checkPHPSet(ctx, tx, op)
		} else if op.Kind == SiteDelete {
			e = checkSiteDelete(ctx, tx, op)
		} else if op.Kind == WordPressInstall {
			e = checkWordPress(ctx, tx, op)
		} else if op.Kind == DatabaseCreate {
			e = checkOperationSite(ctx, tx, op)
		} else {
			e = checkOperationMembership(ctx, tx, op)
		}
		if e != nil {
			return e
		}
		p = PublicationAudit{Version: 1, IntentEventID: intent, FinalEventID: final, State: "intent"}
		op.Identity.PublicationAudit = &p
		if !validPublication(op) {
			return ErrSiteInvalid
		}
		return savePublication(ctx, tx, op)
	})
	return
}

func checkPublicationEvent(ctx context.Context, tx *sql.Tx, op Operation, eid, publication string) error {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM events e JOIN jobs j ON j.id=? WHERE e.id=? AND e.verb=j.verb AND e.object_key=j.object_key AND e.actor=j.actor AND e.role=coalesce(j.role,'') AND e.outcome='ok' AND coalesce(e.code,'')='' AND json_extract(e.detail_json,'$.job_id')=j.id AND json_extract(e.detail_json,'$.stage')='exec' AND json_extract(e.detail_json,'$.publication')=?`, op.JobID, eid, publication).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrSitePrecondition
	}
	return nil
}

func (s *Store) AcknowledgePublicationAudit(ctx context.Context, jid string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE phase4_operations SET updated=updated WHERE job_id=?", jid); e != nil {
			return e
		}
		op, e := loadOperation(ctx, tx, jid)
		if e != nil {
			return e
		}
		p := op.Identity.PublicationAudit
		if p == nil || op.Checkpoint != "done" || len(op.Ciphertext) != 0 || len(op.Nonce) != 0 {
			return ErrSitePrecondition
		}
		if e = checkPublicationEvent(ctx, tx, op, p.IntentEventID, "intent"); e != nil {
			return e
		}
		if e = checkPublicationEvent(ctx, tx, op, p.FinalEventID, "committed"); e != nil {
			return e
		}
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE id=? AND state='done' AND step='phase4_committed'`, jid).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			return ErrSitePrecondition
		}
		p.State = "acknowledged"
		return savePublication(ctx, tx, op)
	})
}

func savePublication(ctx context.Context, tx *sql.Tx, op Operation) error {
	raw, err := json.Marshal(op.Identity)
	if err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, "UPDATE phase4_operations SET identities_json=? WHERE job_id=?", string(raw), op.JobID)
	return affected(r, err)
}
