package store

import (
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
