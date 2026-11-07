package php

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
)

type poolRunner struct {
	calls int
	hook  func(context.Context, int, []string) error
}

func (r *poolRunner) Run(ctx context.Context, _ string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls++
	if r.hook != nil {
		if err := r.hook(ctx, r.calls, args); err != nil {
			return nil, nil, 0, err
		}
	}
	return nil, nil, 0, nil
}
func TestCandidatePreservesMaster(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pool.d")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	master := "[global]\nerror_log = /custom/log\ninclude=" + dir + "/*.conf\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "php-fpm.conf"), []byte(master), 0600); err != nil {
		t.Fatal(err)
	}
	r := &poolRunner{hook: func(_ context.Context, n int, args []string) error {
		if n != 1 {
			return nil
		}
		b, err := os.ReadFile(args[len(args)-1])
		if err != nil {
			return err
		}
		if !strings.Contains(string(b), "error_log = /custom/log") {
			t.Errorf("lost master config: %s", b)
		}
		return nil
	}}
	a := PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	if err := a.ApplyCommitted(context.Background(), "demo", PoolState{Exists: true, MaxChildren: 3}, PoolState{}); err != nil {
		t.Fatal(err)
	}
}

func TestRealFPMCandidate(t *testing.T) {
	bin := "/usr/sbin/php-fpm8.3"
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skip("php-fpm8.3 unavailable")
	}
	dir := filepath.Join(t.TempDir(), "pool.d")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	master := "[global]\nerror_log = /dev/null\ninclude=" + dir + "/*.conf\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "php-fpm.conf"), []byte(master), 0600); err != nil {
		t.Fatal(err)
	}
	pool := "[www]\nuser = www-data\ngroup = www-data\nlisten = /tmp/novapanel-test-unused.sock\npm = ondemand\npm.max_children = 2\n"
	if err := os.WriteFile(filepath.Join(dir, "www.conf"), []byte(pool), 0600); err != nil {
		t.Fatal(err)
	}
	r := &poolRunner{hook: func(_ context.Context, n int, args []string) error {
		if n == 1 {
			out, stderr, code, err := (runner.Real{}).Run(context.Background(), bin, args, runner.RunOpts{})
			if err != nil || code != 0 {
				t.Errorf("FPM: %s %s (exit %d)", out, stderr, code)
				return errors.New("FPM validation failed")
			}
		}
		return nil
	}}
	a := PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	if err := a.ApplyCommitted(context.Background(), "demo", PoolState{}, PoolState{}); err != nil {
		t.Fatal(err)
	}
}

func testPoolDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pool.d")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "php-fpm.conf"), []byte("[global]\ninclude="+dir+"/*.conf\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPoolCommittedRecovery(t *testing.T) {
	ctx := context.Background()
	dir := testPoolDir(t)
	r := &poolRunner{}
	a := PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	old := PoolState{Exists: true, MaxChildren: 3}
	next := PoolState{Exists: true, MaxChildren: 4}
	absent := PoolState{}
	if err := a.Recover(ctx, "demo", old); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "demo.conf")
	original, _ := os.ReadFile(path)
	r.hook = func(_ context.Context, n int, args []string) error {
		if n == 3 {
			got, _ := os.ReadFile(path)
			if string(got) != string(original) {
				t.Fatal("candidate published before test")
			}
			return errors.New("validation failed")
		}
		return nil
	}
	if err := a.ApplyCommitted(ctx, "demo", next, old); err == nil {
		t.Fatal("expected validation failure")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Fatal("committed state not restored")
	}
	r.hook = nil
	if err := a.ApplyCommitted(ctx, "demo", next, old); err != nil {
		t.Fatal(err)
	}
	// Simulate crash after publish, before SQLite checkpoint.
	if err := a.Recover(ctx, "demo", old); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != string(original) {
		t.Fatal("replay did not restore committed state")
	}
	// Crash after checkpoint: recovery projects the new state, regardless of disk.
	if err := a.Recover(ctx, "demo", next); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if string(got) == string(original) {
		t.Fatal("post-checkpoint recovery stale")
	}
	if err := a.Recover(ctx, "demo", absent); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected absent pool: %v", err)
	}
	if _, err := os.Stat(path + ".lkg"); !os.IsNotExist(err) {
		t.Fatal("unexpected marker")
	}
}
func TestPoolRecoveryFailureRetries(t *testing.T) {
	ctx := context.Background()
	r := &poolRunner{}
	a := PoolApplier{Dir: testPoolDir(t), Runner: r, Probe: func(context.Context) error { return nil }}
	old := PoolState{Exists: true, MaxChildren: 3}
	next := PoolState{Exists: true, MaxChildren: 4}
	if err := a.Recover(ctx, "demo", old); err != nil {
		t.Fatal(err)
	}
	r.hook = func(ctx context.Context, n int, _ []string) error {
		if n >= 4 {
			return errors.New("reload failure")
		}
		return nil
	}
	if err := a.ApplyCommitted(ctx, "demo", next, old); !errors.Is(err, jobs.ErrRetry) {
		t.Fatalf("expected retry: %v", err)
	}
	r.hook = nil
	if err := a.Recover(ctx, "demo", old); err != nil {
		t.Fatal(err)
	}
}
func TestPoolRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "demo.conf")); err != nil {
		t.Fatal(err)
	}
	a := PoolApplier{Dir: dir, Runner: &poolRunner{}, Probe: func(context.Context) error { return nil }}
	if err := a.Recover(context.Background(), "demo", PoolState{Exists: true, MaxChildren: 3}); err == nil {
		t.Fatal("accepted symlink")
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "safe" {
		t.Fatal("modified outside file")
	}
}
