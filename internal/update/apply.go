package update

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"novapanel/internal/exec"
	"novapanel/internal/platform"
	"novapanel/internal/rpcerr"
	"os"
	"path/filepath"
	"time"
)

const recoveryTimeout = 120 * time.Second

type pendingUpdate struct {
	Previous string `json:"previous"`
	Next     string `json:"next"`
}

// apply is the offline orchestration seam. The independent helper owns this
// directory exclusively; it must not run inside the daemon being restarted.
func apply(ctx context.Context, root, next string, a Artifact, src io.Reader, runner exec.Runner, ping func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, recoveryTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if runner == nil || ping == nil {
		return rpcerr.Errf("invalid_argument", "update helper dependencies unavailable")
	}
	release, err := lockInstall(root)
	if err != nil {
		return err
	}
	defer release()
	v, ok := parseVersion(next)
	if !ok || v.pre != "" {
		return rpcerr.Errf("invalid_argument", "invalid update version")
	}
	if _, e := os.Lstat(filepath.Join(root, ".update-pending")); !os.IsNotExist(e) {
		return rpcerr.Errf("conflict", "update recovery required")
	}
	previous, err := os.Readlink(filepath.Join(root, "current"))
	if err != nil {
		return err
	}
	if !localVersion(root, previous) {
		return rpcerr.Errf("precondition_failed", "current version is not retained locally")
	}
	if old, valid := parseVersion(previous); valid && compareRelease(v, old) <= 0 {
		return rpcerr.Errf("precondition_failed", "update version must be newer")
	}
	if _, e := os.Lstat(filepath.Join(root, next)); !os.IsNotExist(e) {
		return rpcerr.Errf("conflict", "update version already exists")
	}
	dir, err := stage(ctx, root, a, src)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	// Staging stays private until the complete archive has passed validation.
	if err = os.Chmod(dir, 0755); err != nil {
		return err
	}
	if err = syncDir(dir); err != nil {
		return err
	}
	if err = os.Rename(dir, filepath.Join(root, next)); err != nil {
		return err
	}
	p := pendingUpdate{previous, next}
	body, _ := json.Marshal(p)
	if err = writePending(root, body); err != nil {
		return err
	}
	if err = swapCurrent(root, next); err != nil {
		return err
	}
	if err = restart(ctx, runner); err == nil {
		err = ping(ctx)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		// Recovery must survive expiry of the request that triggered the failed update.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recoveryTimeout)
		defer cancel()
		return errors.Join(err, recoverLocked(rollbackCtx, root, runner))
	}
	if err = os.Remove(filepath.Join(root, ".update-pending")); err != nil {
		return err
	}
	return syncDir(root)
}
func localVersion(root, v string) bool {
	if v == "" || v == "." || v == ".." || filepath.Base(v) != v {
		return false
	}
	s, e := os.Lstat(filepath.Join(root, v))
	return e == nil && s.IsDir() && s.Mode()&os.ModeSymlink == 0
}
func writePending(root string, body []byte) error {
	f, e := os.CreateTemp(root, ".pending-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(body); e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	// Link publishes the complete record without overwriting an existing recovery.
	if e = os.Link(f.Name(), filepath.Join(root, ".update-pending")); e != nil {
		return e
	}
	return syncDir(root)
}
func swapCurrent(root, target string) error {
	f, e := os.CreateTemp(root, ".current-")
	if e != nil {
		return e
	}
	name := f.Name()
	if e = f.Close(); e != nil {
		return e
	}
	defer os.Remove(name)
	if e = os.Remove(name); e != nil {
		return e
	}
	if e = os.Symlink(target, name); e != nil {
		return e
	}
	if e = os.Rename(name, filepath.Join(root, "current")); e != nil {
		return e
	}
	return syncDir(root)
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func restart(ctx context.Context, r exec.Runner) error {
	_, _, exit, e := r.Run(ctx, platform.Systemctl, []string{"restart", "novapanel-agent.service", "novapanel-web.service"}, exec.RunOpts{})
	if e != nil {
		return e
	}
	if exit != 0 {
		return rpcerr.Errf("agent_error", "update restart failed")
	}
	return nil
}
func lockInstall(root string) (func(), error) {
	fd, e := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		unix.Close(fd)
		return nil, rpcerr.Errf("conflict", "update already running")
	}
	return func() { unix.Close(fd) }, nil
}
func recoverApply(ctx context.Context, root string, r exec.Runner) error {
	release, e := lockInstall(root)
	if e != nil {
		return e
	}
	defer release()
	return recoverLocked(ctx, root, r)
}
func recoverLocked(ctx context.Context, root string, r exec.Runner) error {
	body, e := os.ReadFile(filepath.Join(root, ".update-pending"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var p pendingUpdate
	if json.Unmarshal(body, &p) != nil || !localVersion(root, p.Previous) || !localVersion(root, p.Next) {
		return rpcerr.Errf("agent_error", "invalid update recovery record")
	}
	target, e := os.Readlink(filepath.Join(root, "current"))
	if e != nil {
		return e
	}
	if target != p.Next && target != p.Previous {
		return rpcerr.Errf("conflict", "current version changed during update")
	}
	if e = swapCurrent(root, p.Previous); e != nil {
		return e
	}
	if e = restart(ctx, r); e != nil {
		return e
	}
	if e = os.Remove(filepath.Join(root, ".update-pending")); e != nil {
		return e
	}
	return syncDir(root)
}
