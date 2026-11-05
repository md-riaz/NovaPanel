package jobs

import "context"

// LockReconcileDatabase shares the worker's parent and namespace locks.
func (q *Queue) LockReconcileDatabase(ctx context.Context, dbID, kind, name string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Reconcile discovers these keys after the worker starts the job. Never
	// wait on them in the single worker: yield the whole attempt instead.
	if _, worker := ctx.Value(heldLocksKey{}).(map[string]bool); worker {
		parent, ok := q.locks.tryLock("db:" + dbID)
		if !ok {
			return nil, ErrRetry
		}
		key := "dbname:" + name
		if kind == "user" {
			key = "dbuser:" + name
		}
		child, ok := q.locks.tryLock(key)
		if !ok {
			parent()
			return nil, ErrRetry
		}
		return func() { child(); parent() }, nil
	}
	parent, err := q.locks.lock(ctx, "db:"+dbID)
	if err != nil {
		return nil, err
	}
	var child func()
	if kind == "user" {
		child, err = q.LockDatabaseUser(ctx, name)
	} else {
		child, err = q.LockDatabaseName(ctx, name)
	}
	if err != nil {
		parent()
		return nil, err
	}
	return func() { child(); parent() }, nil
}
