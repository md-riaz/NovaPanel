package nginx

import (
	"context"
	"errors"
	runner "novapanel/internal/exec"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type fakeRunner struct {
	calls [][]string
	fail  string
}

func (r *fakeRunner) Run(_ context.Context, p string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls = append(r.calls, append([]string{p}, args...))
	if args[0] == r.fail {
		return nil, nil, 1, nil
	}
	return nil, nil, 0, nil
}
func TestApplyRollbackAndNoop(t *testing.T) {
	for _, failure := range []string{"", "-t", "reload", "probe"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"sites-available", "sites-enabled"} {
				if e := os.Mkdir(filepath.Join(root, dir), 0755); e != nil {
					t.Fatal(e)
				}
			}
			r := &fakeRunner{}
			probes := 0
			a := Applier{Root: root, Runner: r, Probe: func(context.Context, Desired) error {
				probes++
				if failure == "probe" && probes > 1 {
					return errors.New("unhealthy")
				}
				return nil
			}}
			d := Desired{Name: "demo", Domains: []string{"example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo"}
			if e := a.Apply(context.Background(), d); e != nil {
				t.Fatal(e)
			}
			target := filepath.Join(root, "sites-available/demo.conf")
			old, e := os.ReadFile(target)
			if e != nil {
				t.Fatal(e)
			}
			if link, e := os.Readlink(filepath.Join(root, "sites-enabled/demo.conf")); e != nil || link != "../sites-available/demo.conf" {
				t.Fatalf("link %q %v", link, e)
			}
			if e := a.Apply(context.Background(), d); e != nil {
				t.Fatal(e)
			}
			if len(r.calls) != 2 || probes != 1 {
				t.Fatalf("noop calls=%v probes=%d", r.calls, probes)
			}
			// A rename before a crash is not evidence that reload and probe passed.
			uncommitted, err := Render(Desired{Name: d.Name, Domains: d.Domains, Docroot: "/srv/sites/demo/uncommitted", ACMERoot: d.ACMERoot})
			if err != nil {
				t.Fatal(err)
			}
			if e := os.WriteFile(target, uncommitted, 0644); e != nil {
				t.Fatal(e)
			}
			// A crash may leave a regular temporary file; retry must replace it.
			if e := os.WriteFile(target+".tmp", []byte("partial"), 0644); e != nil {
				t.Fatal(e)
			}
			r.fail = failure
			d.Docroot = "/srv/sites/demo/other"
			err = a.Apply(context.Background(), d)
			if (err != nil) != (failure != "") {
				t.Fatalf("failure=%s err=%v", failure, err)
			}
			b, e := os.ReadFile(target)
			if e != nil {
				t.Fatal(e)
			}
			if failure != "" && string(b) != string(old) {
				t.Fatal("did not restore previous config")
			}
			want := [][]string{{"/usr/sbin/nginx", "-t"}, {"/usr/bin/systemctl", "reload", "nginx"}, {"/usr/sbin/nginx", "-t"}}
			if failure != "-t" {
				want = append(want, []string{"/usr/bin/systemctl", "reload", "nginx"})
				if failure != "" {
					want = append(want, []string{"/usr/bin/systemctl", "reload", "nginx"})
				}
			}
			if !reflect.DeepEqual(r.calls, want) {
				t.Fatalf("calls %v want %v", r.calls, want)
			}
		})
	}
}
func TestApplyFirstFailureAndSymlinkRefusal(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if e := os.Mkdir(filepath.Join(root, dir), 0755); e != nil {
			t.Fatal(e)
		}
	}
	r := &fakeRunner{fail: "-t"}
	a := Applier{Root: root, Runner: r, Probe: func(context.Context, Desired) error { return nil }}
	d := Desired{Name: "demo", Domains: []string{"example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo"}
	if e := a.Apply(context.Background(), d); e == nil {
		t.Fatal("accepted invalid config")
	}
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if _, e := os.Lstat(filepath.Join(root, dir, "demo.conf")); !os.IsNotExist(e) {
			t.Fatalf("first config retained %v", e)
		}
	}
	outside := filepath.Join(t.TempDir(), "sentinel")
	if e := os.WriteFile(outside, []byte("safe"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(root, "sites-available/demo.conf.tmp")); e != nil {
		t.Fatal(e)
	}
	if e := a.Apply(context.Background(), d); e == nil {
		t.Fatal("accepted symlink temp")
	}
	b, e := os.ReadFile(outside)
	if e != nil || string(b) != "safe" {
		t.Fatal("outside touched")
	}
}
