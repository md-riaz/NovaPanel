package verbs

import (
	"context"
	"novapanel/internal/exec"
	"novapanel/internal/phpcapacity"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

func phpAvailable(ctx context.Context, runner exec.Runner) error {
	if runner == nil {
		return rpc.Errf("agent_error", "PHP runtime unavailable")
	}
	available, err := phpRuntimeAvailable(ctx, runner)
	if err != nil {
		return err
	}
	if !available {
		return rpc.Errf("precondition_failed", "PHP runtime is not installed")
	}
	return nil
}
func committedPHPCap(limits store.PHPLimits) (int, error) {
	if limits.MaxChildren == nil || *limits.MaxChildren < 2 || *limits.MaxChildren > 32 {
		return 0, rpc.Errf("precondition_failed", "PHP concurrency cap is missing or invalid")
	}
	return *limits.MaxChildren, nil
}

// activePHPLimits commits the one-time legacy cap before any pool recovery.
func activePHPLimits(ctx context.Context, st *store.Store, siteID string) (store.PHPLimits, error) {
	return st.BackfillSitePHPCap(ctx, siteID, func(active int) int {
		return phpcapacity.ChildrenCap(phpcapacity.TotalMemoryMB(), active)
	})
}
