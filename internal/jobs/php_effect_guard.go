package jobs

import (
	"context"
	"errors"

	"novapanel/internal/store"
)

// CheckPHPEffect is an internal execution-lease guard, not a publication or
// recovery adapter. Invoke immediately before every PHP effect while the worker
// holds the original operation locks. It never authorizes a retained callback
// after the worker returns and re-reads the journal and expiry on each call.
func (q *Queue) CheckPHPEffect(ctx context.Context, jobID, siteID string) error {
	return q.checkSiteEffect(ctx, jobID, siteID, store.PHPSet)
}

func (q *Queue) CheckCompositeEffect(ctx context.Context, jobID, siteID string) error {
	return q.checkSiteEffect(ctx, jobID, siteID, store.SiteUpdate)
}

func (q *Queue) checkSiteEffect(ctx context.Context, jobID, siteID string, kind store.OperationKind) error {
	refuse := errors.New("jobs: PHP effect refused")
	lease, _ := ctx.Value(configWorkerKey{}).(*configWorkerLease)
	if lease == nil || lease.queue != q || lease.job != jobID || !lease.active.Load() || q.healthy() != nil {
		return refuse
	}
	call, done, admitted := lease.admit(ctx)
	if !admitted {
		return refuse
	}
	defer done()
	s, ok := q.store.(operationStore)
	if !ok {
		return refuse
	}
	op, err := s.LoadOperation(call, jobID)
	if err != nil {
		return err
	}
	if op.Kind != kind || op.SiteID != siteID || (kind == store.PHPSet && op.Identity.PHP == nil) {
		return refuse
	}
	if lease.restoreExpiredPHP {
		check, ok := q.store.(interface {
			CheckExpiredPHPRestoration(context.Context, string) error
		})
		if !ok {
			return refuse
		}
		return check.CheckExpiredPHPRestoration(call, jobID)
	}
	return s.CheckOperation(call, jobID)
}

// PHPRecoveryOnly identifies the original worker's restoration-only lease.
func (q *Queue) PHPRecoveryOnly(ctx context.Context, jobID string) bool {
	lease, _ := ctx.Value(configWorkerKey{}).(*configWorkerLease)
	return lease != nil && lease.queue == q && lease.job == jobID && lease.active.Load() && lease.restoreExpiredPHP
}
