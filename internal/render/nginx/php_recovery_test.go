package nginx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	runner "novapanel/internal/exec"
	"novapanel/internal/render/php"
)

type oneFailureRunner struct {
	events        *[]string
	failure       string
	armed, failed *bool
	cancel        context.CancelFunc
}

func (r *oneFailureRunner) Run(ctx context.Context, prog string, args []string, o runner.RunOpts) ([]byte, []byte, int, error) {
	start := len(*r.events)
	out, stderr, code, err := (coordinatedRunner{events: r.events}).Run(ctx, prog, args, o)
	if *r.armed && !*r.failed && (*r.events)[start] == r.failure {
		*r.failed = true
		r.cancel()
		return nil, nil, 1, errors.New("injected")
	}
	return out, stderr, code, err
}

func TestPHPTransactionRecovery(t *testing.T) {
	for _, failure := range []string{"fpm.validate", "nginx.validate", "fpm.reload", "fpm.probe", "nginx.reload", "nginx.probe", "commit"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			pool := t.TempDir()
			for _, dir := range []string{"sites-available", "sites-enabled"} {
				if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			events := []string{}
			armed := false
			failed := false
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := func(kind string) error {
				events = append(events, kind)
				if armed && !failed && failure == kind {
					failed = true
					cancel()
					return errors.New("injected")
				}
				return nil
			}
			r := &oneFailureRunner{events: &events, failure: failure, armed: &armed, failed: &failed, cancel: cancel}
			p := &php.Applier{Root: pool, Runner: r, Probe: func(context.Context, php.Desired) error { return probe("fpm.probe") }}
			a := &Applier{Root: root, Runner: r, Probe: func(context.Context, Desired) error { return probe("nginx.probe") }}
			d := Desired{Name: "demo", Domains: []string{"demo.example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", PHP: &php.Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}}
			restoreProbe := func(c context.Context) error {
				if c.Err() != nil {
					t.Fatal("canceled recovery")
				}
				deadline, ok := c.Deadline()
				if !ok || time.Until(deadline) > 10*time.Second {
					t.Fatal("unbounded recovery")
				}
				events = append(events, "restore.probe")
				return nil
			}
			if err := a.ApplyPHP(ctx, p, d, func(context.Context) error { return nil }, restoreProbe); err != nil {
				t.Fatal(err)
			}
			files := []string{filepath.Join(pool, "demo.conf"), filepath.Join(root, "sites-available/demo.conf")}
			before := map[string]string{}
			for _, f := range files {
				b, err := os.ReadFile(f)
				if err != nil {
					t.Fatal(err)
				}
				before[f] = string(b)
			}
			events = nil
			armed = true
			d.PHP.Limits = &php.Limits{MemoryMB: 512, UploadMB: 64, MaxExecSec: 60}
			if err := a.ApplyPHP(ctx, p, d, func(context.Context) error { return probe("commit") }, restoreProbe); err == nil {
				t.Fatal("expected failure")
			}
			if !failed {
				t.Fatal("did not inject failure")
			}
			for _, f := range files {
				for _, suffix := range []string{"", ".lkg"} {
					b, err := os.ReadFile(f + suffix)
					if err != nil || string(b) != before[f] {
						t.Fatal("lost known good", f, suffix, err)
					}
				}
			}
			if failure == "fpm.validate" || failure == "nginx.validate" {
				for _, e := range events {
					if e == "fpm.reload" || e == "nginx.reload" {
						t.Fatal("reloaded invalid candidate", events)
					}
				}
			} else {
				want := []string{"fpm.validate", "nginx.validate", "fpm.reload", "nginx.reload", "restore.probe"}
				if len(events) < len(want) || !reflect.DeepEqual(events[len(events)-len(want):], want) {
					t.Fatal("recovery ordering", events)
				}
			}
		})
	}
}
