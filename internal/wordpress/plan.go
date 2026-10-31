// Package wordpress prepares nonsecret installation contracts and provides a
// protected-config filesystem custody consumer. Neither authorizes PHP execution.
// See README.md for mandatory consumer gates.
package wordpress

import (
	"path/filepath"
	"strings"
)

type Administrator struct{ Title, Username, Email string }

// InstallInput is trusted persisted/reserved identity, not a client request.
// UID/GID must come from the matched provisioned account, never caller claims.
// There are deliberately no passwords, paths into the site, URLs or extra args.
type InstallInput struct {
	SitesRoot, SiteName, PrimaryDomain string
	DatabaseID, DatabaseUserID         string
	UID, GID                           uint32
	Locale                             string
	Administrator                      Administrator
}

type StepKind string

const (
	Download               StepKind = "download"
	VerifyChecksums        StepKind = "verify_checksums"
	CreateConfig           StepKind = "create_config"
	PublishProtectedConfig StepKind = "publish_protected_config"
	InstallCore            StepKind = "install_core"
)

type StdinRequirement string

const (
	DatabasePassword      StdinRequirement = "database_password_plus_newline"
	AdministratorPassword StdinRequirement = "administrator_password_plus_newline"
)

// Step names a secret channel, never its contents. PublishProtectedConfig has no
// executable: the agent must atomically publish eight local salts and 0400 config.
type Step struct {
	Kind    StepKind
	Program string
	Args    []string
	Stdin   StdinRequirement
}

// Plan is a description, not a runnable capability or a custody attestation.
// Groups is explicitly empty: do not inherit the parent's supplementary groups.
type Plan struct {
	Docroot, Dir string
	Env          []string
	UID, GID     uint32
	Groups       []uint32
	Steps        []Step
}

func BuildInstall(in InstallInput, evidence ArtifactEvidence) (Plan, error) {
	if err := ValidateArtifacts(evidence); err != nil {
		return Plan{}, err
	}
	if err := validateInput(in); err != nil {
		return Plan{}, err
	}
	docroot := filepath.Join(in.SitesRoot, in.SiteName, "public")
	base := filepath.Join("/var/lib/novapanel/wordpress", in.SiteName)
	p := Plan{Docroot: docroot, Dir: base + "/bootstrap", UID: in.UID, GID: in.GID, Groups: []uint32{}, Env: []string{
		"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "HOME=" + base + "/home", "WP_CLI_CONFIG_PATH=/dev/null", "WP_CLI_CACHE_DIR=" + base + "/cache", "WP_CLI_PACKAGES_DIR=" + base + "/packages", "PHP_INI_SCAN_DIR=" + base + "/empty-ini",
	}}
	command := func(kind StepKind, stdin StdinRequirement, args ...string) Step {
		argv := []string{"-c", "/etc/novapanel/wordpress/php.ini", "/usr/local/lib/novapanel/wp-cli-2.12.0.phar"}
		argv = append(argv, args...)
		argv = append(argv, "--path="+docroot, "--skip-plugins", "--skip-themes", "--skip-packages")
		return Step{Kind: kind, Program: "/usr/bin/php8.3", Args: argv, Stdin: stdin}
	}
	p.Steps = []Step{
		command(Download, "", "core", "download", "--version=7.1.4", "--locale=en_US"),
		command(VerifyChecksums, "", "core", "verify-checksums", "--version=7.1.4", "--locale=en_US"),
		command(CreateConfig, DatabasePassword, "config", "create", "--dbname=d_"+strings.ToLower(in.DatabaseID), "--dbuser=u_"+strings.ToLower(in.DatabaseUserID), "--dbhost=127.0.0.1", "--dbprefix=wp_", "--dbcharset=utf8mb4", "--skip-salts", "--prompt=dbpass"),
		{Kind: PublishProtectedConfig},
		command(InstallCore, AdministratorPassword, "core", "install", "--url=https://"+in.PrimaryDomain, "--title="+in.Administrator.Title, "--admin_user="+in.Administrator.Username, "--admin_email="+in.Administrator.Email, "--skip-email", "--prompt=admin_password"),
	}
	return p, nil
}
