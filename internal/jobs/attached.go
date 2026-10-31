package jobs

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"slices"
	"strings"
)

// lockAttachedDatabases discovers under the site lock, then reserves the entire
// sorted set once and revalidates. Membership changes fail rather than add locks.
func (q *Queue) lockAttachedDatabases(ctx context.Context, siteID string) ([]store.Database, func(), error) {
	s, ok := q.store.(interface {
		AttachedDatabases(context.Context, string) ([]store.Database, error)
	})
	if !ok {
		return nil, nil, errors.New("jobs: attached database discovery unavailable")
	}
	release := q.locks.lock("site:" + siteID)
	before, err := s.AttachedDatabases(ctx, siteID)
	release()
	if err != nil {
		return nil, nil, err
	}
	keys := []string{"site:" + siteID}
	for _, db := range before {
		keys = append(keys, "db:"+db.ID)
	}
	release = q.locks.lockSet(keys)
	after, err := s.AttachedDatabases(ctx, siteID)
	if err == nil && !slices.Equal(before, after) {
		err = store.ErrSiteConflict
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		release()
		return nil, nil, err
	}
	return after, release, nil
}

func (q *Queue) lockJob(ctx context.Context, j Job) (func(), error) {
	if s, ok := q.store.(operationStore); ok {
		op, err := s.LoadOperation(ctx, j.ID)
		if err == nil {
			return q.locks.lockSet(op.Keys()), nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	if j.Verb == "site.delete" && strings.HasPrefix(j.ObjectKey, "site:") {
		dbs, release, err := q.lockAttachedDatabases(ctx, strings.TrimPrefix(j.ObjectKey, "site:"))
		if err != nil {
			return nil, err
		}
		// Attached deletion needs its own durable typed journal before runtime effects.
		if len(dbs) > 0 {
			release()
			return nil, rpcerr.Errf("unsupported", "Attached site deletion requires a durable journal")
		}
		return release, nil
	}
	keys, err := q.operationKeys(ctx, j)
	if err != nil {
		return nil, err
	}
	return q.locks.lockSet(keys), nil
}
