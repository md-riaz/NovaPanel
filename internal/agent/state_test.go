package agent

import (
	"context"
	"errors"
	"novapanel/internal/config"
	"novapanel/internal/exec"
	"os"
	"path/filepath"
	"testing"
)

type startupRunner struct {
	fail  bool
	calls int
}

func (r *startupRunner) Run(_ context.Context, p string, args []string, _ exec.RunOpts) ([]byte, []byte, int, error) {
	r.calls++
	if p != "/usr/bin/chattr" || len(args) != 2 || args[0] != "+a" {
		return nil, nil, -1, errors.New("unexpected command")
	}
	if r.fail {
		return nil, nil, 1, errors.New("unsupported filesystem")
	}
	return nil, nil, 0, nil
}
func TestStateRejectsWritableDirectory(t *testing.T) {
	d := t.TempDir()
	cfg := config.Defaults()
	cfg.DBPath = filepath.Join(d, "state", "panel.db")
	cfg.SealKeyPath = filepath.Join(d, "state", "seal.key")
	cfg.LogDir = filepath.Join(d, "logs")
	cfg.SocketPath = filepath.Join(d, "run", "agent.sock")
	cfg.ACMEWebroot = filepath.Join(d, "acme")
	cfg.TLSDir = filepath.Join(d, "tls")
	if e := os.MkdirAll(filepath.Dir(cfg.DBPath), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(filepath.Dir(cfg.DBPath), 0777); e != nil {
		t.Fatal(e)
	}
	a := NewWithDependencies(cfg, &startupRunner{}, nil)
	if cleanup, e := a.OpenState(context.Background()); e == nil {
		cleanup()
		t.Fatal("accepted writable state directory")
	}
}

func TestStateStartup(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "append-only failure"}[fail], func(t *testing.T) {
			d := t.TempDir()
			cfg := config.Defaults()
			cfg.DBPath = filepath.Join(d, "state", "panel.db")
			cfg.SealKeyPath = filepath.Join(d, "state", "seal.key")
			cfg.LogDir = filepath.Join(d, "logs")
			cfg.SocketPath = filepath.Join(d, "run", "agent.sock")
			cfg.ACMEWebroot = filepath.Join(d, "acme")
			cfg.TLSDir = filepath.Join(d, "tls")
			r := &startupRunner{fail: fail}
			a := NewWithDependencies(cfg, r, nil)
			close, e := a.OpenState(context.Background())
			if fail {
				if e == nil {
					close()
					t.Fatal("ignored append-only failure")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			defer close()
			if r.calls != 1 || a.Store == nil || a.Secret == nil || a.Audit == nil {
				t.Fatal("state not wired")
			}
			st, e := os.Stat(cfg.SealKeyPath)
			if e != nil || st.Mode().Perm() != 0600 {
				t.Fatal("unsafe key")
			}
		})
	}
}
