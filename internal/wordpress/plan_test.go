package wordpress

import (
	"encoding/json"
	"reflect"
	"testing"
)

func validInput() InstallInput {
	return InstallInput{SitesRoot: "/srv/sites", SiteName: "example", PrimaryDomain: "example.test", DatabaseID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", DatabaseUserID: "01ARZ3NDEKTSV4RRFFQ69G5FAW", UID: 1001, GID: 1001, Locale: "en_US", Administrator: Administrator{Title: "Site title", Username: "Editor.name-1", Email: "owner@example.test"}}
}
func artifactEvidence(t *testing.T) ArtifactEvidence {
	t.Helper()
	return ArtifactEvidence{CLISHA256: ApprovedArtifacts().CLI.SHA256, WordPressSHA256: ApprovedArtifacts().WordPress.SHA256, ChecksumsSHA256: ApprovedArtifacts().ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: ApprovedArtifacts().CLISignerFingerprint}
}
func TestInstallPlan(t *testing.T) {
	p, err := BuildInstall(validInput(), artifactEvidence(t))
	if err != nil {
		t.Fatal(err)
	}
	if p.Docroot != "/srv/sites/example/public" || p.UID != 1001 || p.GID != 1001 || p.Groups == nil || len(p.Groups) != 0 {
		t.Fatalf("bad context: %+v", p)
	}
	want := []Step{
		{Kind: Download, Program: "/usr/bin/php8.3", Args: []string{"-c", "/etc/novapanel/wordpress/php.ini", "/usr/local/lib/novapanel/wp-cli-2.12.0.phar", "core", "download", "--version=7.1.4", "--locale=en_US"}},
		{Kind: VerifyChecksums, Program: "/usr/bin/php8.3", Args: []string{"-c", "/etc/novapanel/wordpress/php.ini", "/usr/local/lib/novapanel/wp-cli-2.12.0.phar", "core", "verify-checksums", "--version=7.1.4", "--locale=en_US"}},
		{Kind: CreateConfig, Program: "/usr/bin/php8.3", Stdin: DatabasePassword, Args: []string{"-c", "/etc/novapanel/wordpress/php.ini", "/usr/local/lib/novapanel/wp-cli-2.12.0.phar", "config", "create", "--dbname=d_01arz3ndektsv4rrffq69g5fav", "--dbuser=u_01arz3ndektsv4rrffq69g5faw", "--dbhost=127.0.0.1", "--dbprefix=wp_", "--dbcharset=utf8mb4", "--skip-salts", "--prompt=dbpass"}},
		{Kind: PublishProtectedConfig},
		{Kind: InstallCore, Program: "/usr/bin/php8.3", Stdin: AdministratorPassword, Args: []string{"-c", "/etc/novapanel/wordpress/php.ini", "/usr/local/lib/novapanel/wp-cli-2.12.0.phar", "core", "install", "--url=https://example.test", "--title=Site title", "--admin_user=Editor.name-1", "--admin_email=owner@example.test", "--skip-email", "--prompt=admin_password"}},
	}
	for i := range want {
		if want[i].Program != "" {
			want[i].Args = append(want[i].Args, "--path=/srv/sites/example/public", "--skip-plugins", "--skip-themes", "--skip-packages")
		}
	}
	if !reflect.DeepEqual(p.Steps, want) {
		t.Fatalf("steps differ: %#v", p.Steps)
	}
	if p.Dir != "/var/lib/novapanel/wordpress/example/bootstrap" {
		t.Fatalf("unsafe cwd: %s", p.Dir)
	}
	wantEnv := []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "HOME=/var/lib/novapanel/wordpress/example/home", "WP_CLI_CONFIG_PATH=/dev/null", "WP_CLI_CACHE_DIR=/var/lib/novapanel/wordpress/example/cache", "WP_CLI_PACKAGES_DIR=/var/lib/novapanel/wordpress/example/packages", "PHP_INI_SCAN_DIR=/var/lib/novapanel/wordpress/example/empty-ini"}
	if !reflect.DeepEqual(p.Env, wantEnv) {
		t.Fatalf("environment: %#v", p.Env)
	}
	bad := artifactEvidence(t)
	bad.SignatureVerified = false
	if p, err := BuildInstall(validInput(), bad); err == nil || !reflect.DeepEqual(p, Plan{}) {
		t.Fatal("plan emitted for unverified artifact")
	}
	// No mutable singleton slices or input aliasing.
	p.Env[0] = "poison"
	p.Steps[0].Args[0] = "poison"
	p2, err := BuildInstall(validInput(), artifactEvidence(t))
	if err != nil || p2.Env[0] != "PATH=/usr/bin:/bin" || p2.Steps[0].Args[0] != "-c" {
		t.Fatal("shared plan state")
	}
	b, err := json.Marshal(p2)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("empty plan")
	}
}
