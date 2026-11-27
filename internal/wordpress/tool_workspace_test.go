package wordpress

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	runner "novapanel/internal/exec"
)

func TestEnsureToolWorkspace(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root ownership required")
	}
	sites, err := os.MkdirTemp("/tmp", "novapanel-tool-workspace-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sites) })
	if err := os.Chmod(sites, 0755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(sites, "example")
	if err := os.Mkdir(home, 0751); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(home, 2000000, 2000000); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0751); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := EnsureToolWorkspace(sites, "example", 2000000, 2000000); err != nil {
			t.Fatal(err)
		}
	}
	base := filepath.Join(home, ".novapanel-wpcli")
	_, _, code, err := (runner.Real{}).Run(context.Background(), "/usr/bin/test", []string{"-w", filepath.Join(base, "cache")}, runner.RunOpts{Uid: 2000000, Gid: 2000000, Groups: []uint32{}})
	if err != nil || code != 0 {
		t.Fatalf("site UID cannot write cache: %v", err)
	}
	if err := os.Remove(filepath.Join(base, "cache")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(base, "cache")); err != nil {
		t.Fatal(err)
	}
	if err := EnsureToolWorkspace(sites, "example", 2000000, 2000000); err == nil {
		t.Fatal("symlink accepted")
	}
}
