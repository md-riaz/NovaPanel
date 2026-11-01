package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// domainExpansion binds every added domain to its selected follow-up in the same
// transaction, including when domains share a queued job that later fails.
// Pending ACME jobs read the complete desired set under the same site lock.
func domainExpansion(ctx context.Context, tx *sql.Tx, d Domain, parentID string, now int64) (*Job, error) {
	parent, err := scanJob(tx.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE id=?", parentID))
	if err != nil {
		return nil, err
	}
	if parent.Verb != "domain.add" || parent.ObjectKey != "site:"+d.SiteID || parent.CreatedBy == "" {
		return nil, ErrSitePrecondition
	}
	var eligible, staging bool
	err = tx.QueryRowContext(ctx, `SELECT s.status='active' AND s.cert_source='letsencrypt' AND s.force_https=1 AND ?='none', p.cert_staging FROM sites s JOIN domains p ON p.site_id=s.id AND p.is_primary=1 WHERE s.id=?`, d.CertState, d.SiteID).Scan(&eligible, &staging)
	if err != nil {
		return nil, err
	}
	if !eligible {
		return nil, nil
	}
	bindingKey := "acme:domain-expansion:" + d.ID
	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM settings WHERE key=? UNION ALL SELECT 1 FROM jobs WHERE id=?)`, bindingKey, d.ID).Scan(&exists)
	if err != nil || exists {
		return nil, err
	}
	bind := func(childID string) error {
		raw, e := json.Marshal(childID)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO settings(key,value_json) VALUES(?,?)", bindingKey, string(raw))
		return e
	}
	var sharedID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM jobs WHERE object_key=? AND verb IN ('ssl.issue','ssl.renew') AND state='queued' AND json_extract(args_json,'$.staging')=? ORDER BY created,id LIMIT 1`, parent.ObjectKey, staging).Scan(&sharedID)
	if err == nil {
		return nil, bind(sharedID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	args, err := json.Marshal(struct {
		SiteID  string `json:"site_id"`
		Staging bool   `json:"staging"`
	}{d.SiteID, staging})
	if err != nil {
		return nil, err
	}
	// Inherit persisted authorized sites:write authority, not the worker's caller or
	// arguments. ssl.issue has its own effectful class, unlike domain.add.
	j := Job{ID: d.ID, Verb: "ssl.issue", Args: args, ObjectKey: parent.ObjectKey, Actor: parent.Actor, Role: parent.Role, CreatedBy: parent.CreatedBy, Scopes: parent.Scopes, Automation: parent.Automation, Effectful: true, State: "queued", Created: now, Updated: now}
	if j.ID == parent.ID {
		return nil, errors.New("domain expansion binding conflict")
	}
	if err = insertJob(ctx, tx, j); err != nil {
		return nil, err
	}
	if err = bind(j.ID); err != nil {
		return nil, err
	}
	return &j, nil
}
