package nginx

import (
	"context"
	"errors"
	"novapanel/internal/platform"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
	"os"
	"path/filepath"
)

// Remove retains the last good file until reload succeeds, so retries after a
// crash between unlink and reload still stop the old live workers serving.
func (a *Applier) Remove(ctx context.Context, name string) error {
	if e := (&validate.V{}).SiteName(name); e != nil {
		return e
	}
	if a.Runner == nil {
		return rpcerr.Errf("agent_error", "Nginx runner unavailable")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Guard != nil {
		if err := a.Guard(ctx); err != nil {
			return err
		}
	}
	resolved, e := filepath.EvalSymlinks(a.Root)
	if e != nil {
		return e
	}
	if !filepath.IsAbs(a.Root) || resolved != a.Root {
		return rpcerr.Errf("invalid_argument", "Invalid nginx directory")
	}
	root, e := os.OpenRoot(a.Root)
	if e != nil {
		return e
	}
	defer root.Close()
	for _, p := range []string{"sites-available", "sites-enabled"} {
		info, e := root.Lstat(p)
		if e != nil {
			return e
		}
		if !info.IsDir() {
			return rpcerr.Errf("invalid_argument", "Invalid nginx parent")
		}
	}
	available, e := root.OpenRoot("sites-available")
	if e != nil {
		return e
	}
	defer available.Close()
	enabled, e := root.OpenRoot("sites-enabled")
	if e != nil {
		return e
	}
	defer enabled.Close()
	file := name + ".conf"
	link := "../sites-available/" + file
	if _, e = enabled.Lstat(file); e == nil {
		target, e := enabled.Readlink(file)
		if e != nil {
			return e
		}
		if target != link {
			return rpcerr.Errf("invalid_argument", "Unexpected nginx link")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	// Even a fully absent config requires reload on retry: a prior process may
	// have crashed after removing files but before signalling nginx.
	if e = enabled.Remove(file); e != nil && !os.IsNotExist(e) {
		return e
	}
	if e = a.command(ctx, platform.Nginx, []string{"-t"}); e != nil {
		return e
	}
	if e = a.command(ctx, platform.Systemctl, []string{"reload", "nginx"}); e != nil {
		return e
	}
	for _, p := range []string{file, file + ".lkg", file + ".tmp"} {
		if e = available.Remove(p); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	return nil
}
