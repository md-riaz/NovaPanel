package php

import (
	"context"
	"errors"
	runner "novapanel/internal/exec"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type applyRunner struct {
	calls []string
	fail  int
}

func (r *applyRunner) Run(_ context.Context, prog string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls = append(r.calls, prog+" "+strings.Join(args, " "))
	if len(r.calls) == r.fail {
		return nil, nil, 1, errors.New("injected")
	}
	return nil, nil, 0, nil
}
func TestPoolPublication(t *testing.T) {
	for _, fail := range []int{0, 1, 2, 3} {
		t.Run(string(rune('a'+fail)), func(t *testing.T) {
			root := t.TempDir()
			r := &applyRunner{}
			probes := 0
			a := &Applier{Root: root, Runner: r, Probe: func(context.Context, Desired) error {
				probes++
				if fail == 3 && probes == 2 {
					return errors.New("probe")
				}
				return nil
			}}
			d := Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}
			if err := a.Apply(context.Background(), d); err != nil {
				t.Fatal(err)
			}
			original, _ := os.ReadFile(filepath.Join(root, "demo.conf.lkg"))
			r.calls = nil
			r.fail = fail
			d.Limits = &Limits{MemoryMB: 512, UploadMB: 64, MaxExecSec: 60}
			err := a.Apply(context.Background(), d)
			if (err != nil) != (fail != 0) {
				t.Fatalf("fail=%d err=%v", fail, err)
			}
			live, _ := os.ReadFile(filepath.Join(root, "demo.conf"))
			lkg, _ := os.ReadFile(filepath.Join(root, "demo.conf.lkg"))
			if fail != 0 && (string(live) != string(original) || string(lkg) != string(original)) {
				t.Fatal("lost original known good")
			}
			if fail == 0 && (!strings.Contains(string(live), "512M") || string(live) != string(lkg)) {
				t.Fatal("not published")
			}
			if !strings.HasPrefix(r.calls[0], "/usr/sbin/php-fpm8.3 -t --fpm-config "+filepath.Join(root, "novapanel-validate.ini")) {
				t.Fatal(r.calls)
			}
		})
	}
}
func TestPoolPublicationRefusesSymlinks(t *testing.T) {
	for _, leaf := range []string{"demo.conf", "demo.conf.lkg", "demo.conf.tmp", "novapanel-validate.ini", "novapanel-validate.ini.tmp"} {
		t.Run(leaf, func(t *testing.T) {
			root := t.TempDir()
			victim := filepath.Join(t.TempDir(), "victim")
			os.WriteFile(victim, []byte("untouched"), 0600)
			os.Symlink(victim, filepath.Join(root, leaf))
			r := &applyRunner{}
			a := &Applier{Root: root, Runner: r, Probe: func(context.Context, Desired) error { return nil }}
			if err := a.Apply(context.Background(), Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}); err == nil {
				t.Fatal("accepted symlink")
			}
			b, _ := os.ReadFile(victim)
			if string(b) != "untouched" || len(r.calls) != 0 {
				t.Fatal("side effect")
			}
		})
	}
}
func TestPoolPublicationCrashRetry(t *testing.T) {
	root := t.TempDir()
	r := &applyRunner{}
	a := &Applier{Root: root, Runner: r, Probe: func(context.Context, Desired) error { return nil }}
	d := Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}
	if err := a.Apply(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(root, "demo.conf.lkg"))
	d.Limits = &Limits{MemoryMB: 512, UploadMB: 64, MaxExecSec: 60}
	b, _ := Render(d)
	os.WriteFile(filepath.Join(root, "demo.conf"), b, 0644)
	r.calls = nil
	r.fail = 2
	if err := a.Apply(context.Background(), d); err == nil {
		t.Fatal("expected failed reload")
	}
	live, _ := os.ReadFile(filepath.Join(root, "demo.conf"))
	if string(live) != string(original) {
		t.Fatal("promoted unprobed bytes")
	}
}
