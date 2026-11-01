package nginx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestApplyTLSRevalidationAndForcedReload(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	d := Desired{Name: "demo", Domains: []string{"demo.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", TLS: installedTLS(t, false)}
	runner := &fakeRunner{}
	a := Applier{Root: root, Runner: runner, Probe: func(context.Context, Desired) error { return nil }, Now: func() time.Time { return tlsNow }}
	if err := a.Apply(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	d.ForceReload = true
	if err := a.Apply(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("cert-only reload skipped: %v", runner.calls)
	}
	a.Now = func() time.Time { return tlsNow.Add(2 * time.Hour) }
	if err := a.Apply(context.Background(), d); err == nil {
		t.Fatal("expired snapshot accepted")
	}
	if len(runner.calls) != 4 {
		t.Fatal("expired cert reached runner")
	}
}
