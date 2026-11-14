package acme_test

import (
	"os"
	"path/filepath"
	"testing"

	"novapanel/internal/acme"
)

func TestEnsureSiteWebroot(t *testing.T) {
	base := t.TempDir()
	if err := os.Chmod(base, 0755); err != nil {
		t.Fatal(err)
	}
	root, err := acme.EnsureSiteWebroot(base, "example")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("root: %v %v", info, err)
	}
	if _, err := acme.EnsureSiteWebroot(base, "example"); err != nil {
		t.Fatal(err)
	}
	p, err := acme.NewWebroot(root, []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	p.Close()
}

func TestEnsureSiteWebrootRejectsUnsafePaths(t *testing.T) {
	base := t.TempDir()
	if err := os.Chmod(base, 0755); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(base, "example")); err != nil {
		t.Fatal(err)
	}
	if _, err := acme.EnsureSiteWebroot(base, "example"); err == nil {
		t.Fatal("accepted symlink")
	}
	if _, err := acme.EnsureSiteWebroot(base, "../escape"); err == nil {
		t.Fatal("accepted traversal")
	}
	if err := os.Mkdir(filepath.Join(base, "unsafe"), 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(base, "unsafe"), 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := acme.EnsureSiteWebroot(base, "unsafe"); err == nil {
		t.Fatal("adopted unsafe directory")
	}
}
