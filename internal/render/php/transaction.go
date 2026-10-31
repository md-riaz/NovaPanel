package php

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
			f, e := root.Open(file)
			if e != nil {
				return e
			}
			e = poolCustody(f)
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
	return fn(&Transaction{a: a, root: root, name: name, good: good, desired: append([]byte(marker), b...)})
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
