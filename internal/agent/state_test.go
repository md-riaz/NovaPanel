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
	cfg.SitesRoot = filepath.Join(d, "sites")
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

func TestStateRejectsSharedPrivateLogDirectory(t *testing.T) {
	cfg := config.Defaults()
	cfg.LogDir = filepath.Dir(cfg.DBPath)
	a := NewWithDependencies(cfg, &startupRunner{}, nil)
	if cleanup, err := a.OpenState(context.Background()); err == nil {
		cleanup()
		t.Fatal("accepted shared private state and log directory")
	}
}

func TestStateStartup(t *testing.T) {
	for _, tc := range []struct {
		name           string
		fail, existing bool
	}{{"new log root", false, false}, {"existing log root", false, true}, {"append-only failure", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			d := t.TempDir()
			cfg := config.Defaults()
			cfg.DBPath = filepath.Join(d, "state", "panel.db")
			cfg.SealKeyPath = filepath.Join(d, "state", "seal.key")
			cfg.LogDir = filepath.Join(d, "logs")
			cfg.SocketPath = filepath.Join(d, "run", "agent.sock")
			cfg.SitesRoot = filepath.Join(d, "sites")
			cfg.ACMEWebroot = filepath.Join(d, "acme")
			cfg.TLSDir = filepath.Join(d, "tls")
			if tc.existing {
				if e := os.Mkdir(cfg.LogDir, 0750); e != nil {
					t.Fatal(e)
				}
			}
			r := &startupRunner{fail: tc.fail}
			a := NewWithDependencies(cfg, r, nil)
			close, e := a.OpenState(context.Background())
			if tc.fail {
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
			logInfo, err := os.Stat(cfg.LogDir)
			if err != nil || logInfo.Mode().Perm() != 0755 {
				t.Fatalf("log root must be 0755: %v, %v", logInfo, err)
			}
			auditInfo, err := os.Stat(filepath.Join(cfg.LogDir, "audit.log"))
			if err != nil || auditInfo.Mode().Perm() != 0600 {
				t.Fatalf("audit file must remain private: %v, %v", auditInfo, err)
			}
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
