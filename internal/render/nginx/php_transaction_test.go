package nginx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	runner "novapanel/internal/exec"
	"novapanel/internal/render/php"
)

type coordinatedRunner struct {
	events *[]string
	fail   string
}

func (r coordinatedRunner) Run(ctx context.Context, prog string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	event := "nginx.validate"
	if strings.Contains(prog, "php-fpm") {
		event = "fpm.validate"
	}
	if len(args) > 0 && args[0] == "reload" {
		event = "nginx.reload"
		if args[1] == "php8.3-fpm" {
			event = "fpm.reload"
		}
	}
	*r.events = append(*r.events, event)
	if ctx.Err() != nil {
		return nil, nil, 1, ctx.Err()
	}
	if r.fail == event {
		return nil, nil, 1, errors.New("injected")
	}
	return nil, nil, 0, nil
}
func TestPHPTransactionOrder(t *testing.T) {
	root := t.TempDir()
	pool := t.TempDir()
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	events := []string{}
	p := &php.Applier{Root: pool, Runner: coordinatedRunner{events: &events}, Probe: func(context.Context, php.Desired) error { events = append(events, "fpm.probe"); return nil }}
	a := &Applier{Root: root, Runner: coordinatedRunner{events: &events}, Probe: func(context.Context, Desired) error { events = append(events, "nginx.probe"); return nil }}
	d := Desired{Name: "demo", Domains: []string{"demo.example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", PHP: &php.Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}}
	commit := func(context.Context) error { events = append(events, "commit"); return nil }
	restoreProbe := func(context.Context) error { events = append(events, "restore.probe"); return nil }
	if err := a.ApplyPHP(context.Background(), p, d, commit, restoreProbe); err != nil {
		t.Fatal(err)
	}
	want := []string{"fpm.validate", "nginx.validate", "fpm.reload", "fpm.probe", "nginx.reload", "nginx.probe", "commit"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events %v want %v", events, want)
	}
	for _, file := range []string{filepath.Join(pool, "demo.conf"), filepath.Join(root, "sites-available/demo.conf")} {
		live, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		good, err := os.ReadFile(file + ".lkg")
		if err != nil || string(good) != string(live) {
			t.Fatal("missing verified LKG", err)
		}
	}
}
