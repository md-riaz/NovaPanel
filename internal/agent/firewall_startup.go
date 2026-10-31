package agent

import (
	"context"
	"errors"
	"novapanel/internal/firewall"
	"time"
)

// FirewallStartup is mandatory, including for zero-value Agents. Production
// paths and manager commands cannot be configured through daemon configuration.
func (a *Agent) FirewallStartup(ctx context.Context) error {
	if a.firewallStartup == nil {
		return errors.New("firewall startup dependency missing")
	}
	return a.firewallStartup(ctx)
}

func (a *Agent) PrepareFirewallStartup(ctx context.Context) (*firewall.FirewallStartupPlan, error) {
	if a.firewallStartupPlan == nil {
		return nil, errors.New("firewall startup dependency missing")
	}
	return a.firewallStartupPlan(ctx)
}
func (a *Agent) CompleteFirewallStartup(ctx context.Context, p *firewall.FirewallStartupPlan) error {
	if a.Audit == nil || a.Store == nil {
		return errors.New("firewall startup state missing")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return a.Audit.CompleteFirewallStartup(ctx, p)
}
