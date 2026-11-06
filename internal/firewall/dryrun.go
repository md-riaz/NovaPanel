package firewall

import (
	"context"
	execpkg "novapanel/internal/exec"
	"novapanel/internal/rpcerr"
)

// DryRun validates the entire desired state before invoking the injected runner.
// It never enables, resets, reloads or otherwise applies the firewall.
func DryRun(ctx context.Context, runner execpkg.Runner, d Desired, sshPort int, sshSource string) error {
	plan, err := Render(d, sshPort, sshSource)
	if err != nil {
		return err
	}
	if runner == nil {
		return rpcerr.Errf("agent_error", "firewall runner missing")
	}
	for _, args := range plan {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, _, exit, err := runner.Run(ctx, "/usr/sbin/ufw", append([]string{"--dry-run"}, args...), execpkg.RunOpts{})
		if err != nil || exit != 0 {
			return rpcerr.Errf("agent_error", "firewall dry-run failed")
		}
	}
	return nil
}
