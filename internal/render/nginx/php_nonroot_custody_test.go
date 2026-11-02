package nginx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"novapanel/internal/render/php"
)

// This is a refusal test, not an unprivileged substitute for root recovery.
func TestPHPDurableRefusesNonRootJournalBeforePublication(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires a non-root caller")
	}
	root := t.TempDir()
	for _, dir := range []string{"nginx/sites-available", "nginx/sites-enabled", "pool", "journal"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	events := []string{}
	runner := coordinatedRunner{events: &events}
	pool := &php.Applier{Root: filepath.Join(root, "pool"), Runner: runner, Probe: func(context.Context, php.Desired) error { return nil }}
	app := &Applier{Root: filepath.Join(root, "nginx"), Runner: runner, Probe: func(context.Context, Desired) error { return nil }}
	d := Desired{Name: "demo", Domains: []string{"demo.example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", PHP: &php.Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}}
	committed := false
	err := app.ApplyPHPDurable(context.Background(), pool, d, PHPDurableBinding{Root: filepath.Join(root, "journal"), JobID: "job1", SiteID: "site1", Check: func(context.Context) error { return nil }}, func(context.Context) error { committed = true; return nil }, func(context.Context) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "invalid recovery custody") {
		t.Fatalf("expected custody refusal, got %v", err)
	}
	if committed || len(events) != 0 {
		t.Fatalf("effect occurred after custody refusal: commit=%v events=%v", committed, events)
	}
	for _, dir := range []string{"nginx/sites-available", "nginx/sites-enabled", "pool", "journal"} {
		entries, e := os.ReadDir(filepath.Join(root, dir))
		if e != nil || len(entries) != 0 {
			t.Fatalf("unexpected content in %s: %v %v", dir, entries, e)
		}
	}
}
