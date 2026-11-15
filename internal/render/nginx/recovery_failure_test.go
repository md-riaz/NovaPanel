package nginx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryFailureDoesNotReload(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	r := &fakeRunner{}
	a := &Applier{Root: root, Runner: r, Probe: func(context.Context, Desired) error { return errors.New("probe") }}
	d := Desired{Name: "demo", Domains: []string{"example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo"}
	err := a.Transaction(context.Background(), func(tx *Transaction) error {
		return tx.Apply(context.Background(), d, func(context.Context, bool) error { return errors.New("pointer restore failed") }, nil)
	})
	if err == nil || len(r.calls) != 2 {
		t.Fatalf("err=%v calls=%v", err, r.calls)
	}
	if _, err = os.Stat(filepath.Join(root, "sites-available/demo.conf.lkg")); !os.IsNotExist(err) {
		t.Fatal("failed operation promoted LKG")
	}
}
