package platform

import (
	"context"
	"fmt"
	runner "novapanel/internal/exec"
	"os"
	"path/filepath"
	"testing"
)

type accountRunner struct {
	home   string
	exists bool
	calls  []string
}

func (r *accountRunner) Run(_ context.Context, p string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls = append(r.calls, p)
	switch p {
	case "/usr/bin/getent":
		if args[0] == "group" {
			return []byte("www-data:x:33:\n"), nil, 0, nil
		}
		if !r.exists {
			return nil, nil, 2, nil
		}
		return []byte(fmt.Sprintf("demo:x:%d:%d::%s:/usr/sbin/nologin\n", 12345, 12345, r.home)), nil, 0, nil
	case "/usr/sbin/useradd":
		r.exists = true
		return nil, nil, 0, nil
	}
	return nil, nil, 1, fmt.Errorf("unexpected program %s", p)
}
func TestEnsureSite(t *testing.T) {
	root := t.TempDir()
	sites := filepath.Join(root, "sites")
	acme := filepath.Join(root, "acme")
	for _, p := range []string{sites, acme} {
		if e := os.Mkdir(p, 0755); e != nil {
			t.Fatal(e)
		}
	}
	r := &accountRunner{home: filepath.Join(sites, "demo")}
	if os.Getuid() != 0 {
		t.Skip("scratch ownership test requires root")
	}
	e := EnsureSite(context.Background(), r, sites, acme, "demo")
	if e != nil {
		t.Fatal(e)
	}
	if len(r.calls) == 0 || r.calls[1] != "/usr/sbin/useradd" {
		t.Fatal(r.calls)
	}
	if e := EnsureSite(context.Background(), r, sites, acme, "demo"); e != nil {
		t.Fatal(e)
	}
	creates := 0
	for _, p := range r.calls {
		if p == "/usr/sbin/useradd" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatal(r.calls)
	}
	for _, p := range []string{r.home, filepath.Join(r.home, "public"), filepath.Join(acme, "demo")} {
		if _, e := os.Stat(p); e != nil {
			t.Fatal(e)
		}
	}
}
func TestEnsureSiteRejectsHostileSink(t *testing.T) {
	root := t.TempDir()
	r := &accountRunner{}
	for _, name := range []string{"root", "bad;name", "../demo"} {
		if e := EnsureSite(context.Background(), r, root, root, name); e == nil {
			t.Fatal(name)
		}
	}
	if e := os.Symlink(t.TempDir(), filepath.Join(root, "demo")); e != nil {
		t.Fatal(e)
	}
	if e := EnsureSite(context.Background(), r, root, root, "demo"); e == nil {
		t.Fatal("accepted symlink")
	}
	if len(r.calls) != 0 {
		t.Fatal(r.calls)
	}
}
