// phase4wpoffline is a confined dependency lab, never a production installer.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"bytes"
	"crypto/rand"
	"encoding/base64"
	runner "novapanel/internal/exec"
	"novapanel/internal/wordpress/configcustody"
)

var errRuntimeRefused = errors.New("offline WordPress runtime refused")

func verify(ctx context.Context, r runner.Runner) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, errout, code, err := r.Run(ctx, "/usr/local/bin/php", []string{"-n", "-d", "auto_prepend_file=", "-d", "auto_append_file=", "-d", "user_ini.filename=", "-d", "memory_limit=256M", "-d", "display_errors=0", "-d", "log_errors=0", "/lab/bootstrap.php", "core", "verify-checksums", "--version=7.1.4", "--locale=en_US", "--path=/scratch/public", "--skip-plugins", "--skip-themes", "--skip-packages"}, runner.RunOpts{Dir: "/lab/cwd", Env: []string{"LANG=C", "PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/lab/home", "WP_CLI_CONFIG_PATH=/dev/null", "WP_CLI_CACHE_DIR=/scratch/cache", "WP_CLI_PACKAGES_DIR=/lab/packages", "PHP_INI_SCAN_DIR=/lab/empty-ini"}, Groups: []uint32{}, MaxOutput: 16384})
	defer clear(out)
	defer clear(errout)
	if err != nil || code != 0 || len(out) >= 16384 || len(errout) >= 16384 || !bytes.Contains(out, []byte("Success: WordPress installation verifies against checksums.")) {
		return errRuntimeRefused
	}
	return nil
}

func localConfig() ([]byte, error) {
	var salts [8]string
	for i := range salts {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, errRuntimeRefused
		}
		salts[i] = base64.RawURLEncoding.EncodeToString(b)
		clear(b)
	}
	return configcustody.ComposeConfig("d_lab", "u_lab", "lab'\\password", salts)
}

func run(ctx context.Context, r runner.Runner) error {
	if os.Getuid() != 23041 || os.Getgid() != 23041 {
		return errRuntimeRefused
	}
	if err := verify(ctx, r); err != nil {
		return err
	}
	// The unchanged official command must reject changed core bytes.
	f, err := os.OpenFile("/scratch/public/wp-includes/version.php", os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return errRuntimeRefused
	}
	_, err = f.WriteString("\n// deliberate lab checksum corruption\n")
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errRuntimeRefused
	}
	if verify(ctx, r) == nil {
		return errRuntimeRefused
	}
	raw, err := localConfig()
	if err != nil {
		return err
	}
	defer clear(raw)
	if err = os.Mkdir("/scratch/config", 0700); err != nil {
		return errRuntimeRefused
	}
	if err = os.WriteFile("/scratch/config/wp-config.php", raw, 0400); err != nil {
		return errRuntimeRefused
	}
	// Only this fixed lab settings file is loaded, never WordPress settings.
	settings := []byte(`<?php if (DB_NAME !== 'd_lab' || DB_USER !== 'u_lab' || DB_HOST !== '127.0.0.1' || $table_prefix !== 'wp_' || strlen(NONCE_SALT) !== 43) { exit(92); } echo 'config data PASS';`)
	if err = os.WriteFile("/scratch/config/wp-settings.php", settings, 0400); err != nil {
		return errRuntimeRefused
	}
	out, stderr, code, err := r.Run(ctx, "/usr/local/bin/php", []string{"-n", "/scratch/config/wp-config.php"}, runner.RunOpts{Dir: "/lab/cwd", Env: []string{"LANG=C"}, Groups: []uint32{}, MaxOutput: 1024})
	defer clear(out)
	defer clear(stderr)
	if err != nil || code != 0 || !bytes.Equal(out, []byte("config data PASS")) {
		return errRuntimeRefused
	}
	return nil
}
func main() {
	if run(context.Background(), runner.Real{}) != nil {
		fmt.Fprintln(os.Stderr, "offline runtime FAIL")
		os.Exit(1)
	}
	fmt.Println("offline runtime PASS: official checksum success and tamper refusal")
}
