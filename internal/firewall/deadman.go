package firewall

import (
	"context"
	"path/filepath"
	"strconv"

	"novapanel/internal/exec"
	"novapanel/internal/id"
)

// Arm schedules the independent root recovery command before a firewall mutation.
func Arm(ctx context.Context, runner exec.Runner, pendingID, snapshotDir string, windowMin int) (string, error) {
	if !id.Valid(pendingID) || !filepath.IsAbs(snapshotDir) || filepath.Clean(snapshotDir) != snapshotDir || windowMin <= 0 || runner == nil {
		return "", invalid("deadman")
	}
	unit := "novapanel-fwrevert-" + pendingID
	_, err := firewallCommand(ctx, runner, "/usr/bin/systemd-run", "--unit="+unit, "--on-active="+strconv.Itoa(windowMin)+"min", "--timer-property=AccuracySec=1s", "/usr/sbin/novapanel-agent", "fw-revert", snapshotDir)
	if err != nil {
		return "", err
	}
	return unit + ".timer", nil
}

func Cancel(ctx context.Context, runner exec.Runner, pendingID string) error {
	if !id.Valid(pendingID) || runner == nil {
		return invalid("deadman")
	}
	_, err := firewallCommand(ctx, runner, "/usr/bin/systemctl", "stop", "novapanel-fwrevert-"+pendingID+".timer")
	return err
}
