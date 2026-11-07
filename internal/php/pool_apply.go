package php

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	runner "novapanel/internal/exec"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"novapanel/internal/validate"
)

// PoolApplier manages the shared FPM configuration. Dir and all its ancestors
// must be administrator-owned. Callers serialize site lifecycle operations and
// verify site ownership before removing or replacing a pool.
type PoolApplier struct {
	Dir    string
	Runner runner.Runner
	Probe  func(context.Context) error
	mu     sync.Mutex
}

func (a *PoolApplier) Apply(ctx context.Context, name string, maxChildren int) error {
	return a.ApplyLimits(ctx, name, maxChildren, store.PHPLimits{})
}

// ApplyLimits validates and applies a pool with the persisted PHP overrides.
func (a *PoolApplier) ApplyLimits(ctx context.Context, name string, maxChildren int, limits store.PHPLimits) error {
	b, err := RenderPoolLimits(name, maxChildren, limits)
	if err != nil {
		return err
	}
	return a.change(ctx, name, b, false)
}
func (a *PoolApplier) Remove(ctx context.Context, name string) error {
	if err := (&validate.V{}).SiteName(name); err != nil {
		return err
	}
	return a.change(ctx, name, nil, true)
}

func poolRoot(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, rpcerr.Errf("invalid_argument", "invalid pool directory")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	defer unix.Close(fd)
	return os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", fd))
}
func poolRead(root *os.Root, name string) ([]byte, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, rpcerr.Errf("invalid_argument", "invalid pool file")
	}
	return io.ReadAll(f)
}
func poolSync(root *os.Root) error {
	f, e := root.Open(".")
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func poolWrite(root *os.Root, name string, b []byte) error {
	tmp := name + ".tmp"
	info, e := root.Lstat(tmp)
	if e == nil {
		if !info.Mode().IsRegular() {
			return rpcerr.Errf("invalid_argument", "invalid temporary pool file")
		}
		if e = root.Remove(tmp); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	f, e := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0644)
	if e != nil {
		return e
	}
	defer root.Remove(tmp)
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if e = root.Rename(tmp, name); e != nil {
		return e
	}
	return poolSync(root)
}
func (a *PoolApplier) command(ctx context.Context, bin string, args ...string) error {
	_, _, code, err := a.Runner.Run(ctx, bin, args, runner.RunOpts{})
	if err != nil {
		return err
	}
	if code != 0 {
		return rpcerr.Errf("agent_error", "FPM command failed")
	}
	return nil
}
func (a *PoolApplier) change(ctx context.Context, name string, b []byte, remove bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Runner == nil || a.Probe == nil {
		return rpcerr.Errf("agent_error", "FPM apply dependencies unavailable")
	}
	root, err := poolRoot(a.Dir)
	if err != nil {
		return err
	}
	defer root.Close()
	file := name + ".conf"
	old, err := poolRead(root, file)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	good, gerr := poolRead(root, file+".lkg")
	if gerr != nil && !os.IsNotExist(gerr) {
		return gerr
	}
	if remove && !exists && os.IsNotExist(gerr) {
		return nil
	}
	if !remove && exists && gerr == nil && bytes.Equal(old, b) && bytes.Equal(good, b) {
		return nil
	}
	// Last-known-good is authoritative even after an interrupted previous apply.
	restore := func() error {
		if gerr == nil {
			return poolWrite(root, file, good)
		}
		e := root.Remove(file)
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		return poolSync(root)
	}
	if remove {
		if exists {
			err = root.Remove(file)
			if err == nil {
				err = poolSync(root)
			}
		}
	} else {
		err = poolWrite(root, file, b)
	}
	if err != nil {
		return errors.Join(err, restore())
	}
	if err = a.command(ctx, "/usr/sbin/php-fpm8.3", "-t"); err != nil {
		return errors.Join(err, restore())
	}
	err = a.command(ctx, "/usr/bin/systemctl", "reload", "php8.3-fpm.service")
	if err == nil {
		err = a.Probe(ctx)
	}
	if err != nil {
		rerr := restore()
		if rerr != nil {
			return errors.Join(err, rerr)
		}
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return errors.Join(err, a.command(recovery, "/usr/bin/systemctl", "reload", "php8.3-fpm.service"))
	}
	if !remove {
		return poolWrite(root, file+".lkg", b)
	}
	if gerr == nil {
		if err = root.Remove(file + ".lkg"); err != nil {
			return err
		}
		return poolSync(root)
	}
	return nil
}
