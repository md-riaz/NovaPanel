package jobs

import "context"

// LockReconcileDatabase shares the worker's parent and namespace locks.
func (q *Queue) LockReconcileDatabase(ctx context.Context, dbID, kind, name string) (func(), error) {
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
