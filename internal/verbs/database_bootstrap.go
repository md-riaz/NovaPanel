package verbs

import (
	"context"
	"errors"
	"novapanel/internal/database"
	"novapanel/internal/exec"
	"novapanel/internal/rpc"
)

func databaseReady(ctx context.Context, runner exec.Runner) error {
	err := database.Bootstrap(ctx, runner)
	if errors.Is(err, database.ErrUnavailable) {
		return rpc.Errf("precondition_failed", "MariaDB unavailable or control schema could not be queried")
	}
	if errors.Is(err, database.ErrUnsafe) {
		return rpc.Errf("agent_error", "MariaDB control schema failed security verification; operator reconciliation required")
	}
	return err
}
