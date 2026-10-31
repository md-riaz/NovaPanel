package platform

import (
	"context"
	runner "novapanel/internal/exec"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
	"os"
	"path/filepath"
	"strings"
)

// RemoveSite removes only a validated site's home and matching dormant account.
func RemoveSite(ctx context.Context, r runner.Runner, sites, name string) error {
	if e := (&validate.V{}).SiteName(name); e != nil {
		return e
	}
	resolved, e := filepath.EvalSymlinks(sites)
	if e != nil {
		return e
	}
	if !filepath.IsAbs(sites) || resolved != sites {
		return rpcerr.Errf("invalid_argument", "Invalid site root")
	}
	root, e := os.OpenRoot(sites)
	if e != nil {
		return e
	}
	defer root.Close()
	if info, e := root.Lstat(name); e == nil && !info.IsDir() {
		return rpcerr.Errf("invalid_argument", "Invalid site home")
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	out, _, code, e := r.Run(ctx, "/usr/bin/getent", []string{"passwd", name}, runner.RunOpts{})
	if e != nil && code != 2 {
		return e
	}
	if code != 0 && code != 2 {
		return rpcerr.Errf("agent_error", "Cannot resolve site account")
	}
	if code == 0 {
		f := strings.Split(strings.TrimSpace(string(out)), ":")
		if len(f) != 7 || f[0] != name || f[2] == "0" || f[5] != filepath.Join(sites, name) || f[6] != "/usr/sbin/nologin" {
			return rpcerr.Errf("precondition_failed", "Existing site account does not match")
		}
	}
	if e = root.RemoveAll(name); e != nil {
		return e
	}
	if code == 2 {
		return nil
	}
	_, _, code, e = r.Run(ctx, "/usr/sbin/userdel", []string{"--", name}, runner.RunOpts{})
	if e != nil {
		return e
	}
	if code != 0 && code != 6 {
		return rpcerr.Errf("agent_error", "Cannot remove site account")
	}
	return nil
}
