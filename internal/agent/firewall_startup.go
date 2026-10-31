package agent

import (
	"context"
	"errors"
)

// FirewallStartup is mandatory, including for zero-value Agents. Production
// paths and manager commands cannot be configured through daemon configuration.
func (a *Agent) FirewallStartup(ctx context.Context) error {
	if a.firewallStartup == nil {
		return errors.New("firewall startup dependency missing")
	}
	return a.firewallStartup(ctx)
}
