# Pure WordPress installation contract

This package does not install WordPress. The install-plan builder performs no IO,
process execution or authorization. The separate protected-config custody consumer
in `configcustody/config.go` performs scratch-tested filesystem publication and sealed recovery.
It does not open the execution gates below. See
`docs/phase4-protected-config-custody.md` for its trusted caller requirements.
The five-step plan includes a non-executable protected-config publication barrier
between config creation and core installation.

## API

```go
func ApprovedArtifacts() Manifest
func ValidateArtifacts(ArtifactEvidence) error
func BuildInstall(InstallInput, ArtifactEvidence) (Plan, error)
```

`InstallInput` contains only nonsecret values. Supply authorized persisted site name
and primary domain, durable reserved database/account ULIDs and account-resolved
nonzero UID/GID. The builder derives canonical generated names and docroot, fixes
locale/version/host, and provides no arbitrary operation, executable, URL, groups,
environment or argument extension. Configured roots use conservative absolute ASCII
components beginning with alphanumeric/underscore and continuing with those bytes,
dots or hyphens. This lexical policy does not attest custody.

`ArtifactEvidence` is a trusted consumer's public verification result, not a proof
or client attestation. Matching digest strings and `SignatureVerified=true` cannot
establish that verification happened. The custody owner must hash the actual held
bytes, verify the detached signature against the separately reviewed fingerprint,
and prevent replacement through execution. Failures return fixed sanitized errors.

## Mandatory execution conditions, not implemented here

No consumer may execute this plan until the following gates exist and pass:

- B's durable reserved identities, complete ordered resource locks, sealed operation
  lifetime/recovery and atomic metadata/job/claim/journal completion API, plus A's
  PHP publication and probe/restore lifecycle. Do not compose independent writes.
- Held no-follow descriptors for every ancestor and leaf, empty/unmanaged-docroot
  refusal, journal-bound restart exceptions and root-owned PHAR 0644 custody outside
  site homes. Resolve UID/GID from the matched non-login account. Pass the explicitly
  empty supplementary group list through the existing `internal/exec.Runner`.
- Provision isolated per-site bootstrap, home, cache, packages and empty-ini paths
  under `/var/lib/novapanel/wordpress/<site>`, outside site homes. Check ownership,
  permissions and no symlinks before use. Bootstrap and every ancestor up to `/`
  must be agent-controlled and contain no `wp-cli.local.yml` or `wp-cli.yml`.
  WP-CLI 2.12.0 searches from cwd upward, not from the explicit `--path`.
- Provide the reviewed root-owned `/etc/novapanel/wordpress/php.ini` and unwritable
  ancestors. PHP's `-c` is not proof that the file exists or is safe. Explicitly
  reject absence. Use a reviewed required-extension allowlist, no auto_prepend_file,
  auto_append_file or arbitrary bootstrap code, and an agent-controlled empty scan
  directory. Do not inherit PHPRC, PHP/WP_CLI variables, loader variables or HOME.
  This package does not ship a working PHP ini or claim runtime compatibility.
- Use the exact replacement environment, fixed PHP binary, isolated cwd and explicit
  `--path`, `--skip-plugins`, `--skip-themes`, `--skip-packages`. `/dev/null` disables
  global WP-CLI YAML. Pinned PHAR static inspection confirmed config discovery,
  package skip support and cache/packages environment variables. No PHAR was run.
  Skip-plugins does not skip mu-plugins or malicious wp-config code. Permit only the
  verified archive and the agent-generated protected config, no unmanaged entries.
- Generate independent DB/admin passwords from 32 random bytes as unpadded base64url,
  seal before effects, and give each corresponding process only its own password
  plus one newline on stdin. The labels in `Step.Stdin` contain no secret reader or
  value. Never add passwords to a Plan, argv, env, error, result, JSON or fmt output.
  The pure package cannot prove future adapter framing, destruction or log redaction.
- After config-create with `--skip-salts`, a typed no-follow atomic publisher adds
  eight independent local crypto/rand keys/salts and publishes site-owned 0400 config
  before core-install. No PHP eval, extra-php, remote salt fallback or shell.
- Suppress raw child stdout/stderr in persistent logs and return sanitized failures.
  A bounded output buffer is not redaction. Do not persist entire plans as audit
  output; title and administrator fields remain user data, not safe log messages.
- Retain the official runtime checksum command unchanged. Review acquisition/cache
  preload and checksum fixture routing before the network-none lab. The retained ZIP
  is not proof that WP-CLI cache/download/checksum behavior works offline. Prove PHP
  8.3 stdin behavior and actual WordPress/MariaDB grant/schema compatibility later.

## Fixture provenance

Pins come from published phase-4 canonical decisions on foundation `42dfcae`.
`testdata/wordpress-7.1.4-checksums.json` is the complete unchanged 338225-byte
retained official HTTPS response, not synthesized metadata. Its SHA-256 is tested
and all 3782 entries were freshly matched against the retained official ZIP before
copying. The ZIP's version.php agreed with 7.1.4. `verified-artifacts.json` records
fresh actual hashes plus gpgv exit-0 VALIDSIG for the retained PHAR and signature,
using a binary keyring derived from the retained reviewed armored public key.

Evidence sources were the retained packets `phase4-decision-review-zisocmmu`,
`phase4-canonical-handoff-emhx8hxu` and
`phase4-authoritative-amendment-20261031/artifact-verification.json` under scratch.
The archive and PHAR are not copied into git. The WordPress and metadata SHA pins
are computed over official HTTPS bytes, not upstream signed hashes. WP-CLI trust
is the separately reviewed official HTTPS fingerprint plus valid signature, not an
independently certified web of trust. No alternate download/key/trust substitution.

Real installation, filesystem custody, secret-bearing execution, runtime PHP ini,
nonroot lab and privileged Ubuntu VM acceptance remain unimplemented/unpassed.
