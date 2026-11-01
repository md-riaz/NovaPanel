package php

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"novapanel/internal/rpcerr"
)

// Transaction holds the shared pool lock. Callers must acquire nginx first and
// must not call Applier methods from fn. This is not a durable journal.
type Transaction struct {
	a       *Applier
	root    *os.Root
	name    string
	good    []byte
	desired []byte
	live    []byte
}

func (a *Applier) Transaction(ctx context.Context, d Desired, fn func(*Transaction) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
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
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	err = poolCustody(dir)
	dir.Close()
	if err != nil {
		return err
	}
	name := d.Name + ".conf"
	for _, file := range []string{name, name + ".lkg", name + ".tmp", "novapanel-validate.ini", "novapanel-validate.ini.tmp"} {
		if info, e := root.Lstat(file); e == nil {
			if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
				return rpcerr.Errf("invalid_argument", "Invalid PHP file")
			}
			f, e := root.OpenFile(file, os.O_RDONLY|unix.O_NOFOLLOW, 0)
			if e != nil {
				return e
			}
			e = poolCustody(f)
			if e == nil {
				var st unix.Stat_t
				e = unix.Fstat(int(f.Fd()), &st)
				if e == nil && st.Nlink != 1 {
					e = rpcerr.Errf("invalid_argument", "Invalid PHP links")
				}
			}
			f.Close()
			if e != nil {
				return e
			}
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	good, err := poolRead(root, name+".lkg")
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	live, e := poolRead(root, name)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	marker := "; novapanel managed pool\n"
	if e == nil && good == nil && !strings.HasPrefix(string(live), marker) {
		return rpcerr.Errf("precondition_failed", "Unmanaged PHP pool")
	}
	return fn(&Transaction{a: a, root: root, name: name, good: good, live: live, desired: append([]byte(marker), b...)})
}

// Snapshot captures the exact live and last-known-good files while the pool
// transaction lock is held. A nil slice denotes absence, not an empty file.
type Snapshot struct {
	Live []byte `json:"live"`
	LKG  []byte `json:"lkg"`
}

func (t *Transaction) Snapshot() Snapshot {
	return Snapshot{Live: bytes.Clone(t.live), LKG: bytes.Clone(t.good)}
}

// RestoreSnapshot restores both original artifacts, including absence. The
// caller must persist and authenticate the snapshot before publication and
// validate, reload and probe the restored services before releasing recovery.
func (t *Transaction) RestoreSnapshot(s Snapshot) error {
	var result error
	for _, v := range []struct {
		name string
		data []byte
	}{
		{t.name, s.Live}, {t.name + ".lkg", s.LKG},
	} {
		var err error
		if v.data != nil {
			err = poolWrite(t.root, v.name, v.data)
		} else {
			// Refuse to remove an unexpected symlink or other special file.
			if info, e := t.root.Lstat(v.name); e == nil && !info.Mode().IsRegular() {
				err = rpcerr.Errf("invalid_argument", "Invalid PHP file")
			} else if e != nil && !os.IsNotExist(e) {
				err = e
			} else if e == nil {
				err = t.root.Remove(v.name)
			}
		}
		result = errors.Join(result, err)
	}
	if result != nil {
		return result
	}
	dir, err := t.root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// Unchanged requires both live bytes and the separately proven LKG.
func (t *Transaction) Unchanged() bool {
	return t.good != nil && bytes.Equal(t.good, t.desired) && bytes.Equal(t.live, t.desired)
}

func (t *Transaction) Stage() error {
	if err := poolWrite(t.root, t.name, t.desired); err != nil {
		return err
	}
	return poolWrite(t.root, "novapanel-validate.ini", []byte(fmt.Sprintf("[global]\ninclude = %s/*.conf\n", t.a.Root)))
}
func (t *Transaction) Validate(ctx context.Context) error { return t.a.validate(ctx) }
func (t *Transaction) Reload(ctx context.Context) error   { return t.a.reload(ctx) }
func (t *Transaction) Promote() error                     { return poolWrite(t.root, t.name+".lkg", t.desired) }
func (t *Transaction) Restore() error {
	var result error
	for _, file := range []string{t.name, t.name + ".lkg"} {
		var err error
		if t.good != nil {
			err = poolWrite(t.root, file, t.good)
		} else {
			err = t.root.Remove(file)
			if os.IsNotExist(err) {
				err = nil
			}
		}
		result = errors.Join(result, err)
	}
	return result
}
