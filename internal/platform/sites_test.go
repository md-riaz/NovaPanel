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
	home                       string
	exists                     bool
	calls                      []string
	uid, gid                   int
	webUID, webGID, webGroup   int
	groupCalls, secondWebGroup int
	createHome                 bool
}

func (r *accountRunner) Run(_ context.Context, p string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls = append(r.calls, p)
	switch p {
	case "/usr/bin/getent":
		if args[0] == "group" {
			r.groupCalls++
			gid := r.webGroup
			if r.groupCalls > 1 && r.secondWebGroup != 0 {
				gid = r.secondWebGroup
			}
			if gid == 0 {
				gid = 33
			}
			return []byte(fmt.Sprintf("www-data:x:%d:\n", gid)), nil, 0, nil
		}
		if args[1] == "www-data" {
			uid, gid := r.webUID, r.webGID
			if uid == 0 {
				uid = 33
			}
			if gid == 0 {
				gid = 33
			}
			return []byte(fmt.Sprintf("www-data:x:%d:%d::/var/www:/usr/sbin/nologin\n", uid, gid)), nil, 0, nil
		}
		if !r.exists {
			return nil, nil, 2, nil
		}
		uid, gid := r.uid, r.gid
		if uid == 0 {
			uid = 12345
		}
		if gid == 0 {
			gid = 12345
		}
		return []byte(fmt.Sprintf("demo:x:%d:%d::%s:/usr/sbin/nologin\n", uid, gid, r.home)), nil, 0, nil
	case "/usr/sbin/useradd":
		r.exists = true
		if r.createHome {
			if err := os.Mkdir(r.home, 0755); err != nil {
				return nil, nil, 1, err
			}
		}
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
func TestEnsureSiteRejectsWebIdentityCollisionBeforeDirectoryCreation(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		uid, gid, webUID, webGID, webGroup int
	}{
		{"web uid", 33, 12345, 33, 34, 35},
		{"web passwd gid", 12345, 34, 33, 34, 35},
		{"web group gid", 12345, 35, 33, 34, 35},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			sites, acme := filepath.Join(root, "sites"), filepath.Join(root, "acme")
			for _, p := range []string{sites, acme} {
				if err := os.Mkdir(p, 0755); err != nil {
					t.Fatal(err)
				}
			}
			r := &accountRunner{home: filepath.Join(sites, "demo"), exists: true, uid: tc.uid, gid: tc.gid, webUID: tc.webUID, webGID: tc.webGID, webGroup: tc.webGroup}
			if err := EnsureSite(context.Background(), r, sites, acme, "demo"); err == nil {
				t.Fatal("accepted web identity collision")
			}
			for _, p := range []string{filepath.Join(sites, "demo"), filepath.Join(acme, "demo")} {
				if _, err := os.Lstat(p); !os.IsNotExist(err) {
					t.Fatalf("directory created before rejection: %s: %v", p, err)
				}
			}
		})
	}
}

func TestEnsureSiteFreshCollisionLeavesAccountAndHome(t *testing.T) {
	root := t.TempDir()
	sites, acme := filepath.Join(root, "sites"), filepath.Join(root, "acme")
	for _, p := range []string{sites, acme} {
		if err := os.Mkdir(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	r := &accountRunner{home: filepath.Join(sites, "demo"), uid: 33, createHome: true}
	if err := EnsureSite(context.Background(), r, sites, acme, "demo"); err == nil {
		t.Fatal("collision accepted")
	}
	if !r.exists {
		t.Fatal("fixture account not created")
	}
	if _, err := os.Stat(r.home); err != nil {
		t.Fatalf("fixture home not created: %v", err)
	}
	if err := EnsureSite(context.Background(), r, sites, acme, "demo"); err == nil {
		t.Fatal("retry accepted unsafe identity")
	}
}

func TestEnsureSiteRejectsFirstWebGroupCollision(t *testing.T) {
	root := t.TempDir()
	sites, acme := filepath.Join(root, "sites"), filepath.Join(root, "acme")
	for _, p := range []string{sites, acme} {
		if err := os.Mkdir(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	r := &accountRunner{home: filepath.Join(sites, "demo"), exists: true, gid: 12345, webGroup: 12345, secondWebGroup: 33}
	if err := EnsureSite(context.Background(), r, sites, acme, "demo"); err == nil {
		t.Fatal("accepted colliding group")
	}
	if _, err := os.Lstat(r.home); !os.IsNotExist(err) {
		t.Fatalf("directory created before rejection: %v", err)
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
