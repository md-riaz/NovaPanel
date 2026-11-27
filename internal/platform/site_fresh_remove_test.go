package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveFreshSitePathsConfined(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires disposable root container")
	}
	base := t.TempDir()
	sites := filepath.Join(base, "sites")
	logs := filepath.Join(base, "logs")
	outside := filepath.Join(base, "outside")
	for _, p := range []string{sites, logs, outside, filepath.Join(sites, "demo"), filepath.Join(sites, "demo", "public"), filepath.Join(logs, "demo")} {
		if err := os.Mkdir(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{filepath.Join(sites, "demo"), filepath.Join(sites, "demo", "public"), filepath.Join(logs, "demo")} {
		if err := os.Chown(p, 2000000, 2000000); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(outside, "keep")
	if err := os.WriteFile(keep, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(sites, "demo", "public", "link")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveFreshSitePaths(sites, logs, "demo", 2000000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("outside removed", err)
	}
	if _, err := os.Lstat(filepath.Join(sites, "demo")); !os.IsNotExist(err) {
		t.Fatal("home retained", err)
	}
	if err := RemoveFreshSitePaths(sites, logs, "demo", 2000000); err != nil {
		t.Fatal("replay", err)
	}
	if err := os.Mkdir(filepath.Join(sites, "demo"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := RemoveFreshSitePaths(sites, logs, "demo", 2000000); err == nil {
		t.Fatal("foreign home accepted")
	}
	if _, err := os.Stat(filepath.Join(sites, "demo")); err != nil {
		t.Fatal("foreign home removed", err)
	}
}
