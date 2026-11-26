package verbs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	runner "novapanel/internal/exec"
	"novapanel/internal/platform"

	"golang.org/x/sys/unix"
	"novapanel/internal/agent"
	"novapanel/internal/render/sshd"
	"novapanel/internal/rpc"
)

// applySSHD is the shared serialized entry point for SSH config mutations.
func applySSHD(ctx context.Context, a *agent.Agent, d sshd.Desired, teardown bool) error {
	if a.Jobs == nil || a.SSHD == nil {
		return rpc.Errf("agent_error", "SSH configuration unavailable")
	}
	unlock, err := a.Jobs.LockSSHD(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	root, err := os.OpenRoot(filepath.Dir(a.Cfg.DBPath))
	if err != nil {
		return err
	}
	defer root.Close()
	const marker = "sshd-diverged"
	_, err = root.Lstat(marker)
	if err == nil {
		if err = a.SSHD.Heal(ctx); err != nil {
			return rpc.Errf("precondition_failed", "SSH configuration diverged; repair it before retrying")
		}
		if err = root.Remove(marker); err != nil {
			return err
		}
		if err = syncSSHState(root); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if teardown {
		_, statErr := os.Stat("/run/systemd/system")
		if os.IsNotExist(statErr) {
			d.Offline = true
		} else if statErr != nil {
			return statErr
		} else {
			if a.Exec == nil {
				return rpc.Errf("agent_error", "SSH execution unavailable")
			}
			out, stderr, code, e := a.Exec.Run(ctx, platform.Systemctl, []string{"show", "--property=LoadState", "--value", "ssh.service"}, runner.RunOpts{})
			if strings.Contains(string(stderr), "System has not been booted with systemd as init system") || strings.TrimSpace(string(out)) == "not-found" {
				d.Offline = true
			} else if e != nil || code != 0 {
				return rpc.Errf("agent_error", "Cannot inspect SSH service")
			}
		}
	}
	err = a.SSHD.Apply(ctx, d)
	if !errors.Is(err, sshd.Diverged) {
		return err
	}
	f, markerErr := root.OpenFile(marker, os.O_WRONLY|os.O_CREATE|unix.O_NOFOLLOW, 0600)
	if markerErr == nil {
		markerErr = f.Chmod(0600)
		if markerErr == nil {
			markerErr = f.Sync()
		}
		markerErr = errors.Join(markerErr, f.Close())
		if markerErr == nil {
			markerErr = syncSSHState(root)
		}
	}
	return errors.Join(rpc.Errf("precondition_failed", "SSH configuration diverged; repair it before retrying"), err, markerErr)
}
func syncSSHState(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
