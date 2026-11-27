package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const tlsIntentKey = "tls:publication:v1"

// TLSIntent is agent-owned nonsecret intent. One intent gates all nginx changes.
func (s *Store) TLSIntent(ctx context.Context) (json.RawMessage, error) {
	var b string
	err := s.db.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key=?", tlsIntentKey).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return json.RawMessage(b), err
}

// FinalizerTLSBlocked permits only the quarantined owner's maintenance restore.
func (s *Store) FinalizerTLSBlocked(ctx context.Context, jobID string) (bool, error) {
	var blocked bool
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		blocked, err = finalizerTLSBlocked(ctx, tx, jobID)
		return err
	})
	return blocked, err
}

func finalizerTLSBlocked(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, jobID string) (bool, error) {
	var raw string
	err := q.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key=?", tlsIntentKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	var identity string
	err = q.QueryRowContext(ctx, `SELECT c.identities_json FROM phase4_operations o JOIN jobs j ON j.id=o.job_id JOIN phase4_operation_children c ON c.job_id=o.job_id AND c.child_kind='domain_tls' WHERE o.job_id=? AND o.kind='site_update' AND o.reconciled=0 AND o.checkpoint NOT IN ('done','expired') AND j.code='agent_error'`, jobID).Scan(&identity)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	var child OperationChildIdentity
	if json.Unmarshal([]byte(identity), &child) != nil || len(child.TLS) == 0 || bytes.Equal([]byte(raw), child.TLS) {
		return true, nil
	}
	return false, nil
}
func (s *Store) BeginTLSIntent(ctx context.Context, b json.RawMessage) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO settings(key,value_json) VALUES(?,?)", tlsIntentKey, string(b))
	return err
}

// BeginCompositeTLSIntent binds the publication intent to its durable child in the same transaction.
func (s *Store) BeginCompositeTLSIntent(ctx context.Context, jobID string, b json.RawMessage) error {
	if !json.Valid(b) {
		return ErrSiteInvalid
	}
	identity, err := json.Marshal(OperationChildIdentity{TLS: b})
	if err != nil {
		return err
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `UPDATE phase4_operation_children SET identities_json=?,updated=? WHERE job_id=? AND child_kind='domain_tls' AND checkpoint='prepared' AND status='pending' AND EXISTS(SELECT 1 FROM phase4_operations o JOIN jobs j ON j.id=o.job_id WHERE o.job_id=? AND o.kind='site_update' AND o.checkpoint='prepared' AND j.state='running')`, string(identity), s.clock.Now(), jobID, jobID)
		if err = affected(r, err); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value_json) VALUES(?,?)", tlsIntentKey, string(b))
		return err
	})
}

// CheckCompositeTLSIntent records a binding mismatch against its durable owner.
func (s *Store) CheckCompositeTLSIntent(ctx context.Context, raw json.RawMessage) (matched, changed bool, err error) {
	var binding struct {
		JobID string `json:"job_id"`
	}
	if json.NewDecoder(bytes.NewReader(raw)).Decode(&binding) != nil {
		return false, false, nil
	}
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		var current string
		if e := tx.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, tlsIntentKey).Scan(&current); e != nil {
			return e
		}
		if current != string(raw) {
			return ErrSiteConflict
		}
		rows, e := tx.QueryContext(ctx, `SELECT o.job_id,c.identities_json FROM phase4_operations o JOIN jobs j ON j.id=o.job_id JOIN phase4_operation_children c ON c.job_id=o.job_id AND c.child_kind='domain_tls' WHERE o.kind='site_update' AND o.checkpoint NOT IN ('done','expired') AND (j.state IN ('queued','running') OR (j.state='failed' AND j.code='interrupted'))`)
		if e != nil {
			return e
		}
		var snapshot string
		for rows.Next() {
			var jid, identity string
			if e = rows.Scan(&jid, &identity); e != nil {
				rows.Close()
				return e
			}
			if jid == binding.JobID {
				matched, snapshot = true, identity
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil || !matched {
			return e
		}
		var child OperationChildIdentity
		if json.Unmarshal([]byte(snapshot), &child) != nil || !bytes.Equal(child.TLS, raw) {
			changed = true
			_, e = tx.ExecContext(ctx, `UPDATE jobs SET state=CASE WHEN state='failed' THEN 'queued' ELSE state END,finished=NULL,code='agent_error',error='TLS recovery binding changed',updated=? WHERE id=? AND (state IN ('queued','running') OR (state='failed' AND code='interrupted'))`, s.clock.Now(), binding.JobID)
		}
		return e
	})
	return
}

func (s *Store) ClearTLSIntent(ctx context.Context, b json.RawMessage) error {
	r, err := s.db.ExecContext(ctx, "DELETE FROM settings WHERE key=? AND value_json=?", tlsIntentKey, string(b))
	return affected(r, err)
}

// CommitManualTLS commits metadata and the replay marker together. It cannot make
// the preceding filesystem changes atomic with SQLite; the retained intent owns recovery.
func (s *Store) CommitManualTLS(ctx context.Context, siteID, jobID string, expiry int64, b json.RawMessage) error {
	return s.CommitTLS(ctx, siteID, jobID, "ssl.upload", false, expiry, b)
}

func (s *Store) CommitTLS(ctx context.Context, siteID, jobID, verb string, staging bool, expiry int64, b json.RawMessage) error {
	source := "manual"
	if verb == "ssl.issue" || verb == "ssl.renew" {
		source = "letsencrypt"
	} else if verb != "ssl.upload" || staging {
		return ErrACMEPolicy
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, "UPDATE sites SET cert_source=?,updated=? WHERE id=? AND status='active'", source, s.clock.Now(), siteID)
		if err = affected(r, err); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE domains SET cert_state='issued',cert_staging=?,cert_not_after=? WHERE site_id=?", staging, expiry, siteID); err != nil {
			return err
		}
		result, err := json.Marshal(struct {
			NotAfter int64 `json:"not_after"`
		}{expiry})
		if err != nil {
			return err
		}
		if verb == "ssl.upload" || verb == "ssl.issue" {
			rows, e := tx.QueryContext(ctx, "SELECT fqdn FROM domains WHERE site_id=? ORDER BY is_primary DESC, fqdn", siteID)
			if e != nil {
				return e
			}
			names := []string{}
			for rows.Next() {
				var name string
				if e = rows.Scan(&name); e != nil {
					rows.Close()
					return e
				}
				names = append(names, name)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			result, err = json.Marshal(struct {
				FQDNs    []string `json:"fqdns"`
				NotAfter int64    `json:"not_after"`
			}{names, expiry})
			if err != nil {
				return err
			}
		}
		r, err = tx.ExecContext(ctx, "UPDATE jobs SET step='tls_committed',result_json=? WHERE id=? AND verb=? AND object_key=? AND state='running'", string(result), jobID, verb, "site:"+siteID)
		if err = affected(r, err); err != nil {
			return err
		}
		r, err = tx.ExecContext(ctx, "DELETE FROM settings WHERE key=? AND value_json=?", tlsIntentKey, string(b))
		return affected(r, err)
	})
}
