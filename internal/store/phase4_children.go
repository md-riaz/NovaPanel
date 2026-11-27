package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"

	"novapanel/internal/id"
	php "novapanel/internal/render/php"
)

// OperationChildIdentity contains only the durable, nonsecret PHP snapshot.
type OperationChildIdentity struct {
	PHP *PHPOperationIdentity `json:",omitempty"`
}

type OperationChild struct {
	JobID, Kind, Checkpoint, Status, TypedError, JournalRef string
	Ordinal                                                 int
	Identity                                                OperationChildIdentity
	BracketEngaged                                          bool
	Updated                                                 int64
}

func validChildIdentity(kind string, identity OperationChildIdentity) bool {
	if kind != "php_change" {
		return identity.PHP == nil
	}
	p := identity.PHP
	if p == nil || p.OriginalVersion != "8.3" || p.DesiredVersion != "8.3" || p.OriginalCap < 2 || p.OriginalCap > 32 || p.DesiredCap < 2 || p.DesiredCap > 32 || p.FailureEventID != "" {
		return false
	}
	if _, err := php.MergeLimits(&p.OriginalLimits, php.LimitsPatch{}); err != nil {
		return false
	}
	if _, err := php.MergeLimits(&p.DesiredLimits, php.LimitsPatch{}); err != nil {
		return false
	}
	return p.OriginalLimitsPresent || p.OriginalLimits == (php.Limits{MemoryMB: 256, UploadMB: 64, MaxExecSec: 60})
}

// PrepareOperationChildren stores the ordered vector and snapshots before effects.
func (s *Store) PrepareOperationChildren(ctx context.Context, jobID string, children []OperationChild) error {
	if len(children) == 0 {
		return ErrSiteInvalid
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM phase4_operations o JOIN jobs j ON j.id=o.job_id WHERE o.job_id=? AND o.kind='site_update' AND o.checkpoint='prepared' AND j.state IN ('queued','running')`, jobID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			return ErrSitePrecondition
		}
		for ordinal, c := range children {
			if c.Ordinal != ordinal || (c.Kind != "maintenance_on" && c.Kind != "php_change" && c.Kind != "domain_tls" && c.Kind != "maintenance_off") || !validChildIdentity(c.Kind, c.Identity) {
				return ErrSiteInvalid
			}
			raw, err := json.Marshal(c.Identity)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO phase4_operation_children(job_id,ordinal,child_kind,checkpoint,status,identities_json,updated) VALUES(?,?,?,'prepared','pending',?,?)`, jobID, ordinal, c.Kind, string(raw), s.clock.Now()); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) OperationChildren(ctx context.Context, jobID string) ([]OperationChild, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ordinal,child_kind,checkpoint,status,coalesce(typed_error,''),identities_json,bracket_engaged,coalesce(journal_ref,''),updated FROM phase4_operation_children WHERE job_id=? ORDER BY ordinal`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OperationChild
	for rows.Next() {
		c := OperationChild{JobID: jobID}
		var raw string
		if err = rows.Scan(&c.Ordinal, &c.Kind, &c.Checkpoint, &c.Status, &c.TypedError, &raw, &c.BracketEngaged, &c.JournalRef, &c.Updated); err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewBufferString(raw))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&c.Identity); err != nil {
			return nil, err
		}
		if decoder.Decode(new(any)) != io.EOF || !validChildIdentity(c.Kind, c.Identity) {
			return nil, ErrSiteInvalid
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CheckpointOperationChild advances a pending child after its effect is verified.
func (s *Store) CheckpointOperationChild(ctx context.Context, jobID string, ordinal int, from, to string) error {
	if (from != "prepared" || to != "config_published") && (from != "config_published" || to != "verified") {
		return ErrSiteInvalid
	}
	r, err := s.db.ExecContext(ctx, `UPDATE phase4_operation_children SET checkpoint=?,updated=? WHERE job_id=? AND ordinal=? AND child_kind='php_change' AND status='pending' AND checkpoint=?`, to, s.clock.Now(), jobID, ordinal, from)
	return affected(r, err)
}

// CommitOperationChild commits the child and its nonsecret journal event together.
func (s *Store) CommitOperationChild(ctx context.Context, jobID string, ordinal int, checkpoint, status, code string) error {
	if (status != "committed" && status != "failed") || checkpoint == "" || (status == "failed") != (code != "") {
		return ErrSiteInvalid
	}
	eventID, err := id.New()
	if err != nil {
		return err
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		now := s.clock.Now()
		r, err := tx.ExecContext(ctx, `UPDATE phase4_operation_children SET checkpoint=?,status=?,typed_error=?,journal_ref=?,updated=? WHERE job_id=? AND ordinal=? AND status='pending' AND (child_kind<>'php_change' OR ?='failed') AND EXISTS(SELECT 1 FROM jobs WHERE id=? AND state='running')`, checkpoint, status, nullableString(code), eventID, now, jobID, ordinal, status, jobID)
		if err = affected(r, err); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO job_events(id,job_id,ts,msg) VALUES(?,?,?,?)`, eventID, jobID, now, fmt.Sprintf("child %d %s", ordinal, status))
		return err
	})
}

