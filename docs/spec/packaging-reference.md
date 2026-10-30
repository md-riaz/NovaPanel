# Packaging reference (systemd units, tmpfiles, sysusers)

The concrete templates the corpus previously referenced as "BHPanel session notes." These are the
files the implementer writes in `packaging/` (Phase 1 deliverable). Values match F0a (Ubuntu 24.04).

## sysusers — create the unprivileged web user/group

`packaging/sysusers/novapanel.conf` (installed to `/usr/lib/sysusers.d/`):
```
# type name      id   gecos
u     novapanel  -    "NovaPanel web"
```
The agent runs as **root** (no sysusers entry); the `novapanel` system user owns `panel-web` and is
the group on the agent socket (F1).

## tmpfiles — runtime & state dirs (not unit RuntimeDirectory=)

`packaging/tmpfiles/novapanel.conf` (→ `/usr/lib/tmpfiles.d/`):
```
d /run/novapanel      0755 root root      -
d /run/php/novapanel  0755 root root      -
d /var/lib/novapanel  0700 root root      -
d /var/log/novapanel  0750 root novapanel -
d /var/www/acme       0755 root root      -     # world-traversable ACME webroot (B-D8)
```
`/run/novapanel` is here (not a unit `RuntimeDirectory=`) because systemd deletes a RuntimeDirectory
on unit stop; `/var/lib/novapanel` is here (not `StateDirectory=`) to avoid the double-mount trap; the
agent also re-creates any missing dir at start and sets `chattr +a` on `audit.log` itself (so no
`LogsDirectory=`).

## novapanel-agent.service

```ini
[Unit]
Description=NovaPanel privileged agent
After=network.target
Before=novapanel-web.service
StartLimitIntervalSec=300
StartLimitBurst=20

[Service]
Type=simple
ExecStart=/usr/sbin/novapanel-agent
Restart=on-failure
RestartSec=2s
# Root by design (the ONLY root component). NoNewPrivileges=no: it must create users, write
# /etc units, manage the firewall.
NoNewPrivileges=no
ProtectKernelTunables=yes
ProtectKernelModules=yes
RestrictNamespaces=yes
RestrictRealtime=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
SystemCallFilter=@system-service @privileged
SystemCallFilter=~@obsolete @swap
# NO StateDirectory / LogsDirectory / RuntimeDirectory — see tmpfiles note above.
CapabilityBoundingSet=CAP_CHOWN CAP_DAC_OVERRIDE CAP_FOWNER CAP_SETUID CAP_SETGID CAP_KILL CAP_LINUX_IMMUTABLE CAP_NET_ADMIN CAP_NET_BIND_SERVICE
TimeoutStopSec=30s

[Install]
WantedBy=multi-user.target
```
`CAP_LINUX_IMMUTABLE` is required for `chattr +a` on the audit log; `CAP_NET_ADMIN` for firewall
management; `CAP_SETUID/SETGID` to drop a worker to a site user.

## novapanel-web.service

```ini
[Unit]
Description=NovaPanel web UI + API
After=network.target novapanel-agent.service
Wants=novapanel-agent.service
StartLimitIntervalSec=300
StartLimitBurst=20

[Service]
Type=simple
ExecStart=/usr/bin/novapanel-web
Restart=on-failure
RestartSec=2s
User=novapanel
# Never root. Stateless; talks to the agent over the socket only.
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ReadWritePaths=/run/novapanel
ProtectKernelTunables=yes
ProtectControlGroups=yes
RestrictNamespaces=yes
RestrictSUIDSGID=yes
LockPersonality=yes
SystemCallArchitectures=native
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
TimeoutStopSec=15s

[Install]
WantedBy=multi-user.target
```
`systemd-analyze verify packaging/systemd/*.service` must pass (Tier-2 CI). Per-site units
(`novapanel-cron-<site>-<id>.{service,timer}`, the firewall revert transient unit) are rendered at
runtime, not shipped.

## Phase 4 artifact custody, not host bootstrap

Adopt WP-CLI 2.12.0 from `https://github.com/wp-cli/wp-cli/releases/download/v2.12.0/wp-cli-2.12.0.phar`, SHA-256 `ce34ddd838f7351d6759068d09793f26755463b4a4610a5a5c0a97b68220d85c`, signer fingerprint `63AF7AA15067C05616FDDD88A3A2E8F226F0BC06`. Download to quarantine, verify digest and detached signature against the separately reviewed official fingerprint, then install root-owned 0644 outside site homes at `/usr/local/lib/novapanel/wp-cli-2.12.0.phar`. Refuse execution if installed bytes or custody differ. The URL is versioned, not immutable. Reverify approved bytes, signature and reviewed official fingerprint for every acquired or installed artifact. Signature verification evidence is retained in /root/.hermes/cache/scratch/phase4-decision-review-zisocmmu/signature-verification.log and reverified in this packet; trust is official fingerprint over HTTPS plus valid signature, not independently certified web of trust. Do not invent a trust key, fetch an arbitrary key on mismatch, or call the PHAR executable just to inspect it.

Generate eight independent WordPress keys/salts locally with crypto/rand and store them only in the sealed operation until protected config publication. Use WP-CLI config create with `--skip-salts` and stdin DB password, then add the generated constants with the agent's typed config publisher before core install. Do not use --extra-php stdin, PHP eval or the remote salt fallback. wp-config is a no-follow, atomically published site-owned 0400 regular file under the site lock. Refuse an existing/unmanaged or nonempty docroot; restart may resume only files bound to its own journal. Adopt WordPress 7.1.4, locale en_US, official ZIP https://downloads.wordpress.org/release/wordpress-7.1.4.zip, SHA-256 a147146c159362778ecbbbfb07bb20ddf4e85b130f971c824e656a8f938c5f87, 37239942 bytes. Pin the full official metadata from https://api.wordpress.org/core/checksums/1.0/?version=7.1.4&locale=en_US. Retained ZIP matched all 3782 entries. Retained full official checksum JSON SHA-256 is bb718bddf197e0098f05395dd9b228fad70b394926ee9d70a8c6a67cf20781e7; preserve its exact bytes as the offline fixture alongside the archive, and retain the official runtime verification path. This SHA is a locally computed pin over official HTTPS bytes, not an upstream signed SHA. Retain the unchanged official wp core verify-checksums command as an additional runtime gate; do not replace it with our ZIP scan, change checksum source files, use --insecure, or assume PHP/WP-CLI compatibility without the later real lab. Candidate grant allowlist stays exactly the phase spec's DB-scoped list until real install and upgrade/schema acceptance for that release passes. Failure blocks release; it does not justify global privileges or plugin-driven permission expansion.

Add only `d /run/php/novapanel 0755 root root -` and reviewed dependency metadata to packaging. make release-stage still builds binaries and maintenance.tar, not units/accounts/dependency installation. Remove phase-4's obsolete home 0750, `or wp.install`, secret argv example, nightly-admin_password claim and all-verbs-jobs language. Remove host secure-installation execution from Phase 4. Only Slow mutations queue; ssl.auto_set stays synchronous.
