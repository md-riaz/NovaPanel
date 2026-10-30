package nginx

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	runner "novapanel/internal/exec"
	"novapanel/internal/platform"
	"novapanel/internal/rpcerr"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Applier serializes changes to nginx's shared live configuration set.
// Root must be an administrator-owned nginx directory, never a site-owned path.
type Applier struct {
	Root   string
	Runner runner.Runner
	Probe  func(context.Context, Desired) error
	Now    func() time.Time
	mu     sync.Mutex
}

func (a *Applier) Apply(ctx context.Context, d Desired) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d.TLS != nil {
		now := time.Now()
		if a.Now != nil {
			now = a.Now()
		}
		state, err := LoadTLS(d.TLS.root, d.TLS.primary, d.TLS.staging, now)
		if err != nil {
			return err
		}
		d.TLS = state
	}
	b, e := Render(d)
	if e != nil {
		return e
	}
	if a.Runner == nil || a.Probe == nil {
		return rpcerr.Errf("agent_error", "Nginx apply dependencies unavailable")
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
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		info, e := root.Lstat(dir)
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
	name := d.Name + ".conf"
	old, e := readRegular(available, name)
	exists := e == nil
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	link := "../sites-available/" + name
	hadLink := false
	if info, e := enabled.Lstat(name); e == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return rpcerr.Errf("invalid_argument", "Invalid nginx enabled file")
		}
		target, e := enabled.Readlink(name)
		if e != nil {
			return e
		}
		if target != link {
			return rpcerr.Errf("invalid_argument", "Unexpected nginx link")
		}
		hadLink = true
	} else if !os.IsNotExist(e) {
		return e
	}
	good, goodErr := readRegular(available, name+".lkg")
	if goodErr != nil && !os.IsNotExist(goodErr) {
		return goodErr
	}
	if !d.ForceReload && exists && hadLink && goodErr == nil && bytes.Equal(old, b) && bytes.Equal(good, b) {
		return nil
	}
	// Only a successful reload and probe promote bytes to last-known-good.
	old = good
	exists = goodErr == nil
	if e = writeAtomic(available, name, b); e != nil {
		return e
	}
	restore := func() error {
		var err error
		if exists {
			err = writeAtomic(available, name, old)
		} else {
			err = available.Remove(name)
		}
		if !hadLink {
			err = errors.Join(err, enabled.Remove(name))
		}
		return err
	}
	if !hadLink {
		if e = enabled.Symlink(link, name); e != nil {
			if exists {
				return errors.Join(e, writeAtomic(available, name, old))
			}
			return errors.Join(e, available.Remove(name))
		}
	}
	if e = a.command(ctx, platform.Nginx, []string{"-t"}); e != nil {
		return errors.Join(e, restore())
	}
	if e = a.command(ctx, platform.Systemctl, []string{"reload", "nginx"}); e == nil {
		e = a.Probe(ctx, d)
	}
	if e != nil {
		restored := restore()
		if restored != nil {
			return errors.Join(e, restored)
		}
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return errors.Join(e, a.command(recovery, platform.Systemctl, []string{"reload", "nginx"}))
	}
	return writeAtomic(available, name+".lkg", b)
}
func (a *Applier) command(ctx context.Context, prog string, args []string) error {
	_, _, code, e := a.Runner.Run(ctx, prog, args, runner.RunOpts{})
	if e != nil {
		return e
	}
	if code != 0 {
		return rpcerr.Errf("agent_error", "Nginx command failed")
	}
	return nil
}
func readRegular(root *os.Root, name string) ([]byte, error) {
	f, e := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, rpcerr.Errf("invalid_argument", "Invalid nginx file")
	}
	return io.ReadAll(f)
}
func writeAtomic(root *os.Root, name string, b []byte) error {
	temp := name + ".tmp"
	if info, err := root.Lstat(temp); err == nil {
		if !info.Mode().IsRegular() {
			return rpcerr.Errf("invalid_argument", "Invalid nginx temporary file")
		}
		if err = root.Remove(temp); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, e := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0644)
	if e != nil {
		return e
	}
	defer root.Remove(temp)
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	return root.Rename(temp, name)
}
