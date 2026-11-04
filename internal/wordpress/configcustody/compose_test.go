package configcustody

import (
	"context"
	"encoding/base64"
	execboundary "novapanel/internal/exec"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeConfigPHP83DataRoundTrip(t *testing.T) {
	if _, err := os.Stat("/usr/bin/php8.3"); err != nil {
		t.Skip("PHP 8.3 integration dependency unavailable")
	}
	dir := t.TempDir()
	var salts [8]string
	for i := range salts {
		salts[i] = "'\\\n"
	}
	password := "'\\\n?><?php echo 'not executed';"
	raw, err := ComposeConfig("d_example", "u_example", password, salts)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "wp-config.php"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Only this trusted scratch bootstrap executes. No WordPress artifact is run.
	bootstrap := []byte(`<?php echo base64_encode(DB_PASSWORD), ":", base64_encode(NONCE_SALT), ":", DB_HOST, ":", $table_prefix;`)
	if err = os.WriteFile(filepath.Join(dir, "wp-settings.php"), bootstrap, 0600); err != nil {
		t.Fatal(err)
	}
	out, stderr, code, err := (execboundary.Real{}).Run(context.Background(), "/usr/bin/php8.3", []string{"-n", filepath.Join(dir, "wp-config.php")}, execboundary.RunOpts{Env: []string{"PATH=/usr/bin:/bin"}})
	expected := base64.StdEncoding.EncodeToString([]byte(password)) + ":" + base64.StdEncoding.EncodeToString([]byte(salts[7])) + ":127.0.0.1:wp_"
	if err != nil || code != 0 || string(out) != expected || len(stderr) != 0 {
		t.Fatal("PHP 8.3 config data roundtrip failed")
	}
}

func TestComposeConfigEscapesDataAndInstallsEightSalts(t *testing.T) {
	var salts [8]string
	for i := range salts {
		salts[i] = "local-'\\salt"
	}
	raw, err := ComposeConfig("d_example", "u_example", "password'\\value", salts)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "<?php\n") || !strings.Contains(text, "define('DB_PASSWORD', 'password\\'\\\\value');") || !strings.Contains(text, "define('DB_HOST', '127.0.0.1');") || !strings.Contains(text, "require_once ABSPATH . 'wp-settings.php';") {
		t.Fatal("incorrect config composition")
	}
	for _, name := range []string{"AUTH_KEY", "SECURE_AUTH_KEY", "LOGGED_IN_KEY", "NONCE_KEY", "AUTH_SALT", "SECURE_AUTH_SALT", "LOGGED_IN_SALT", "NONCE_SALT"} {
		if strings.Count(text, "define('"+name+"', 'local-\\'\\\\salt');") != 1 {
			t.Fatalf("missing salt %s", name)
		}
	}
	if strings.Contains(text, "https:") || strings.Contains(text, "eval(") {
		t.Fatal("unexpected remote or executable input")
	}
}
