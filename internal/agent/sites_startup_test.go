package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStartupRejectsUnsafeSitesRoot(t *testing.T) {
	for _, kind := range []string{"writable", "symlink", "file", "writable-parent"} {
		t.Run(kind, func(t *testing.T) {
			c := startupConfig(t)
			parent := filepath.Join(filepath.Dir(c.DBPath), "sites-parent")
			c.SitesRoot = filepath.Join(parent, "sites")
			if err := os.MkdirAll(c.SitesRoot, 0755); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "writable":
				if err := os.Chmod(c.SitesRoot, 0777); err != nil {
					t.Fatal(err)
				}
			case "writable-parent":
				if err := os.Chmod(parent, 0777); err != nil {
					t.Fatal(err)
				}
			case "symlink", "file":
				if err := os.Remove(c.SitesRoot); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink" {
					if err := os.Symlink(t.TempDir(), c.SitesRoot); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(c.SitesRoot, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r := &startupRunner{}
			a := NewWithDependencies(c, r, nil)
			if close, err := a.OpenState(context.Background()); err == nil {
				close()
				t.Fatal("accepted unsafe sites root")
			}
			if r.calls != 0 {
				t.Fatal("unsafe startup reached privileged command")
			}
		})
	}
}

func TestStartupProvisionsSitesRoot(t *testing.T) {
	c := startupConfig(t)
	c.SitesRoot = filepath.Join(filepath.Dir(c.DBPath), "sites")
	a := NewWithDependencies(c, &startupRunner{}, nil)
	close, err := a.OpenState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	info, err := os.Lstat(c.SitesRoot)
	if err != nil {
		t.Fatalf("startup omitted sites root: %v", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0755 {
		t.Fatal("unsafe sites root mode")
	}
}
