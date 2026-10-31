package jobs

import (
	"context"
	"errors"

	runner "novapanel/internal/exec"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
)

// StageWordPress extracts only into private agent custody. It deliberately cannot
// change the live docroot, advance a checkpoint, invoke PHP or finish a job.
// The original queue worker retains the complete persisted lock set throughout.
func (q *Queue) StageWordPress(ctx context.Context, jobID, root string, p wordpress.StagedPaths, r runner.Runner) error {
	refuse := errors.New("jobs: WordPress staging refused")
	lease, _ := ctx.Value(configWorkerKey{}).(*configWorkerLease)
	if lease == nil || lease.queue != q || lease.job != jobID || !lease.active.Load() || r == nil {
		return refuse
	}
	s, ok := q.store.(operationStore)
	if !ok {
		return refuse
	}
	if q.healthy() != nil || s.CheckOperation(ctx, jobID) != nil {
		return refuse
	}
	op, err := s.LoadOperation(ctx, jobID)
	if err != nil || op.Kind != store.WordPressInstall || (op.Checkpoint != "db_applied" && op.Checkpoint != "config_published") {
		return refuse
	}
	if _, err = q.OpenWordPressCredentials(ctx, jobID); err != nil {
		return refuse
	}
	w := op.Identity.WordPress
	plan, err := wordpress.BuildInstall(w.Input, w.Evidence)
	if err != nil {
		return refuse
	}
	return wordpress.ExtractStaged(ctx, p, r, root, wordpress.StageBinding{JobID: jobID, SiteID: op.SiteID, Docroot: plan.Docroot, UID: w.Input.UID, GID: w.Input.GID})
}
