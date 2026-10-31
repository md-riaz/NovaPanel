package jobs

import (
	"context"
	"errors"

	runner "novapanel/internal/exec"
	"novapanel/internal/platform"
	"novapanel/internal/store"
)

// WordPressSiteRunner resolves only the original sealed reservation under the
// queue worker's complete operation lock set. This is a credential adapter, not
// permission to run a WordPress plan: custody, ini, offline transport and physical
// quiescence must be supplied by the later runtime consumer. No default is wired.
func (q *Queue) WordPressSiteRunner(ctx context.Context, jobID string, boundary runner.Runner) (runner.Runner, error) {
	refuse := errors.New("jobs: WordPress site runner refused")
	lease, _ := ctx.Value(configWorkerKey{}).(*configWorkerLease)
	if lease == nil || lease.queue != q || lease.job != jobID || !lease.active.Load() || boundary == nil || q.healthy() != nil {
		return nil, refuse
	}
	ctx, done, admitted := lease.admit(ctx)
	if !admitted {
		return nil, refuse
	}
	defer done()
	s, ok := q.store.(operationStore)
	if !ok {
		return nil, refuse
	}
	if _, err := q.OpenWordPressCredentials(ctx, jobID); err != nil {
		return nil, refuse
	}
	op, err := s.LoadOperation(ctx, jobID)
	if err != nil || op.Kind != store.WordPressInstall || op.Identity.WordPress == nil {
		return nil, refuse
	}
	in := op.Identity.WordPress.Input
	r, err := platform.NewSiteRunner(ctx, boundary, in.SitesRoot, in.SiteName, in.UID, in.GID)
	if err != nil {
		return nil, err
	}
	return &wordPressSiteRunner{q: q, lease: lease, runner: r}, nil
}

type wordPressSiteRunner struct {
	q      *Queue
	lease  *configWorkerLease
	runner runner.Runner
}

func (r *wordPressSiteRunner) Run(ctx context.Context, p string, a []string, o runner.RunOpts) ([]byte, []byte, int, error) {
	// Use the original execution context, not an arbitrary fresh caller context.
	lease, _ := ctx.Value(configWorkerKey{}).(*configWorkerLease)
	if lease != r.lease || !r.lease.active.Load() || r.q.healthy() != nil {
		return nil, nil, -1, errors.New("jobs: WordPress site runner refused")
	}
	ctx, done, admitted := r.lease.admit(ctx)
	if !admitted {
		return nil, nil, -1, errors.New("jobs: WordPress site runner refused")
	}
	defer done()
	if _, err := r.q.OpenWordPressCredentials(ctx, r.lease.job); err != nil {
		return nil, nil, -1, errors.New("jobs: WordPress site runner refused")
	}
	return r.runner.Run(ctx, p, a, o)
}
