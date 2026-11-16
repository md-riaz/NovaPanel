package main

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	runner "novapanel/internal/exec"
)

type capture struct {
	calls int
	args  []string
	opts  runner.RunOpts
	out   []byte
}

func (c *capture) Run(_ context.Context, p string, a []string, o runner.RunOpts) ([]byte, []byte, int, error) {
	c.calls++
	c.args = a
	c.opts = o
	if p != "/usr/local/bin/php" {
		panic("unexpected PHP")
	}
	return c.out, nil, 0, nil
}
func TestChecksumRuntimeRequiresOfficialSuccess(t *testing.T) {
	if verify(context.Background(), &capture{}) == nil {
		t.Fatal("empty successful child accepted")
	}
}

func TestChecksumRuntimeFixedInvocation(t *testing.T) {
	r := &capture{out: []byte("Success: WordPress installation verifies against checksums.")}
	if err := verify(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	want := []string{"-n", "-d", "auto_prepend_file=", "-d", "auto_append_file=", "-d", "user_ini.filename=", "-d", "memory_limit=256M", "-d", "display_errors=0", "-d", "log_errors=0", "/lab/bootstrap.php", "core", "verify-checksums", "--version=7.1.4", "--locale=en_US", "--path=/scratch/public", "--skip-plugins", "--skip-themes", "--skip-packages"}
	if !reflect.DeepEqual(r.args, want) {
		t.Fatalf("argv %q", r.args)
	}
	if r.opts.Dir != "/lab/cwd" || r.opts.MaxOutput != 16384 || r.opts.Stdin != nil || r.opts.Groups == nil {
		t.Fatalf("unsafe opts %+v", r.opts)
	}
	if !reflect.DeepEqual(r.opts.Env, []string{"LANG=C", "PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/lab/home", "WP_CLI_CONFIG_PATH=/dev/null", "WP_CLI_CACHE_DIR=/scratch/cache", "WP_CLI_PACKAGES_DIR=/lab/packages", "PHP_INI_SCAN_DIR=/lab/empty-ini"}) {
		t.Fatal(r.opts.Env)
	}
}
func TestChecksumRuntimeDiscardsOutput(t *testing.T) {
	r := &capture{out: []byte("Success: WordPress installation verifies against checksums." + strings.Repeat("secret", 10))}
	if err := verify(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	for _, b := range r.out {
		if b != 0 {
			t.Fatal("child output retained")
		}
	}
}
func TestLocalConfigUsesTypedComposer(t *testing.T) {
	raw, err := localConfig()
	if err != nil || !strings.Contains(string(raw), "define('DB_HOST', '127.0.0.1');") || !strings.Contains(string(raw), "define('NONCE_SALT',") {
		t.Fatal("typed local config missing")
	}
	clear(raw)
}

func TestLabRefusesAmbientRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root refusal")
	}
	if err := run(context.Background(), &capture{}); err == nil {
		t.Fatal("root runtime admitted")
	}
}
