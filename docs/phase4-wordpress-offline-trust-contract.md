# WordPress offline execution trust contract

This clarification records the parent's approved trust decision before dependent code. It does not open a WordPress runner, DB consumer or runtime installation. The canonical artifact pins in phase-4.md remain authoritative.

## Verify before execution

- Acquire into quarantine. Verify WP-CLI 2.12.0 PHAR SHA-256 `ce34ddd838f7351d6759068d09793f26755463b4a4610a5a5c0a97b68220d85c` and detached GPG signature with exact `VALIDSIG` fingerprint `63AF7AA15067C05616FDDD88A3A2E8F226F0BC06`. Use the separately reviewed official key, never arbitrary key retrieval on failure. Record digest, signature result, signer and official HTTPS provenance before any PHP execution.
- Verify installed PHAR custody with held no-follow descriptors. Require root-owned regular file 0644 and root-controlled unwritable ancestors outside site homes. Recheck the bytes used for execution. An untrusted caller's digest or signature-result boolean is not evidence.
- WordPress is 7.1.4 en_US. ZIP SHA-256 is `a147146c159362778ecbbbfb07bb20ddf4e85b130f971c824e656a8f938c5f87`. Official checksum JSON SHA-256 is `bb718bddf197e0098f05395dd9b228fad70b394926ee9d70a8c6a67cf20781e7`. Verify all 3782 entries and version.php before use. These are pins over official HTTPS bytes, not a signed WordPress archive digest.
- A later runtime gate must still run unchanged official `core verify-checksums --version=7.1.4 --locale=en_US --path=<derived docroot>`. Never edit upstream checksum code, add exclusions or use `--insecure`.

## Bootstrap custody

Static inspection of the pinned PHAR's Runner.php shows global configuration uses `WP_CLI_CONFIG_PATH`, while project configuration searches upward from `getcwd()` for wp-cli.local.yml and wp-cli.yml. Setting only the global config path does not disable project configuration.

- Use fixed `/usr/bin/php8.3`, the verified installed PHAR and distinct typed command steps. Every step has explicit `--path=<derived docroot>`. No alias, `--require`, `--exec`, `--extra-php`, arbitrary URL or caller-supplied argument extension.
- Execute from a dedicated agent-owned non-site-writable working directory outside every site home. Verify that directory and every searched ancestor contain no project config or wp-load.php. `--path` selects WordPress independently of cwd.
- Give the child an explicit environment, never inherited ambient WP_CLI/PHP settings. Set isolated HOME, an agent-owned empty global config through `WP_CLI_CONFIG_PATH`, and isolated cache/package locations. Disable packages with the pinned release's supported `--skip-packages`. No site user may write configuration, package directories or their ancestors.
- Use `--skip-plugins --skip-themes` where effective, including core install. These flags do not suppress mu-plugins, drop-ins or malicious wp-config.php. Therefore a held empty-docroot check, verified extraction and typed config publication must exclude all unverified bootstrap files, including files inserted concurrently by a site user.
- CLI PHP uses an explicit reviewed agent-owned ini and isolated `PHP_INI_SCAN_DIR`, with `auto_prepend_file`, `auto_append_file` and `user_ini.filename` empty. Do not rely on CLI ignoring .user.ini while allowing ambient PHP configuration or extension loading. Required extensions and ini contents need confined PHP 8.3 acceptance before execution is enabled.
- Resolve UID/GID/groups from the persisted provisioned account, not a request. No root, nginx supplementary membership, sudo, shell or `--allow-root`. Use the existing typed exec runner.

## Secrets and installation

`config create --prompt=dbpass --skip-salts` consumes only the generated DB password plus one newline on its stdin. `core install --prompt=admin_password --skip-email` uses a separate stdin stream containing only its administrator password plus one newline. Neither secret enters argv, environment, plans, errors or raw persistent child output. Publish eight independent locally generated salts with the typed no-follow atomic config publisher and site-owned 0400 wp-config.php before core install.

## Offline acquisition boundary remains closed

Retained ZIP/checksum bytes alone are not a WP-CLI cache implementation. Before a network-none runtime lab, approve and test the pinned PHAR's download-cache preload format and a fixture response mechanism for the unchanged official checksum request. Fixture routing must be lab-only, pinned to the exact official URL/version/locale and exact retained response bytes, reject all other requests, and leave upstream source and production TLS verification unchanged. No generic HTTP hook, ambient proxy or arbitrary transport override is approved by this document. Until that concrete mechanism and PHP ini are reviewed, artifact validation and static planning may proceed but PHP/WP-CLI must not run.

The retained preparation packet is `/root/.hermes/cache/scratch/phase4-wordpress-execution-contract-20261031/handoff.md`. Complete ordered locks, trusted reserved identities, recovery journal and atomic terminal metadata/job/claim publication remain separate prerequisites. No standalone DB or WordPress consumer is authorized here.
