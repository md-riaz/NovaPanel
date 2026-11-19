//go:build !wordpress_offline_lab

package verbs

import (
	"context"
	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/store"
)

func wpInstallTail(_ context.Context, _ *agent.Agent, _ *jobs.Job, _ store.Operation, _ jobs.WordPressCredentials) error {
	return jobs.ErrRetry
}
