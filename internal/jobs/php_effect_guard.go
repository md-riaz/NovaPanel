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
	if op.Kind != store.PHPSet || op.SiteID != siteID || op.Identity.PHP == nil {
		return refuse
	}
	return s.CheckOperation(call, jobID)
}
