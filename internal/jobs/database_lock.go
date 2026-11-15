package jobs

import "context"

// SetPostRecovery installs a trusted startup callback, before Run is called.
// It runs after F6 recovery; the callback must leave live jobs to their replay.
func (q *Queue) SetPostRecovery(fn func(context.Context) error) {
	q.postRecovery = fn
}

// LockDatabase acquires the same parent lock used by queued database jobs.
// Acquire this before LockDatabaseName or LockDatabaseUser.
func (q *Queue) LockDatabase(ctx context.Context, databaseID string) (func(), error) {
	return q.locks.lock(ctx, "db:"+databaseID)
}
