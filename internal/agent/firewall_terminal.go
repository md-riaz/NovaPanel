package agent

import (
	"context"
	"errors"
	"novapanel/internal/firewall"
	"time"
)

// CommitFirewallTerminal consumes the exact SQL projection through the ordinary
// audit log, without acknowledging the journal or granting startup admission.
// The caller must not race Agent cleanup with this operation.
func (a *Agent) CommitFirewallTerminal(ctx context.Context) (err error) {
	if a == nil || a.Store == nil || a.Audit == nil {
		return errors.New("firewall terminal state unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	open := a.firewallTerminalJournal
	if open == nil {
		open = firewall.OpenInstalledFirewallJournal
	}
	j, err := open(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, j.Close()) }()
	return a.Audit.CommitFirewallTerminal(ctx, j)
}
