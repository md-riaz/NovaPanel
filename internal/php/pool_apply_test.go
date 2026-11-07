package php

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/store"
)

type poolRunner struct {
	calls int
	fail  int
	hook  func(context.Context, int) error
}

func (r *poolRunner) Run(ctx context.Context, _ string, _ []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls++
	if r.hook != nil {
		if err := r.hook(ctx, r.calls); err != nil {
			return nil, nil, 0, err
		}
	}
	if r.calls == r.fail {
		return nil, nil, 1, nil
	}
	return nil, nil, 0, nil
}
func TestPoolApplyRollbackAndNoop(t *testing.T) {
	dir := t.TempDir()
	r := &poolRunner{}
	a := PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	if err := a.Apply(context.Background(), "demo", 3); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Join(dir, "demo.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Apply(context.Background(), "demo", 3); err != nil || r.calls != 2 {
		t.Fatalf("noop: calls=%d err=%v", r.calls, err)
	}
	r.fail = 3
	if err = a.Apply(context.Background(), "demo", 4); err == nil {
		t.Fatal("expected validation failure")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "demo.conf"))
	if string(got) != string(old) {
		t.Fatal("failed validation did not restore")
	}
	r.fail = 0
	a.Probe = func(context.Context) error { return errors.New("unhealthy") }
	if err = a.Remove(context.Background(), "demo"); err == nil {
		t.Fatal("expected probe failure")
	}
	got, _ = os.ReadFile(filepath.Join(dir, "demo.conf"))
	if string(got) != string(old) {
		t.Fatal("failed removal did not restore")
	}
}
func TestPoolRollbackFailuresRetry(t *testing.T) {
	for _, stage := range []string{"test-restore", "reload-restore", "probe-restore", "reload-recovery", "probe-recovery", "cancel-recovery"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			r := &poolRunner{}
			a := PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
			if err := a.Apply(context.Background(), "demo", 3); err != nil {
				t.Fatal(err)
			}
			old, err := os.ReadFile(filepath.Join(dir, "demo.conf.lkg"))
			if err != nil {
				t.Fatal(err)
			}
			block := filepath.Join(dir, "demo.conf.tmp")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			inject := func() error {
				if err := os.Mkdir(block, 0700); err != nil {
					return err
				}
				return errors.New("injected failure")
			}
			r.hook = func(c context.Context, n int) error {
				switch {
				case stage == "test-restore" && n == 3, stage == "reload-restore" && n == 4:
					return inject()
				case stage == "reload-recovery" && (n == 4 || n == 5), stage == "probe-recovery" && n == 5:
					return errors.New("injected command failure")
				case stage == "cancel-recovery" && n == 4:
					cancel()
					return context.Canceled
				case stage == "cancel-recovery" && n == 5:
					if c.Err() != nil {
						t.Fatal("recovery context canceled")
					}
					return errors.New("recovery failure")
				}
				return nil
			}
			if stage == "probe-restore" {
				a.Probe = func(context.Context) error { return inject() }
			}
			if stage == "probe-recovery" {
				a.Probe = func(context.Context) error { return errors.New("probe failure") }
			}
			err = a.Apply(ctx, "demo", 4)
			if !errors.Is(err, jobs.ErrRetry) {
				t.Fatalf("expected retry, got %v", err)
			}
			if stage == "reload-recovery" || stage == "probe-recovery" || stage == "cancel-recovery" {
				got, e := os.ReadFile(filepath.Join(dir, "demo.conf"))
				if e != nil || string(got) != string(old) {
					t.Fatalf("restore: %v", e)
				}
			}
			if stage == "test-restore" || stage == "reload-restore" || stage == "probe-restore" {
				if err := os.Remove(block); err != nil {
					t.Fatal(err)
				}
			}
			r.hook = nil
			a.Probe = func(context.Context) error { return nil }
			if err := a.Apply(context.Background(), "demo", 4); err != nil {
				t.Fatal(err)
			}
			got, e := os.ReadFile(filepath.Join(dir, "demo.conf"))
			if e != nil {
				t.Fatal(e)
			}
			good, e := os.ReadFile(filepath.Join(dir, "demo.conf.lkg"))
			if e != nil || string(good) != string(got) || string(got) == string(old) {
				t.Fatalf("retry did not converge: %v", e)
			}
		})
	}
}

