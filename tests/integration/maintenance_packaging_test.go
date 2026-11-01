package integration

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseStageIncludesMaintenance(t *testing.T) {
	output := t.TempDir()
	cmd := exec.Command("make", "-C", "../..", "release-stage", "OUT="+output)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release staging: %v: %s", err, b)
	}
	for _, name := range []string{"panel-agent", "panel-web", "novapanelctl", "maintenance.tar"} {
		info, err := os.Stat(filepath.Join(output, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			t.Fatalf("missing staged artifact %s: %v", name, err)
		}
	}
}

func TestMaintenancePackage(t *testing.T) {
	output := filepath.Join(t.TempDir(), "maintenance.tar")
	cmd := exec.Command("python3", "../../tools/package_maintenance.py", output)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("package: %v: %s", err, b)
	}
	f, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	want := map[string]int64{"var": 0755, "var/www": 0755, "var/www/novapanel": 0755, "var/www/novapanel/maintenance.html": 0644}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(h.Name, "/")
		mode, ok := want[name]
		if !ok || h.Mode != mode || h.Uid != 0 || h.Gid != 0 || h.Linkname != "" {
			t.Fatalf("unexpected package entry: %+v", h)
		}
		delete(want, name)
		if name == "var/www/novapanel/maintenance.html" {
			if h.Typeflag != tar.TypeReg {
				t.Fatal("maintenance page is not a regular file")
			}
			b, err := io.ReadAll(tr)
			asset, readErr := os.ReadFile("../../packaging/rootfs/var/www/novapanel/maintenance.html")
			if err != nil || readErr != nil || !bytes.Equal(b, asset) || !bytes.Contains(b, []byte("Site under maintenance")) || bytes.Contains(b, []byte("<script")) {
				t.Fatalf("maintenance content mismatch: %v %v", err, readErr)
			}
		} else if h.Typeflag != tar.TypeDir {
			t.Fatal("parent is not a directory")
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing package entries: %v", want)
	}
}
