// Package sshd renders and applies per-site SSH configuration.
package sshd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	runner "novapanel/internal/exec"
	"novapanel/internal/platform"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
)

const DefaultRoot = "/etc/ssh/sshd_config.d"
const Binary = "/usr/sbin/sshd"

type Desired struct {
	Site, User string
	Enabled    bool
}
type Applier struct {
	Root   string
	Runner runner.Runner
	Guard  func(context.Context) error
	mu     sync.Mutex
}

// Diverged reports that the previous configuration could not be restored and reloaded.
var Diverged = errors.New("sshd configuration diverged")

func (a *Applier) command(ctx context.Context, prog string, args ...string) error {
	if a.Runner == nil {
		return rpcerr.Errf("agent_error", "SSH execution unavailable")
	}
	_, _, code, err := a.Runner.Run(ctx, prog, args, runner.RunOpts{})
	if err != nil || code != 0 {
		return rpcerr.Errf("agent_error", "SSH command failed: %s", prog)
	}
	return nil
}

// Heal validates and reloads the currently effective configuration.
func (a *Applier) Heal(ctx context.Context) error {
	if err := a.command(ctx, Binary, "-t"); err != nil {
		return err
	}
	return a.command(ctx, platform.Systemctl, "reload", "ssh.service")
}

func (a *Applier) Apply(ctx context.Context, d Desired) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Guard != nil {
		if err := a.Guard(ctx); err != nil {
			return err
		}
	}
	if err := (&validate.V{}).SiteName(d.Site); err != nil {
		return err
	}
	if d.User != d.Site {
		return rpcerr.Errf("invalid_argument", "Invalid SSH user binding")
	}
	dir := a.Root
	if dir == "" {
		dir = DefaultRoot
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(dir) || resolved != dir {
		return rpcerr.Errf("invalid_argument", "Invalid SSH config directory")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	name := "novapanel-" + d.Site + ".conf"
	old, err := readRegular(root, name)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// Preserve the previous state before replacing the active file.
	if exists {
		if err = writeAtomic(root, name+".lkg", old); err != nil {
			return err
		}
	} else if err = remove(root, name+".lkg"); err != nil {
		return err
	}
	var desired []byte
	if d.Enabled {
		desired = []byte(fmt.Sprintf("Match User %s\n    ForceCommand internal-sftp\n    AllowTcpForwarding no\n    X11Forwarding no\n    PermitTTY no\n", d.User))
		err = writeAtomic(root, name, desired)
	} else {
		err = remove(root, name)
	}
	if err != nil {
		return err
	}
	if err = a.Heal(ctx); err != nil {
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		var restore error
		if exists {
			restore = writeAtomic(root, name, old)
		} else {
			restore = remove(root, name)
		}
		if restore == nil {
			restore = a.command(recovery, platform.Systemctl, "reload", "ssh.service")
		}
		if restore != nil {
			return errors.Join(err, Diverged, restore)
		}
		return err
	}
	if d.Enabled {
		return writeAtomic(root, name+".lkg", desired)
	}
	return remove(root, name+".lkg")
}

func readRegular(root *os.Root, name string) ([]byte, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, rpcerr.Errf("invalid_argument", "Invalid SSH config file")
	}
	return io.ReadAll(f)
}
func syncDir(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func remove(root *os.Root, name string) error {
	err := root.Remove(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncDir(root)
}
func writeAtomic(root *os.Root, name string, data []byte) error {
	tmp := name + ".tmp"
	if info, err := root.Lstat(tmp); err == nil {
		if !info.Mode().IsRegular() {
			return rpcerr.Errf("invalid_argument", "Invalid SSH temporary file")
		}
		if err = root.Remove(tmp); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0644)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = root.Rename(tmp, name); err != nil {
		return err
	}
	return syncDir(root)
}
