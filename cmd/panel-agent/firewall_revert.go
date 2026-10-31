package main

import (
	"context"
	"errors"
	"novapanel/internal/id"
)

const recoverySnapshotPrefix = "/var/lib/novapanel/firewall/snapshots/"

// Dispatch before flags or daemon dependencies. The spelling only selects an ID.
func dispatchFirewallRevert(ctx context.Context, args []string, uid, gid int, recover func(context.Context, string) error) (bool, error) {
	if len(args) == 0 || args[0] != "fw-revert" {
		return false, nil
	}
	if uid != 0 || gid != 0 {
		return true, errors.New("fw-revert requires root UID and GID")
	}
	if len(args) != 2 {
		return true, errors.New("fw-revert requires exactly one snapshot directory")
	}
	path := args[1]
	if len(path) != len(recoverySnapshotPrefix)+26 {
		return true, errors.New("invalid recovery snapshot selection")
	}
	pending := path[len(recoverySnapshotPrefix):]
	if !id.Valid(pending) || path != recoverySnapshotPrefix+pending {
		return true, errors.New("invalid recovery snapshot selection")
	}
	return true, recover(ctx, pending)
}