func TestPoolRemoveMarkerCleanupRetry(t *testing.T) {
	for _, stage := range []string{"remove", "sync"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			r := &poolRunner{}
			a := PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
			ctx := context.Background()
			if err := a.Apply(ctx, "demo", 3); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("cleanup failure")
			if stage == "remove" {
				a.removeMarker = func(*os.Root, string) error { return injected }
			} else {
				a.syncDir = func(*os.Root) error { return injected }
			}
			err := a.Remove(ctx, "demo")
			if !errors.Is(err, injected) || !errors.Is(err, jobs.ErrRetry) {
				t.Fatalf("expected retry: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "demo.conf")); !os.IsNotExist(err) {
				t.Fatalf("pool still exists: %v", err)
			}
			a.removeMarker, a.syncDir = nil, nil
			if err := a.Remove(ctx, "demo"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "demo.conf.lkg")); !os.IsNotExist(err) {
				t.Fatalf("marker still exists: %v", err)
			}
		})
	}
}

func TestPoolApplyLimits(t *testing.T) {
	dir := t.TempDir()
	r := &poolRunner{}
	a := PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	n := 128
	limits := store.PHPLimits{MemoryMB: &n}
	if err := a.ApplyLimits(context.Background(), "demo", 3, limits); err != nil {
		t.Fatal(err)
	}
	if err := a.ApplyLimits(context.Background(), "demo", 3, limits); err != nil || r.calls != 2 {
		t.Fatalf("noop: calls=%d err=%v", r.calls, err)
	}
	old, err := os.ReadFile(filepath.Join(dir, "demo.conf"))
	if err != nil {
		t.Fatal(err)
	}
	n = 0
	if err := a.ApplyLimits(context.Background(), "demo", 3, limits); err == nil || r.calls != 2 {
		t.Fatal("invalid limit had side effects")
	}
	got, err := os.ReadFile(filepath.Join(dir, "demo.conf"))
	if err != nil || string(got) != string(old) {
		t.Fatal("invalid limit changed pool")
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
	if err := a.Apply(context.Background(), "demo", 3); err == nil {
		t.Fatal("accepted symlink")
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "safe" {
		t.Fatal("modified outside file")
	}
}

func TestPoolLKGRecoveryFailureRequestsRetry(t *testing.T) {
	dir := t.TempDir()
	r := &poolRunner{fail: 5}
	a := PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	if err := a.Apply(context.Background(), "demo", 3); err != nil {
		t.Fatal(err)
	}
	block := filepath.Join(dir, "demo.conf.lkg.tmp")
	a.Probe = func(context.Context) error { return os.Mkdir(block, 0700) }
	if err := a.Apply(context.Background(), "demo", 4); !errors.Is(err, jobs.ErrRetry) {
		t.Fatalf("expected retry on failed recovery: %v", err)
	}
}

func TestPoolLKGWriteFailureRestoresAndRetry(t *testing.T) {
	dir := t.TempDir()
	r := &poolRunner{}
	a := PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	ctx := context.Background()
	if err := a.Apply(ctx, "demo", 3); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Join(dir, "demo.conf"))
	if err != nil {
		t.Fatal(err)
	}
	block := filepath.Join(dir, "demo.conf.lkg.tmp")
	a.Probe = func(context.Context) error { return os.Mkdir(block, 0700) }
	if err := a.Apply(ctx, "demo", 4); err == nil {
		t.Fatal("expected LKG failure")
	}
	got, err := os.ReadFile(filepath.Join(dir, "demo.conf"))
	if err != nil || string(got) != string(old) {
		t.Fatalf("config not restored: %v", err)
	}
	good, err := os.ReadFile(filepath.Join(dir, "demo.conf.lkg"))
	if err != nil || string(good) != string(old) {
		t.Fatalf("LKG changed: %v", err)
	}
	if r.calls != 5 {
		t.Fatalf("expected recovery reload, calls=%d", r.calls)
	}
	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	a.Probe = func(context.Context) error { return nil }
	if err := a.Apply(ctx, "demo", 4); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(dir, "demo.conf"))
	if err != nil || string(got) == string(old) {
		t.Fatalf("retry did not apply: %v", err)
	}
	good, err = os.ReadFile(filepath.Join(dir, "demo.conf.lkg"))
	if err != nil || string(good) != string(got) {
		t.Fatalf("retry LKG mismatch: %v", err)
	}
}
