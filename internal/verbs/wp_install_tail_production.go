//go:build !wordpress_offline_lab

package verbs

import (
	"context"
	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/store"
)

func wpInstallTail(_ context.Context, _ *store.Store, _ runner.Runner, _ string, _ *jobs.Job, _ store.Operation, _ jobs.WordPressCredentials) error {
	return jobs.ErrRetry
}
