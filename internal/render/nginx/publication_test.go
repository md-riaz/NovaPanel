package nginx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPublicationRecoveryOrder(t *testing.T) {
	for _, failure := range []string{"-t", "reload", "probe", "cancel", "commit"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"sites-available", "sites-enabled"} {
				if err := os.Mkdir(filepath.Join(root, dir), 0755); err != nil {
					t.Fatal(err)
				}
			}
			r := &fakeRunner{}
			a := &Applier{Root: root, Runner: r, Probe: func(context.Context, Desired) error { return nil }}
			d := Desired{Name: "demo", Domains: []string{"example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo"}
			if err := a.Apply(context.Background(), d); err != nil {
				t.Fatal(err)
			}
			old, _ := os.ReadFile(filepath.Join(root, "sites-available/demo.conf"))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pointer := "old"
			err := a.Transaction(ctx, func(tx *Transaction) error {
				snap, e := tx.Snapshot(d.Name)
				if e != nil {
					return e
				}
				pointer = "new"
				r.fail = failure
				a.Probe = func(context.Context, Desired) error {
					if failure == "cancel" {
						cancel()
						return ctx.Err()
					}
					if failure == "probe" {
						return errors.New("probe")
					}
					return nil
				}
				d.Docroot = "/srv/sites/demo/changed"
				return tx.Apply(ctx, d, func(recovery context.Context, reload bool) error {
					if recovery.Err() != nil {
						t.Fatal("cancelled recovery")
					}
					current, _ := os.ReadFile(filepath.Join(root, "sites-available/demo.conf"))
					if string(current) == string(old) {
						t.Fatal("config restored before pointer")
					}
					pointer = "old"
					return tx.Restore(recovery, snap, reload)
				}, func(context.Context) error {
					if failure == "commit" {
						return errors.New("commit")
					}
					return nil
				})
			})
			if err == nil || pointer != "old" {
				t.Fatalf("err=%v pointer=%s", err, pointer)
			}
			got, _ := os.ReadFile(filepath.Join(root, "sites-available/demo.conf"))
			if string(got) != string(old) {
				t.Fatal("config not restored")
			}
		})
	}
}
