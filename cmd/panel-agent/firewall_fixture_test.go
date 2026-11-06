package main

import (
	"context"
	"novapanel/internal/agent"
)

// Existing lifecycle tests have no firewall transaction. The private seam keeps
// them independent of the production fixed layout without a runtime bypass.
func serveScratch(ctx context.Context, a *agent.Agent) error {
	return serveInitialized(ctx, a, func() error { return nil })
}
