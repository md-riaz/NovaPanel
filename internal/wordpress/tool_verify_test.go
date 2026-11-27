package wordpress

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runner "novapanel/internal/exec"
)

func TestVerifyPackagedTool(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root custody required")
	}
	root := filepath.Join("..", "..", "packaging", "rootfs")
	dir := t.TempDir()
	sources := []string{"usr/lib/novapanel/wpcli/wp-cli-2.12.0.phar", "usr/lib/novapanel/wpcli/wp-cli-2.12.0.phar.asc", "usr/lib/novapanel/wpcli/wp-cli-release.gpg", "usr/lib/novapanel/wpcli/php.ini"}
	var paths [4]string
	for i, source := range sources {
		b, err := os.ReadFile(filepath.Join(root, source))
		if err != nil {
			t.Fatal(err)
		}
		paths[i] = filepath.Join(dir, string(rune('a'+i)))
		if err := os.WriteFile(paths[i], b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	verify := func() error { _, err := verifyTool(context.Background(), runner.Real{}, paths, 0, true); return err }
	if err := verify(); err != nil {
		t.Fatalf("valid package: %v", err)
	}
	for _, i := range []int{0, 1, 2, 3} {
		b, err := os.ReadFile(paths[i])
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(paths[i], []byte("tampered"), 0644); err != nil {
			t.Fatal(err)
		}
		if err = verify(); err == nil {
			t.Fatalf("tampered %d accepted", i)
		}
		if err = os.WriteFile(paths[i], b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(paths[0], 0666); err != nil {
		t.Fatal(err)
	}
	if err := verify(); err == nil {
		t.Fatal("writable PHAR accepted")
	}
}

func TestPackagedCLIWithReviewedINI(t *testing.T) {
	if _, err := os.Stat("/usr/bin/php8.3"); os.IsNotExist(err) {
		t.Skip("Ubuntu PHP 8.3 unavailable")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "packaging", "rootfs", "usr", "lib", "novapanel", "wpcli"))
	if err != nil {
		t.Fatal(err)
	}
	out, stderr, code, err := (runner.Real{}).Run(context.Background(), "/usr/bin/php8.3", []string{"-c", filepath.Join(root, "php.ini"), filepath.Join(root, "wp-cli-2.12.0.phar"), "--info"}, runner.RunOpts{Dir: root, Env: []string{"PATH=/usr/bin:/bin", "PHP_INI_SCAN_DIR=/dev/null"}})
	if err != nil || code != 0 || !strings.Contains(string(out), "WP-CLI version:\t2.12.0") {
		t.Fatalf("packaged CLI failed: code=%d err=%v stderr=%s", code, err, stderr)
	}
}
