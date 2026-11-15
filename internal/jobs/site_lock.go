package jobs

import (
	"context"
	"errors"
	"novapanel/internal/id"
	"novapanel/internal/validate"
)

// LockSiteOSUser serializes site OS-user operations with the worker and
// reconcile. Call before inspecting mutable site/account state and hold until
// the operation finishes. The order is always site ID, then OS username.
// Worker callers yield instead of waiting, so a single worker cannot starve.
func (q *Queue) LockSiteOSUser(ctx context.Context, siteID, username string) (func(), error) {
	if !id.Valid(siteID) {
		return nil, errors.New("jobs: invalid site ID")
	}
	return q.lockSiteOSUser(ctx, siteID, username)
}

// LockSiteCreateOSUser locks the create name, then its identical OS username.
// It reuses the worker's site:<name> lock when called during site.create.
func (q *Queue) LockSiteCreateOSUser(ctx context.Context, name string) (func(), error) {
	if err := (&validate.V{}).SiteName(name); err != nil {
		return nil, err
	}
	return q.lockSiteOSUser(ctx, name, name)
}

func (q *Queue) lockSiteOSUser(ctx context.Context, siteKey, username string) (func(), error) {
	if err := (&validate.V{}).UnixUser(username); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	held, worker := ctx.Value(heldLocksKey{}).(map[string]bool)
	var releases []func()
	for _, key := range []string{"site:" + siteKey, "osuser:" + username} {
		if held[key] {
			continue
		}
		var release func()
		var err error
		if worker {
			var ok bool
			release, ok = q.locks.tryLock(key)
			if !ok {
				err = ErrRetry
			}
		} else {
			release, err = q.locks.lock(ctx, key)
		}
		if err == nil {
			err = ctx.Err()
			if err != nil {
				release()
			}
		}
		if err != nil {
			for i := len(releases) - 1; i >= 0; i-- {
				releases[i]()
			}
			return nil, err
		}
		releases = append(releases, release)
	}
	return func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}, nil
}
