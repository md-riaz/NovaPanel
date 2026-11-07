package php

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	runner "novapanel/internal/exec"
	"novapanel/internal/store"
)

type poolRunner struct {
	calls int
	fail  int
}

func (r *poolRunner) Run(_ context.Context, _ string, _ []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls++
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
