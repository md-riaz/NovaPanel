package jobs

import (
	"context"
	"errors"
	"novapanel/internal/store"
)

// AddDomain publishes automatic follow-up without taking the parent's site lock
// again. SQL commits the domain and child before audit/wakeup or external apply.
func (q *Queue) AddDomain(ctx context.Context, d store.Domain, parentID string) (store.Domain, error) {
	if err := q.healthy(); err != nil {
		return store.Domain{}, err
	}
	s, ok := q.store.(interface {
		AddDomainWithExpansion(context.Context, store.Domain, string, int64) (store.Domain, *store.Job, error)
	})
	if !ok {
		return store.Domain{}, errors.New("jobs: coordinated domain persistence unavailable")
	}
	out, child, err := s.AddDomainWithExpansion(ctx, d, parentID, q.clock.Now())
	if err != nil {
		return out, err
	}
	if child != nil {
		if err = q.record(*child, "ok", "", "enqueue"); err != nil {
			return out, err
		}
		select {
		case q.wake <- struct{}{}:
		default:
		}
	}
	return out, nil
}
