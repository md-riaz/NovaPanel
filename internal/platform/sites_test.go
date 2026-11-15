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
	gecos  string
	calls  []string
}

func (r *accountRunner) Run(_ context.Context, p string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls = append(r.calls, p)
	switch p {
	case "/usr/bin/getent":
		if args[0] == "group" {
			return []byte(args[1] + ":x:33:\n"), nil, 0, nil
		}
		if !r.exists {
			return nil, nil, 2, nil
		}
		return []byte(fmt.Sprintf("demo:x:%d:%d:%s:%s:/usr/sbin/nologin\n", 12345, 12345, r.gecos, r.home)), nil, 0, nil
	case "/usr/sbin/useradd":
		r.exists = true
		r.gecos = "novapanel-site=" + siteID
		return nil, nil, 0, nil
	}
	return nil, nil, 1, fmt.Errorf("unexpected program %s", p)
}

const siteID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

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
	e := EnsureSite(context.Background(), r, sites, acme, acme, "demo", siteID)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.calls) == 0 || r.calls[2] != "/usr/sbin/useradd" {
		t.Fatal(r.calls)
	}
	if e := EnsureSite(context.Background(), r, sites, acme, acme, "demo", siteID); e != nil {
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
	for _, p := range []string{r.home, filepath.Join(r.home, "public"), filepath.Join(acme, "demo", "php-error.log")} {
		if _, e := os.Stat(p); e != nil {
			t.Fatal(e)
		}
	}
}
func TestEnsureSiteRejectsUnownedNonemptyDirectories(t *testing.T) {
	for _, tree := range []string{"sites", "logs"} {
		t.Run(tree, func(t *testing.T) {
			root := t.TempDir()
			sites, acme, logs := filepath.Join(root, "sites"), filepath.Join(root, "acme"), filepath.Join(root, "logs")
			for _, p := range []string{sites, acme, logs} {
				if err := os.Mkdir(p, 0755); err != nil {
					t.Fatal(err)
				}
			}
			dir := filepath.Join(root, tree, "demo")
			if err := os.Mkdir(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "foreign"), []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			r := &accountRunner{home: filepath.Join(sites, "demo")}
			if err := EnsureSite(context.Background(), r, sites, acme, logs, "demo", siteID); err == nil {
				t.Fatal("adopted foreign directory")
			}
			if r.exists {
				t.Fatal("created account for foreign content")
			}
			if _, err := os.Stat(filepath.Join(dir, "foreign")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEnsureSiteIgnoresACMETree(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("scratch ownership test requires root")
	}
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			root := t.TempDir()
			sites, logs, acme := filepath.Join(root, "sites"), filepath.Join(root, "logs"), filepath.Join(root, "acme")
			for _, p := range []string{sites, logs} {
				if err := os.Mkdir(p, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if existing {
				if err := os.MkdirAll(filepath.Join(acme, "demo"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(acme, "demo", "foreign"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r := &accountRunner{home: filepath.Join(sites, "demo")}
			for i := 0; i < 2; i++ {
				if err := EnsureSite(context.Background(), r, sites, acme, logs, "demo", siteID); err != nil {
					t.Fatal(err)
				}
			}
			if existing {
				b, err := os.ReadFile(filepath.Join(acme, "demo", "foreign"))
				if err != nil || string(b) != "keep" {
					t.Fatalf("ACME content changed: %q %v", b, err)
				}
				info, err := os.Stat(filepath.Join(acme, "demo"))
				if err != nil || info.Mode().Perm() != 0700 {
					t.Fatalf("ACME permissions changed: %v %v", info, err)
				}
			} else if _, err := os.Lstat(acme); !os.IsNotExist(err) {
				t.Fatalf("ACME root touched: %v", err)
			}
		})
	}
}

func TestEnsureSiteRejectsHostileSink(t *testing.T) {
	root := t.TempDir()
	r := &accountRunner{}
	for _, name := range []string{"root", "bad;name", "../demo"} {
		if e := EnsureSite(context.Background(), r, root, root, root, name, siteID); e == nil {
			t.Fatal(name)
		}
	}
	if e := os.Symlink(t.TempDir(), filepath.Join(root, "demo")); e != nil {
		t.Fatal(e)
	}
	if e := EnsureSite(context.Background(), r, root, root, root, "demo", siteID); e == nil {
		t.Fatal("accepted symlink")
	}
	if len(r.calls) != 0 {
		t.Fatal(r.calls)
	}
}

func TestEnsureSiteRejectsUnmarkedAccount(t *testing.T) {
	root := t.TempDir()
	r := &accountRunner{home: filepath.Join(root, "demo"), exists: true}
	if err := EnsureSite(context.Background(), r, root, root, root, "demo", siteID); err == nil {
		t.Fatal("accepted unmarked account")
	}
	if _, err := os.Stat(r.home); !os.IsNotExist(err) {
		t.Fatal("modified home", err)
	}
}
