package nginx

import (
	"context"
	"novapanel/internal/platform"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
	"os"
	"path/filepath"
)

func syncDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Transaction holds the shared apply lock through pointer changes and DB commit.
// Callbacks must not call Applier methods, which acquire the same lock.
type Transaction struct{ a *Applier }

func (a *Applier) Transaction(ctx context.Context, fn func(*Transaction) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(&Transaction{a})
}

// Snapshot is nonsecret rollback state, persisted before publishing a certificate.
type Snapshot struct {
	Name    string `json:"name"`
	Config  []byte `json:"config"`
	LKG     []byte `json:"lkg"`
	Enabled bool   `json:"enabled"`
}

func (t *Transaction) directories(name string) (*os.Root, *os.Root, error) {
	if err := (&validate.V{}).SiteName(name); err != nil {
		return nil, nil, err
	}
	resolved, err := filepath.EvalSymlinks(t.a.Root)
	if err != nil || !filepath.IsAbs(t.a.Root) || resolved != t.a.Root {
		return nil, nil, rpcerr.Errf("invalid_argument", "Invalid nginx directory")
	}
	root, err := os.OpenRoot(t.a.Root)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	for _, d := range []string{"sites-available", "sites-enabled"} {
		info, e := root.Lstat(d)
		if e != nil {
			return nil, nil, e
		}
		if !info.IsDir() {
			return nil, nil, rpcerr.Errf("invalid_argument", "Invalid nginx parent")
		}
	}
	available, err := root.OpenRoot("sites-available")
	if err != nil {
		return nil, nil, err
	}
	enabled, err := root.OpenRoot("sites-enabled")
	if err != nil {
		available.Close()
		return nil, nil, err
	}
	return available, enabled, nil
}
func (t *Transaction) Snapshot(name string) (Snapshot, error) {
	s := Snapshot{Name: name}
	a, e, err := t.directories(name)
	if err != nil {
		return s, err
	}
	defer a.Close()
	defer e.Close()
	file := name + ".conf"
	s.Config, err = readRegular(a, file)
	if err != nil && !os.IsNotExist(err) {
		return s, err
	}
	s.LKG, err = readRegular(a, file+".lkg")
	if err != nil && !os.IsNotExist(err) {
		return s, err
	}
	target, err := e.Readlink(file)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if target != "../sites-available/"+file {
		return s, rpcerr.Errf("invalid_argument", "Unexpected nginx link")
	}
	s.Enabled = true
	return s, nil
}

// Restore must be called only after restoring the certificate pointer. Recovery
// validates the restored full configuration before reloading it.
func (t *Transaction) Restore(ctx context.Context, s Snapshot, reload bool) error {
	a, e, err := t.directories(s.Name)
	if err != nil {
		return err
	}
	defer a.Close()
	defer e.Close()
	file := s.Name + ".conf"
	for _, v := range []struct {
		name string
		data []byte
	}{{file, s.Config}, {file + ".lkg", s.LKG}} {
		if v.data != nil {
			err = writeAtomic(a, v.name, v.data)
		} else {
			err = a.Remove(v.name)
			if os.IsNotExist(err) {
				err = nil
			}
		}
		if err != nil {
			return err
		}
	}
	target, err := e.Readlink(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && target != "../sites-available/"+file {
		return rpcerr.Errf("invalid_argument", "Unexpected nginx link")
	}
	if s.Enabled && os.IsNotExist(err) {
		err = e.Symlink("../sites-available/"+file, file)
	} else if !s.Enabled && err == nil {
		err = e.Remove(file)
	} else {
		err = nil
	}
	if err != nil {
		return err
	}
	if err = syncDirectory(a); err != nil {
		return err
	}
	if err = syncDirectory(e); err != nil {
		return err
	}
	if !reload {
		return nil
	}
	if err = t.a.command(ctx, platform.Nginx, []string{"-t"}); err != nil {
		return err
	}
	return t.a.command(ctx, platform.Systemctl, []string{"reload", "nginx"})
}

// Apply routes every failure, including render/commit failures, through recovery
// while retaining the lock. A failed recovery never attempts a later reload.
func (t *Transaction) Apply(ctx context.Context, d Desired, recover func(context.Context, bool) error, commit func(context.Context) error) error {
	return t.a.apply(ctx, d, recover, commit)
}
