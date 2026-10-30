package platform

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	runner "novapanel/internal/exec"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ensureDirectory rejects symlinks in every component, including site-owned parents.
func ensureDirectory(root *os.Root, path string) (*os.File, error) {
	parent, e := root.OpenFile(".", os.O_RDONLY|unix.O_DIRECTORY, 0)
	if e != nil {
		return nil, e
	}
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		e = unix.Mkdirat(int(parent.Fd()), part, 0755)
		if e != nil && !errors.Is(e, unix.EEXIST) {
			parent.Close()
			return nil, e
		}
		fd, err := unix.Openat(int(parent.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		parent.Close()
		if err != nil {
			return nil, err
		}
		parent = os.NewFile(uintptr(fd), part)
	}
	return parent, nil
}

// EnsureSite only touches validated directories through held, no-follow descriptors.
func EnsureSite(ctx context.Context, r runner.Runner, sites, acme, name string) error {
	if e := (&validate.V{}).SiteName(name); e != nil {
		return e
	}
	roots := make([]*os.Root, 0, 2)
	defer func() {
		for _, root := range roots {
			root.Close()
		}
	}()
	for _, p := range []string{sites, acme} {
		resolved, e := filepath.EvalSymlinks(p)
		if e != nil {
			return e
		}
		if !filepath.IsAbs(p) || resolved != p {
			return rpcerr.Errf("invalid_argument", "Invalid site root")
		}
		root, e := os.OpenRoot(p)
		if e != nil {
			return e
		}
		roots = append(roots, root)
	}
	for i, paths := range [][]string{{name, filepath.Join(name, "public")}, {name, filepath.Join(name, ".well-known"), filepath.Join(name, ".well-known", "acme-challenge")}} {
		for _, p := range paths {
			info, e := roots[i].Lstat(p)
			if e == nil && !info.IsDir() {
				return rpcerr.Errf("invalid_argument", "Invalid site directory")
			}
			if e != nil && !os.IsNotExist(e) {
				return e
			}
		}
	}
	run := func(prog string, args ...string) ([]byte, int, error) {
		out, _, code, e := r.Run(ctx, prog, args, runner.RunOpts{})
		return out, code, e
	}
	home := filepath.Join(sites, name)
	out, code, e := run("/usr/bin/getent", "passwd", name)
	if e != nil && code != 2 {
		return e
	}
	if code == 2 {
		_, code, e = run("/usr/sbin/useradd", "-r", "-U", "-s", "/usr/sbin/nologin", "-m", "-d", home, "-K", "UMASK=0027", "--", name)
		if e != nil {
			return e
		}
		if code != 0 {
			return rpcerr.Errf("agent_error", "Cannot create site account")
		}
		out, code, e = run("/usr/bin/getent", "passwd", name)
		if e != nil {
			return e
		}
	}
	if code != 0 {
		return rpcerr.Errf("agent_error", "Cannot resolve site account")
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ":")
	if len(fields) != 7 || fields[0] != name || fields[5] != home || fields[6] != "/usr/sbin/nologin" {
		return rpcerr.Errf("precondition_failed", "Existing site account does not match")
	}
	uid, e := strconv.Atoi(fields[2])
	if e != nil || uid <= 0 {
		return rpcerr.Errf("precondition_failed", "Invalid site account")
	}
	gid, e := strconv.Atoi(fields[3])
	if e != nil || gid <= 0 {
		return rpcerr.Errf("precondition_failed", "Invalid site group")
	}
	out, code, e = run("/usr/bin/getent", "group", "www-data")
	if e != nil {
		return e
	}
	fields = strings.Split(strings.TrimSpace(string(out)), ":")
	if code != 0 || len(fields) != 4 || fields[0] != "www-data" {
		return rpcerr.Errf("agent_error", "Cannot resolve nginx group")
	}
	webgid, e := strconv.Atoi(fields[2])
	if e != nil || webgid <= 0 {
		return rpcerr.Errf("agent_error", "Invalid nginx group")
	}
	for _, dir := range []struct {
		root     *os.Root
		path     string
		uid, gid int
		mode     os.FileMode
	}{
		{roots[0], name, uid, gid, 0751}, {roots[0], filepath.Join(name, "public"), uid, webgid, os.ModeSetgid | 0750},
		{roots[1], name, 0, 0, 0755}, {roots[1], filepath.Join(name, ".well-known"), 0, 0, 0755}, {roots[1], filepath.Join(name, ".well-known", "acme-challenge"), 0, 0, 0755},
	} {
		f, e := ensureDirectory(dir.root, dir.path)
		if e != nil {
			return e
		}
		var stat unix.Stat_t
		e = unix.Fstat(int(f.Fd()), &stat)
		if e == nil && (int(stat.Uid) != dir.uid || int(stat.Gid) != dir.gid) {
			e = f.Chown(dir.uid, dir.gid)
		}
		if e == nil {
			info, statErr := f.Stat()
			e = statErr
			if e == nil && info.Mode()&(os.ModePerm|os.ModeSetgid) != dir.mode {
				e = f.Chmod(dir.mode)
			}
		}
		if e = errors.Join(e, f.Close()); e != nil {
			return fmt.Errorf("site directory: %w", e)
		}
	}
	return nil
}
