package firewall

import (
	"context"
	"errors"
	"fmt"
	execpkg "novapanel/internal/exec"
	"os"
	"time"
)

var ErrFirewallRecoveryPending = errors.New("firewall recovery pending; terminal reconciliation and reuse unavailable")

// PreflightInstalledFirewall must run before daemon state or worker initialization.
// It never opens SQLite or acknowledges, removes or repairs journal evidence.
func PreflightInstalledFirewall(ctx context.Context, runner execpkg.Runner) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return fmt.Errorf("firewall startup: %w", errRuntimeRecovery)
	}
	return startupFirewall(ctx, runtimeLayout{root: productionFirewallRoot, rules: "/etc/ufw"}, runtimeManager{runner: runner, now: time.Now, monotonic: managerMonotonic})
}
func startupFirewall(ctx context.Context, l runtimeLayout, m runtimeManager) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	defer func() {
		if err != nil {
			err = fmt.Errorf("firewall startup: %w", err)
		}
	}()
	if err = l.admit(); err != nil {
		return err
	}
	j, err := OpenJournalContext(ctx, l.root+"/journal")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, j.Close()) }()
	r, present, err := j.Inspect(ctx)
	if err != nil || !present {
		return err
	}
	switch r.State {
	case "confirmed", "reverted", "acknowledged":
		return ErrFirewallRecoveryPending
	}
	managerErr := m.ensure(ctx, r)
	after, present, inspectErr := j.Inspect(ctx)
	if inspectErr != nil {
		return errors.Join(ErrFirewallRecoveryPending, managerErr, inspectErr)
	}
	if !present || after != r {
		return errors.Join(ErrFirewallRecoveryPending, managerErr, errors.New("journal changed during manager verification"))
	}
	return errors.Join(ErrFirewallRecoveryPending, managerErr)
}