// CommitPHPChild publishes PHP metadata and its child journal in one transaction.
func (s *Store) CommitPHPChild(ctx context.Context, jobID string, ordinal int) error {
	eventID, err := id.New()
	if err != nil {
		return err
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var siteID, raw string
		err := tx.QueryRowContext(ctx, `SELECT o.site_id,c.identities_json FROM phase4_operations o JOIN jobs j ON j.id=o.job_id JOIN phase4_operation_children c ON c.job_id=o.job_id WHERE o.job_id=? AND o.kind='site_update' AND j.state='running' AND c.ordinal=? AND c.child_kind='php_change' AND c.status='pending' AND c.checkpoint='verified'`, jobID, ordinal).Scan(&siteID, &raw)
		if err != nil {
			return err
		}
		var identity OperationChildIdentity
		decoder := json.NewDecoder(bytes.NewBufferString(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&identity) != nil || decoder.Decode(new(any)) != io.EOF || !validChildIdentity("php_change", identity) {
			return ErrSiteInvalid
		}
		site, limits, err := phpSnapshot(ctx, tx, siteID)
		if err != nil {
			return err
		}
		p := identity.PHP
		if *site.PHPVersion != p.OriginalVersion || (limits != nil) != p.OriginalLimitsPresent || (limits != nil && *limits != p.OriginalLimits) {
			return ErrSiteConflict
		}
		now := s.clock.Now()
		if _, err = tx.ExecContext(ctx, `UPDATE sites SET php_version=?,updated=? WHERE id=?`, p.DesiredVersion, now, siteID); err != nil {
			return err
		}
		if err = writePhase4PHPLimits(ctx, tx, siteID, p.DesiredLimits, p.DesiredCap); err != nil {
			return err
		}
		r, err := tx.ExecContext(ctx, `UPDATE phase4_operation_children SET status='committed',journal_ref=?,updated=? WHERE job_id=? AND ordinal=? AND status='pending' AND checkpoint='verified'`, eventID, now, jobID, ordinal)
		if err = affected(r, err); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO job_events(id,job_id,ts,msg) VALUES(?,?,?,?)`, eventID, jobID, now, fmt.Sprintf("child %d committed", ordinal))
		return err
	})
}

func (s *Store) EngageOperationBracket(ctx context.Context, jobID string) error {
	r, err := s.db.ExecContext(ctx, `UPDATE phase4_operation_children SET bracket_engaged=1,updated=? WHERE job_id=? AND child_kind='maintenance_on' AND checkpoint='prepared' AND status='pending' AND EXISTS(SELECT 1 FROM phase4_operation_children c WHERE c.job_id=? AND c.child_kind='maintenance_off' AND c.status='pending')`, s.clock.Now(), jobID, jobID)
	return affected(r, err)
}
