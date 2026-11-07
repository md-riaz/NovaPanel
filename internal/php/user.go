// Package php manages the per-site PHP runtime. Callers must hold site:<id>
// and then osuser:<name> throughout account operations and revalidate state.
package php

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	runner "novapanel/internal/exec"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
)

var ulid = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

const marker = "novapanel-site="
const retainedMarker = "novapanel-retained="

func inputs(root, name, id string) error {
	if err := (&validate.V{}).SiteName(name); err != nil {
		return err
	}
	if !ulid.MatchString(id) || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return rpcerr.Errf("invalid_argument", "invalid site identity or root")
	}
	return nil
}

type account struct{ gecos string }

func lookup(ctx context.Context, r runner.Runner, root, name string) (*account, error) {
	out, _, code, err := r.Run(ctx, "/usr/bin/getent", []string{"passwd", name}, runner.RunOpts{})
	if code == 2 && ctx.Err() == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, rpcerr.Errf("agent_error", "cannot resolve site account")
	}
	f := strings.Split(strings.TrimSpace(string(out)), ":")
	if len(f) != 7 || f[0] != name || f[5] != filepath.Join(root, name) || f[6] != "/usr/sbin/nologin" {
		return nil, rpcerr.Errf("conflict", "site account differs")
	}
	uid, e1 := strconv.Atoi(f[2])
	gid, e2 := strconv.Atoi(f[3])
	if e1 != nil || e2 != nil || uid <= 0 || gid <= 0 {
		return nil, rpcerr.Errf("conflict", "invalid site account")
	}
	return &account{gecos: f[4]}, nil
}
func run(ctx context.Context, r runner.Runner, prog string, args ...string) error {
	_, _, code, err := r.Run(ctx, prog, args, runner.RunOpts{})
	if err != nil {
		return err
	}
	if code != 0 {
		return rpcerr.Errf("agent_error", "account operation failed")
	}
	return nil
}

// EnsureUser never adopts an existing account without this exact site's marker.
func EnsureUser(ctx context.Context, r runner.Runner, root, name, id string) error {
	if err := inputs(root, name, id); err != nil {
		return err
	}
	a, err := lookup(ctx, r, root, name)
	if err != nil {
		return err
	}
	if a != nil {
		if a.gecos != marker+id {
			return rpcerr.Errf("conflict", "site account is not owned")
		}
		return nil
	}
	return run(ctx, r, "/usr/sbin/useradd", "-r", "-U", "-s", "/usr/sbin/nologin", "-m", "-d", filepath.Join(root, name), "-K", "UMASK=0027", "-c", marker+id, "--", name)
}

// MigrateUser is only for a bounded startup enumeration of live Phase-3 rows.
// The caller must prove the frozen 1:1 mapping and hold the shared locks.
func MigrateUser(ctx context.Context, r runner.Runner, root, name, id string) error {
	if err := inputs(root, name, id); err != nil {
		return err
	}
	a, err := lookup(ctx, r, root, name)
	if err != nil || a == nil {
		return err
	}
	if a.gecos == marker+id {
		return nil
	}
	if a.gecos != "" {
		return rpcerr.Errf("conflict", "site account is not an unmarked legacy account")
	}
	return run(ctx, r, "/usr/sbin/usermod", "-c", marker+id, "--", name)
}

// VerifyUser checks ownership before destructive pool or home operations.
func VerifyUser(ctx context.Context, r runner.Runner, root, name, id string) error {
	return verifyUser(ctx, r, root, name, id, false)
}

// VerifyUserForDelete also accepts this site's retained marker for delete replay.
// It only verifies ownership; callers must hold the account locks.
func VerifyUserForDelete(ctx context.Context, r runner.Runner, root, name, id string) error {
	return verifyUser(ctx, r, root, name, id, true)
}

func verifyUser(ctx context.Context, r runner.Runner, root, name, id string, allowRetained bool) error {
	if err := inputs(root, name, id); err != nil {
		return err
	}
	a, err := lookup(ctx, r, root, name)
	if err != nil {
		return err
	}
	if a == nil {
		return rpcerr.Errf("not_found", "site account missing")
	}
	if a.gecos != marker+id && !(allowRetained && a.gecos == retainedMarker+id) {
		return rpcerr.Errf("conflict", "site account is not owned")
	}
	return nil
}

// RetainUser marks an owned account as retained after its pool is disabled.
// Replaying the same retention is safe; retained accounts are never adopted.
func RetainUser(ctx context.Context, r runner.Runner, root, name, id string) error {
	if err := inputs(root, name, id); err != nil {
		return err
	}
	a, err := lookup(ctx, r, root, name)
	if err != nil {
		return err
	}
	if a == nil {
		return rpcerr.Errf("not_found", "site account missing")
	}
	if a.gecos == retainedMarker+id {
		return nil
	}
	if a.gecos != marker+id {
		return rpcerr.Errf("conflict", "site account is not owned")
	}
	return run(ctx, r, "/usr/sbin/usermod", "-c", retainedMarker+id, "--", name)
}

// DeleteUser is for the owning delete job, after pool teardown and home guard.
func DeleteUser(ctx context.Context, r runner.Runner, root, name, id string) error {
	if err := inputs(root, name, id); err != nil {
		return err
	}
	a, err := lookup(ctx, r, root, name)
	if err != nil || a == nil {
		return err
	}
	if a.gecos != marker+id {
		return rpcerr.Errf("conflict", "site account is not owned")
	}
	return run(ctx, r, "/usr/sbin/userdel", "--", name)
}

// DeleteOrphan requires the caller to check live rows and jobs under both locks.
func DeleteOrphan(ctx context.Context, r runner.Runner, root, name, id string, hasLiveRowOrJob bool) error {
	if hasLiveRowOrJob {
		return rpcerr.Errf("precondition_failed", "site or job still live")
	}
	return DeleteUser(ctx, r, root, name, id)
}
