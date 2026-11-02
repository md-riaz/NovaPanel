package nginx

import (
	"context"
	"errors"
	runnerexec "novapanel/internal/exec"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"novapanel/internal/render/php"
)

func TestPHPDurableReopenRestoresOriginalAfterDeath(t *testing.T) {
	root := os.Getenv("DURABLE_CRASH_ROOT")
	if root == "" {
		for _, point := range []string{"first", "second"} {
			t.Run(point, func(t *testing.T) { testPHPDurableCrash(t, point) })
		}
		return
	}
	testPHPDurableCrash(t, os.Getenv("DURABLE_CRASH_POINT"))
}

func testPHPDurableCrash(t *testing.T, point string) {
	root := os.Getenv("DURABLE_CRASH_ROOT")
	if root == "" {
		root = t.TempDir()
		for _, dir := range []string{"nginx/sites-available", "nginx/sites-enabled", "pool", "journal"} {
			if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
				t.Fatal(err)
			}
		}
		stdout, stderr, exit, err := (runnerexec.Real{}).Run(context.Background(), os.Args[0], []string{"-test.run=^TestPHPDurableReopenRestoresOriginalAfterDeath$"}, runnerexec.RunOpts{Env: append(os.Environ(), "DURABLE_CRASH_ROOT="+root, "DURABLE_CRASH_POINT="+point), MaxOutput: 1 << 20})
		if exit != 42 {
			t.Fatalf("child: exit=%d err=%v stdout=%s stderr=%s", exit, err, stdout, stderr)
		}
	}
	events := []string{}
	runner := coordinatedRunner{events: &events}
	p := &php.Applier{Root: filepath.Join(root, "pool"), Runner: runner, Probe: func(context.Context, php.Desired) error { events = append(events, "fpm.probe"); return nil }}
	a := &Applier{Root: filepath.Join(root, "nginx"), Runner: runner, Probe: func(context.Context, Desired) error { events = append(events, "nginx.probe"); return nil }}
	limits := php.Limits{MemoryMB: 512, UploadMB: 64, MaxExecSec: 60}
	d := Desired{Name: "demo", Domains: []string{"demo.example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", PHP: &php.Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001, Limits: &limits}}
	binding := PHPDurableBinding{Root: filepath.Join(root, "journal"), JobID: "job1", SiteID: "site1", Check: func(context.Context) error { return nil }}
	paths := []string{"pool/demo.conf", "pool/demo.conf.lkg", "nginx/sites-available/demo.conf", "nginx/sites-available/demo.conf.lkg"}
	if os.Getenv("DURABLE_CRASH_ROOT") != "" {
		old := d
		phpOld := *d.PHP
		phpOld.Limits = nil
		old.PHP = &phpOld
		if err := a.ApplyPHP(context.Background(), p, old, func(context.Context) error { return nil }, func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
		for i, path := range paths {
			b, err := os.ReadFile(filepath.Join(root, path))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, "original-"+string(rune('0'+i))), b, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if point == "first" {
			binding.Check = func(context.Context) error {
				before, e1 := os.ReadFile(filepath.Join(root, "original-1"))
				now, e2 := os.ReadFile(filepath.Join(root, "pool/demo.conf.lkg"))
				if e1 == nil && e2 == nil && !reflect.DeepEqual(before, now) {
					os.Exit(42)
				}
				return nil
			}
		}
		if err := a.ApplyPHPDurable(context.Background(), p, d, binding, func(context.Context) error { os.Exit(42); return nil }, func(context.Context) error { return nil }); err != nil {
			t.Fatalf("commit not reached: %v", err)
		}
		t.Fatal("commit not reached")
	}
	committed := false
	err := a.ApplyPHPDurable(context.Background(), p, d, binding, func(context.Context) error { committed = true; return nil }, func(context.Context) error { events = append(events, "old.probe"); return nil })
	if err == nil || committed {
		t.Fatalf("recovery err=%v commit=%v", err, committed)
	}
	for i, path := range paths {
		got, e := os.ReadFile(filepath.Join(root, path))
		if e != nil {
			t.Fatal(e)
		}
		want, e := os.ReadFile(filepath.Join(root, "original-"+string(rune('0'+i))))
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s differs", path)
		}
	}
	if len(events) != 5 || events[len(events)-1] != "old.probe" {
		t.Fatalf("events: %v", events)
	}
}

func TestPHPDurableRejectsExpiredOrUnsafeTargets(t *testing.T) {
	for _, tc := range []string{"expired", "symlink", "hardlink", "replaced-dir"} {
		t.Run(tc, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"nginx/sites-available", "nginx/sites-enabled", "pool", "journal"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			events := []string{}
			runner := coordinatedRunner{events: &events}
			p := &php.Applier{Root: filepath.Join(root, "pool"), Runner: runner, Probe: func(context.Context, php.Desired) error { return nil }}
			a := &Applier{Root: filepath.Join(root, "nginx"), Runner: runner, Probe: func(context.Context, Desired) error { return nil }}
			d := Desired{Name: "demo", Domains: []string{"demo.example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", PHP: &php.Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}}
			file := filepath.Join(root, "pool/demo.conf")
			if tc == "symlink" {
				if err := os.Symlink("/etc/passwd", file); err != nil {
					t.Fatal(err)
				}
			}
			if tc == "hardlink" {
				other := filepath.Join(root, "other")
				if err := os.WriteFile(other, []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(other, file); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			check := func(context.Context) error {
				calls++
				if tc == "expired" {
					return errors.New("expired")
				}
				if tc == "replaced-dir" && calls == 2 {
					path := filepath.Join(root, "nginx/sites-available")
					if err := os.Rename(path, path+"-old"); err != nil {
						return err
					}
					if err := os.Mkdir(path, 0700); err != nil {
						return err
					}
				}
				return nil
			}
			binding := PHPDurableBinding{Root: filepath.Join(root, "journal"), JobID: "job1", SiteID: "site1", Check: check}
			committed := false
			err := a.ApplyPHPDurable(context.Background(), p, d, binding, func(context.Context) error { committed = true; return nil }, func(context.Context) error { return nil })
			if err == nil || committed || len(events) != 0 {
				t.Fatalf("err=%v committed=%v effects=%v", err, committed, events)
			}
		})
	}
}
