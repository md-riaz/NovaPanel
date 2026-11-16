package nginx

import (
	"context"
	"novapanel/internal/render/php"
	"os"
	"path/filepath"
	"testing"
)

func TestPHPTransactionProvenUnchanged(t *testing.T) {
	root, pool := t.TempDir(), t.TempDir()
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	events := []string{}
	p := &php.Applier{Root: pool, Runner: coordinatedRunner{events: &events}, Probe: func(context.Context, php.Desired) error { events = append(events, "fpm.probe"); return nil }}
	a := &Applier{Root: root, Runner: coordinatedRunner{events: &events}, Probe: func(context.Context, Desired) error { events = append(events, "nginx.probe"); return nil }}
	d := Desired{Name: "demo", Domains: []string{"demo.example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", PHP: &php.Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}}
	commits := 0
	commit := func(context.Context) error { commits++; return nil }
	restored := func(context.Context) error { t.Fatal("unexpected recovery"); return nil }
	if err := a.ApplyPHP(context.Background(), p, d, commit, restored); err != nil {
		t.Fatal(err)
	}
	files := []string{filepath.Join(pool, "demo.conf"), filepath.Join(pool, "demo.conf.lkg"), filepath.Join(root, "sites-available/demo.conf"), filepath.Join(root, "sites-available/demo.conf.lkg")}
	before := make([]os.FileInfo, len(files))
	for i, f := range files {
		var err error
		before[i], err = os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
	}
	events = nil
	if err := a.ApplyPHP(context.Background(), p, d, commit, restored); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 || commits != 2 {
		t.Fatalf("unchanged effects %v commits %d", events, commits)
	}
	for i, f := range files {
		after, err := os.Stat(f)
		if err != nil || !os.SameFile(before[i], after) {
			t.Fatal("unchanged file rewritten", f, err)
		}
	}
}
