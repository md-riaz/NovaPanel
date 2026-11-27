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

type account struct {
	gecos    string
	uid, gid uint32
}

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
	uid, e1 := strconv.ParseUint(f[2], 10, 32)
	gid, e2 := strconv.ParseUint(f[3], 10, 32)
	if e1 != nil || e2 != nil || uid == 0 || gid == 0 || uid == 1<<32-1 || gid == 1<<32-1 {
		return nil, rpcerr.Errf("conflict", "invalid site account")
	}
	return &account{gecos: f[4], uid: uint32(uid), gid: uint32(gid)}, nil
}

// ResolveUser returns the numeric identity of the account owned by this site.
func ResolveUser(ctx context.Context, r runner.Runner, root, name, id string) (uint32, uint32, error) {
	if err := inputs(root, name, id); err != nil {
		return 0, 0, err
	}
	a, err := lookup(ctx, r, root, name)
	if err != nil {
		return 0, 0, err
	}
	if a == nil || a.gecos != marker+id {
		return 0, 0, rpcerr.Errf("precondition_failed", "site account unavailable")
	}
	return a.uid, a.gid, nil
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

// EnsureReservedUser requires a durable reservation and the site and OS-user locks.
// The locked group password field carries the ownership marker across crashes.
func EnsureReservedUser(ctx context.Context, r runner.Runner, root, name, id string, uid uint32) error {
	if err := inputs(root, name, id); err != nil {
		return err
	}
	if uid < 2000000 || uid > 2999999 {
		return rpcerr.Errf("precondition_failed", "site identity outside reserved range")
	}
	account, err := lookup(ctx, r, root, name)
	if err != nil {
		return err
	}
	if account != nil && (account.gecos != marker+id || account.uid != uid || account.gid != uid) {
		return rpcerr.Errf("conflict", "site account is not owned")
	}
	numeric := strconv.FormatUint(uint64(uid), 10)
	get := func(database, key string) ([]byte, bool, error) {
		out, _, code, err := r.Run(ctx, "/usr/bin/getent", []string{database, key}, runner.RunOpts{})
		if code == 2 && ctx.Err() == nil {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if code != 0 {
			return nil, false, rpcerr.Errf("agent_error", "cannot resolve site identity")
		}
		return out, true, nil
	}
	if _, exists, e := get("passwd", numeric); e != nil {
		return e
	} else if exists && account == nil {
		return rpcerr.Errf("conflict", "site UID already allocated")
	} else if !exists && account != nil {
		return rpcerr.Errf("conflict", "site UID changed")
	}
	out, exists, err := get("group", numeric)
	if err != nil {
		return err
	}
	if exists {
		fields := strings.Split(strings.TrimSpace(string(out)), ":")
		if len(fields) != 4 || fields[0] != name || fields[2] != numeric || fields[3] != "" {
			return rpcerr.Errf("conflict", "site group is not owned")
		}
		shadow, found, err := get("gshadow", name)
		if err != nil {
			return err
		}
		parts := strings.Split(strings.TrimSpace(string(shadow)), ":")
		if !found || len(parts) != 4 || parts[0] != name || parts[1] != "!"+marker+id || parts[2] != "" || parts[3] != "" {
			return rpcerr.Errf("conflict", "site group is not owned")
		}
	} else {
		if account != nil {
			return rpcerr.Errf("conflict", "site group missing")
		}
		if _, taken, e := get("group", name); e != nil {
			return e
		} else if taken {
			return rpcerr.Errf("conflict", "site group name already allocated")
		}
		if err = run(ctx, r, "/usr/sbin/groupadd", "-g", numeric, "-p", "!"+marker+id, "--", name); err != nil {
			return err
		}
	}
	if account != nil {
		return nil
	}
	return run(ctx, r, "/usr/sbin/useradd", "-u", numeric, "-g", numeric, "-s", "/usr/sbin/nologin", "-m", "-d", filepath.Join(root, name), "-K", "UMASK=0027", "-c", marker+id, "--", name)
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

// ResolveUserForDelete accepts the retained marker during deletion replay.
func ResolveUserForDelete(ctx context.Context, r runner.Runner, root, name, id string) (uint32, uint32, error) {
	if err := verifyUser(ctx, r, root, name, id, true); err != nil {
		return 0, 0, err
	}
	a, err := lookup(ctx, r, root, name)
	if err != nil {
		return 0, 0, err
	}
	if a == nil || (a.gecos != marker+id && a.gecos != retainedMarker+id) {
		return 0, 0, rpcerr.Errf("precondition_failed", "site account unavailable")
	}
	return a.uid, a.gid, nil
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
	// Absence of managed paths cannot establish that this UID owns no files
	// elsewhere. Keep the UID reserved until an operator can verify cleanup.
	return rpcerr.Errf("precondition_failed", "orphan account requires manual UID-owned file cleanup")
}
