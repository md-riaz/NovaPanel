package arch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdVerifyWarnings(t *testing.T) {
	if _, err := exec.LookPath("systemd-analyze"); err != nil {
		t.Skip("systemd-analyze is not installed")
	}
	script, err := os.ReadFile("systemd_verify.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, directive string
		wantFailure     bool
	}{
		{"clean", "", false},
		{"unknown-directive", "UnknownNovaPanelDirective=yes\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"tools/arch", "packaging/systemd"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(root, "tools/arch/systemd_verify.py")
			if err := os.WriteFile(path, script, 0600); err != nil {
				t.Fatal(err)
			}
			unit := "[Unit]\nDescription=Verification fixture\n[Service]\nExecStart=/usr/bin/true\n" + tc.directive
			if err := os.WriteFile(filepath.Join(root, "packaging/systemd/fixture.service"), []byte(unit), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("python3", path)
			cmd.Env = append(os.Environ(), "TMPDIR="+root)
			out, err := cmd.CombinedOutput()
			if (err != nil) != tc.wantFailure {
				t.Fatalf("want failure=%v, got %v:\n%s", tc.wantFailure, err, out)
			}
			if tc.wantFailure && !strings.Contains(string(out), "UnknownNovaPanelDirective") {
				t.Fatalf("missing fixture warning:\n%s", out)
			}
		})
	}
}
