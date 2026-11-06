package store

import "context"

// PrepareReconcileSchedule seeds the F20 backstop and repairs a previously disabled row without resetting its cadence.
func (s *Store) PrepareReconcileSchedule(ctx context.Context, now int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO schedules(id,verb,args_json,cron,state_json,next_run,enabled,actor,role,owner_id,scopes_json,automation) VALUES('reconcile:auto','reconcile','{}','daily','{}',?,1,'scheduler@local','owner','local-root','[]',0) ON CONFLICT(id) DO UPDATE SET enabled=1 WHERE schedules.enabled=0`, now+24*60*60*1000)
	return err
}
