package php

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	runner "novapanel/internal/exec"
	"novapanel/internal/platform"
	"novapanel/internal/rpcerr"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Applier owns a dedicated administrator-controlled pool directory. Callers must
// hold the site lock and coordinate nginx/metadata publication before serving it.
// This API does not provision identities, directories, or system services.
// All managed *.conf files are included by the system FPM configuration.
type Applier struct {
	Root   string
	Runner runner.Runner
	Probe  func(context.Context, Desired) error
	mu     sync.Mutex
}

func (a *Applier) Apply(ctx context.Context, d Desired) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, err := Render(d)
	if err != nil {
		return err
	}
	if a.Runner == nil || a.Probe == nil {
		return rpcerr.Errf("agent_error", "PHP apply dependencies unavailable")
	}
	resolved, err := filepath.EvalSymlinks(a.Root)
	if err != nil || !filepath.IsAbs(a.Root) || resolved != a.Root {
		return rpcerr.Errf("invalid_argument", "Invalid PHP directory")
	}
	for _, c := range a.Root {
		if !(c == '/' || c == '-' || c == '_' || c == '.' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return rpcerr.Errf("invalid_argument", "Invalid PHP directory")
		}
	}
	root, err := os.OpenRoot(a.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	name := d.Name + ".conf"
	// Inspect every publication target before any write or command.
	for _, file := range []string{name, name + ".lkg", name + ".tmp", "novapanel-validate.ini", "novapanel-validate.ini.tmp"} {
		if info, e := root.Lstat(file); e == nil {
			if !info.Mode().IsRegular() {
				return rpcerr.Errf("invalid_argument", "Invalid PHP file")
			}
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	good, err := poolRead(root, name+".lkg")
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// Never infer known-good from live bytes, including retries after process death.
	if err = poolWrite(root, name, b); err != nil {
		return err
	}
	restore := func() error {
		if exists {
			return poolWrite(root, name, good)
		}
		err := root.Remove(name)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	validation := []byte(fmt.Sprintf("[global]\ninclude = %s/*.conf\n", a.Root))
	if err = poolWrite(root, "novapanel-validate.ini", validation); err != nil {
		return errors.Join(err, restore())
	}
	if err = a.validate(ctx); err != nil {
		return errors.Join(err, restore())
	}
	err = a.reload(ctx)
	if err == nil {
		err = a.Probe(ctx, d)
	}
	if err == nil {
		err = poolWrite(root, name+".lkg", b)
	}
	if err == nil {
		return nil
	}
	if e := restore(); e != nil {
		return errors.Join(err, e)
	}
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if e := a.validate(recovery); e != nil {
		return errors.Join(err, e)
	}
	return errors.Join(err, a.reload(recovery))
}
func (a *Applier) validate(ctx context.Context) error {
	return a.command(ctx, "/usr/sbin/php-fpm8.3", []string{"-t", "--fpm-config", filepath.Join(a.Root, "novapanel-validate.ini")})
}
func (a *Applier) reload(ctx context.Context) error {
	return a.command(ctx, platform.Systemctl, []string{"reload", "php8.3-fpm"})
}
func (a *Applier) command(ctx context.Context, prog string, args []string) error {
	_, _, code, err := a.Runner.Run(ctx, prog, args, runner.RunOpts{})
	if err != nil || code != 0 {
		return rpcerr.Errf("agent_error", "PHP command failed")
	}
	return nil
}
func poolRead(root *os.Root, name string) ([]byte, error) {
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
		return nil, rpcerr.Errf("invalid_argument", "Invalid PHP file")
	}
	return io.ReadAll(f)
}
func poolWrite(root *os.Root, name string, b []byte) error {
	for _, file := range []string{name, name + ".tmp"} {
		if info, err := root.Lstat(file); err == nil {
			if !info.Mode().IsRegular() {
				return rpcerr.Errf("invalid_argument", "Invalid PHP file")
			}
			if strings.HasSuffix(file, ".tmp") {
				if err = root.Remove(file); err != nil {
					return err
				}
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	temp := name + ".tmp"
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0644)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = root.Rename(temp, name); err != nil {
		return err
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
