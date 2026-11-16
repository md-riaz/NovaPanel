package nginx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"novapanel/internal/render/php"
)

func TestPHPTransactionFirstCreateAndRecoveryFailure(t *testing.T) {
	for _, recoverFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "restore_absence", true: "restore_validation_fails"}[recoverFails], func(t *testing.T) {
			root := t.TempDir()
			pool := t.TempDir()
			for _, dir := range []string{"sites-available", "sites-enabled"} {
				if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			events := []string{}
			p := &php.Applier{Root: pool, Runner: coordinatedRunner{events: &events}, Probe: func(context.Context, php.Desired) error { return nil }}
			a := &Applier{Root: root, Runner: coordinatedRunner{events: &events}, Probe: func(context.Context, Desired) error { return nil }}
			d := Desired{Name: "demo", Domains: []string{"demo.example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", PHP: &php.Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}}
			probed := false
			err := a.ApplyPHP(context.Background(), p, d, func(context.Context) error {
				events = nil
				if recoverFails {
					p.Runner = coordinatedRunner{events: &events, fail: "fpm.validate"}
				}
				return errors.New("metadata refused")
			}, func(context.Context) error { probed = true; return nil })
			if err == nil {
				t.Fatal("expected metadata failure")
			}
			for _, f := range []string{filepath.Join(pool, "demo.conf"), filepath.Join(pool, "demo.conf.lkg"), filepath.Join(root, "sites-available/demo.conf"), filepath.Join(root, "sites-available/demo.conf.lkg"), filepath.Join(root, "sites-enabled/demo.conf")} {
				if _, err := os.Lstat(f); !os.IsNotExist(err) {
					t.Fatal("first-create file survived", f, err)
				}
			}
			if probed {
				t.Fatal("restored service probe mismatch", events)
			}
			if recoverFails && (len(events) != 1 || events[0] != "fpm.validate") {
				t.Fatal("reloaded after invalid recovery", events)
			}
		})
	}
}
