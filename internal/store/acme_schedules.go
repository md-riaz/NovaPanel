package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// PrepareACMESchedules seeds agent-owned authority, never authority from RPC args.
// Existing cadence survives restart. Expansion uses issue because the old SAN does
// not cover the newly added name; execution reads the complete current domain set.
func (s *Store) PrepareACMESchedules(ctx context.Context, now int64) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT s.id,d.cert_staging, EXISTS(SELECT 1 FROM domains a WHERE a.site_id=s.id AND a.cert_state='none') AND s.force_https FROM sites s JOIN domains d ON d.site_id=s.id AND d.is_primary=1 WHERE s.status='active' AND s.cert_source='letsencrypt' AND s.ssl_auto=1 AND (d.cert_not_after<=? OR (s.force_https=1 AND EXISTS(SELECT 1 FROM domains a WHERE a.site_id=s.id AND a.cert_state='none')))`, now+30*24*60*60*1000)
		if err != nil {
			return err
		}
		type candidate struct {
			id              string
			staging, expand bool
		}
		pending := []candidate{}
		for rows.Next() {
			var c candidate
			if err = rows.Scan(&c.id, &c.staging, &c.expand); err != nil {
				rows.Close()
				return err
			}
			pending = append(pending, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, c := range pending {
			verb := "ssl.renew"
			if c.expand {
				verb = "ssl.issue"
			}
			args, _ := json.Marshal(struct {
				SiteID  string `json:"site_id"`
				Staging bool   `json:"staging"`
			}{c.id, c.staging})
			_, err = tx.ExecContext(ctx, `INSERT INTO schedules(id,verb,args_json,cron,state_json,next_run,enabled,actor,role,owner_id,scopes_json,automation) VALUES(?,?,?,'daily','{}',?,1,'scheduler@local','owner','local-root','[]',0) ON CONFLICT(id) DO UPDATE SET next_run=CASE WHEN excluded.verb='ssl.issue' AND schedules.verb!='ssl.issue' THEN excluded.next_run ELSE schedules.next_run END,verb=excluded.verb,args_json=excluded.args_json`, "acme:auto:"+c.id, verb, string(args), now)
			if err != nil {
				return err
			}
		}
		return nil
	})
}
