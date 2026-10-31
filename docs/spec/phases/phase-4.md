# Phase 4 — PHP, WordPress, MariaDB

## Goal
PHP sites with per-site isolation (own Unix user + FPM pool), a least-privilege MariaDB DB/user
lifecycle, and a checksum-verified WordPress install. One supported PHP version first.

## Depends on
Phases 1–3 (jobs, auth, sites+nginx renderer). Reads: `00-foundations.md` F6–F12; `vpspanel-plan.md`
§6 (per-site isolation).

## Deliverables
1. **Per-site Unix user + FPM pool** (§6): one non-login user per site (`/usr/sbin/nologin`, `0751`
   home); one PHP-FPM pool over a Unix socket where the **worker runs as the site user** but the
   **socket is owned by the nginx user** (the 502 fix); `pm=ondemand`; per-pool hardening.
2. **PHP version management** (`php.set`, `php.list`): one supported version first; per-site version +
   limits.
3. **MariaDB DB/user lifecycle** (`database.create/rotate_password/delete/list`, `dbuser.*`):
   per-site DB + dedicated user bound to loopback, **least-privilege grant** (never `*.*`), generated
   names + show-once passwords; the **agent authenticates to MariaDB as root via `unix_socket`** (no
   stored admin password anywhere).
4. **WordPress install** (`site.create type=wordpress`): `wp core download` →
   `wp core verify-checksums` → `wp config create` → `wp core install`, run **as the site user**,
   `wp-config.php` perms `0400`, never over a non-empty docroot.
5. **Guard:** no destructive WordPress ops (update/clone/staging) until backups/restore land (Phase 6).

## Blueprint

### FPM pool (reference — the 502 fix is load-bearing)
`/etc/php/<ver>/fpm/pool.d/<site>.conf`:
```ini
[{{site}}]
user  = {{siteuser}}
group = {{siteuser}}
listen = /run/php/novapanel/{{site}}.sock
listen.owner = www-data        ; the NGINX user owns the socket  ← without this: EACCES → 502
listen.group = www-data        ; (www-data on Debian/Ubuntu, nginx on RHEL/Rocky/Alma)
listen.mode  = 0660
pm = ondemand
pm.max_children         = 2           ; fixed bounded phase cap
pm.process_idle_timeout = 10s
pm.max_requests         = 500         ; respawn worker (leak guard)
php_admin_value[cgi.fix_pathinfo]  = 0        ; defense in depth w/ nginx try_files (Phase 3)
; Compatibility profile: no open_basedir and no command-function denylist.
; The worker's Unix uid, filesystem permissions and DB grant are the isolation boundary.
; dl remains disabled in the system PHP configuration.
php_admin_value[error_log]         = /var/log/novapanel/{{site}}/php-error.log
php_admin_flag[log_errors]         = on
```
- **Compatibility profile is the default.** Do not set `open_basedir` or disable `exec`, `passthru`,
  `shell_exec`, `system`, `proc_open` or `popen` by default. A VPS owner may need Composer, framework
  workers, image/PDF processors and deployment hooks. The per-site Unix uid, home `0751`, docroot `site:www-data 2750` and private per-file/subtree modes and
  least-privilege DB user provide the actual boundary; `dl` stays disabled system-wide.
- Optional hardened PHP is deferred until a later approved request shape. No field or toggle exists in Phase 4. The UI states that it is unavailable; compatibility remains the default.

### MariaDB lifecycle (reference)
```sql
CREATE DATABASE `{{db}}` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER '{{dbuser}}'@'127.0.0.1' IDENTIFIED BY '{{generated}}';
GRANT SELECT,INSERT,UPDATE,DELETE,CREATE,DROP,ALTER,INDEX,CREATE TEMPORARY TABLES,LOCK TABLES,REFERENCES
  ON `{{grant_pattern_escaped_then_identifier_quoted_db}}`.* TO '{{dbuser}}'@'127.0.0.1';          -- NEVER *.*, NEVER GRANT OPTION/FILE/SUPER
```
- **Transport decision (locked here):** **app/site connections use TCP `127.0.0.1`**; the DB user is
  `'{{dbuser}}'@'127.0.0.1'`, the Go/WordPress DSN uses `127.0.0.1`, and `wp config create --dbhost`
  = `127.0.0.1`. The three **must** match or installs fail "Access denied". The **agent's own admin**
  connection uses the **`unix_socket`** plugin (root over the socket, no password).
- **`bind-address = 127.0.0.1`** in server config (no network exposure). Rotation:
  `ALTER USER '{{dbuser}}'@'127.0.0.1' IDENTIFIED BY '{{new}}'` then update the site's `wp-config.php`
  in one recoverable job with sealed credentials and config consistency. Host secure-installation/bootstrap is outside this phase.
- **No admin DB password is ever stored** (the single most important control): the agent authenticates
  by OS identity via `unix_socket`. If a file credential is ever unavoidable, root-owned `0600`
  outside the DB and web root; **never `MYSQL_PWD`** (deprecated, leaks via `/proc`), never `-p<pw>`
  on argv.

### WordPress install (reference — fixed argv, run as the site user)
```
wp core download --version=<v> --path=/srv/sites/{{site}}/public        # refuse if docroot non-empty → precondition_failed
wp core verify-checksums --version=<v>                                   # mismatch → agent_error (tampered); NEVER --insecure
wp config create --dbname={{db}} --dbuser={{dbuser}} --prompt=dbpass --skip-salts \
    --dbhost=127.0.0.1 --dbprefix=wp_ --dbcharset=utf8mb4 --path=...     # salts unique per site
wp core install --url=https://{{domain}} --title=... --admin_user=... \
    --admin_email=... --skip-email --prompt=admin_password --path=...    # independent stdin consumer
# typed publisher installs site-owned 0400 wp-config.php with locally generated salts before core install
```
WP-CLI runs as the **site user** (it refuses `--allow-root` by design); file ownership must match the
FPM pool user so auto-updates work.

### Privilege drop, secrets, sockets (resolves B-D11, B29, B39)
- **PHP version pinned: 8.3** (F0a). Pool dir `/etc/php/8.3/fpm/pool.d/`, reload `php8.3-fpm`.
- **Run as the site user** via `exec.Run` with `RunOpts{Uid, Gid, Groups}` (F15) — the agent forks a
  child dropped to the site uid/gid (`setgroups`+`setgid`+`setuid`); no `sudo`/`su`/`runuser`, no
  shell. `wp`/`git`/`mysql` all go through this.
- **DB admin** connects as root via the **`unix_socket`** plugin (no password, no argv secret). A
  **site** DB user's password is generated, applied with `CREATE USER … IDENTIFIED BY` /
  `ALTER USER … IDENTIFIED BY` run through an admin connection (DDL, not argv), and delivered to the
  caller **show-once via the F6 job `claim`** — never stored in `jobs.result_json`, the DB, or audit.
- **wp-config password off argv:** `wp config create … --prompt=dbpass` with the password on the
  child's **stdin** (`RunOpts.Stdin`); never `--dbpass=<pw>` on the command line. `mysqldump`/`mysql`
  that need the site cred use `--defaults-extra-file=<root-0600 tmp>` written then removed, not `-p`.
- **FPM socket dir:** `/run/php/novapanel/` created by a **tmpfiles** line
  (`d /run/php/novapanel 0755 root root -`) so it survives an fpm restart (same rationale as
  `/run/novapanel`). Socket `listen.owner/group = www-data` (F0a nginx user).
- **Docroot/home ownership** follows Phase 3's model (docroot `site:www-data 2750`, home `0751`) so
  nginx can serve; Phase 4 creates the user and dirs to match. `useradd -r -s /usr/sbin/nologin -m`,
  home `/srv/sites/<site>`, group = the site name; reserved-name list in `internal/validate`.

## External refs (gathered — verified, with flags to resolve before coding)
- **502 cause (verified):** FPM socket default owner = FPM run-user (site user) mode 0660 → nginx
  `connect()` EACCES → 502; fix is `listen.owner/group = nginx`, not the worker identity.
- **MariaDB (verified):** `localhost`≠`127.0.0.1` are distinct accounts (socket vs TCP); `unix_socket`
  plugin auth = peer OS uid via SO_PEERCRED (no password) — cleanest for a root agent; an
  `unix_socket` account has no password so `SET PASSWORD` errors; `MYSQL_PWD` "extremely insecure",
  deprecated.
- **WordPress (verified):** `wp core verify-checksums` compares against WordPress.org hashes; **never
  `--insecure`**; wp-config perms **0400**; run as site user not root; check docroot empty first.
- WP-CLI 2.12.0 uses --prompt=dbpass and --prompt=admin_password in separate processes. Eight salts are locally generated. Exact artifact pins and grant checks follow below.

## Verification (the gate)
```
1.  Per-site user: non-login, 0751 home and site:www-data 2750 docroot; a second site's user cannot read the first site's files.
2.  FPM pool: worker runs as the SITE user (asserted), socket owned by the NGINX user mode 0660 →
      nginx serves PHP (no 502). A second site's worker cannot read the first site's `0750` files.
      The compatibility default leaves child-process functions available. Optional hardened PHP is
      explicitly unavailable until a later request-shape amendment; test the unavailability warning.
3.  DB lifecycle: database.create makes a loopback-bound user with ONLY that db's privileges (a test
      asserts the user cannot read another db and has no *.* grant); rotate updates wp-config in the
      same job; the agent used unix_socket (no admin password exists anywhere in config/DB).
4.  Transport consistency: user host, Go/WP DSN, and wp-config --dbhost all 127.0.0.1; a mismatch
      test reproduces "Access denied" and the spec's single-transport rule prevents it.
5.  WP install: verify-checksums runs and a tampered file FAILS the install; wp-config.php is 0400
      owned by the site user; install over a non-empty docroot is refused; WP-CLI ran as the site user.
6.  No destructive WP op (update/clone/staging) is callable yet (returns unsupported until Phase 6).
7.  Only Slow mutations are jobs; ssl.auto_set stays synchronous. All mutations are audited; passwords are show-once
      via the job claim (never returned twice, never in result_json/logs).
```

## Definition of Done
- [ ] Per-site user + FPM pool with the socket-owner 502 fix; cross-site isolation proven.
- [ ] Pool hardening: `cgi.fix_pathinfo=0`, per-site error log, and default compatibility profile
  documented. Optional hardened PHP is deferred until a later approved request shape; UI unavailability is explicit.
- [ ] MariaDB per-site DB+user, least-privilege (no `*.*`), loopback-bound; admin via unix_socket, no stored admin password.
- [ ] Single DB transport (127.0.0.1) consistent across user host / DSN / wp-config.
- [ ] WordPress install verifies checksums (never --insecure), wp-config 0400, runs as site user, refuses non-empty docroot.
- [ ] Show-once passwords; only Slow mutations are jobs, all mutations audited; no destructive WP ops before Phase 6.

## Guardrails (this phase)
- **The FPM socket is owned by the Nginx user, never the site user.** The worker runs as the site user;
  these settings must remain separate to avoid the 502 permission-denied failure.
- **Do not treat PHP configuration as the isolation boundary.** The default profile keeps process-execution
  functions available and does not set `open_basedir`; per-site Unix uid, home `0751`, docroot `site:www-data 2750` and private per-file/subtree modes,
  per-site socket and least-privilege DB grants contain a compromised project. Hardened PHP restrictions
  are deferred and unavailable until a later approved request shape and warning contract.
- **Never grant `*.*` or `GRANT OPTION`/`FILE`/`SUPER`;** per-site DB + loopback user only.
- **No DB-admin password stored anywhere** — agent uses `unix_socket`; never `MYSQL_PWD`/`-p` on argv.
- **Never skip checksum verification or use `--insecure`;** wp-config never world-readable; WP-CLI
  never as root.
- **No destructive WordPress ops until restore exists (Phase 6).**

## Canonical Phase 4 amendment and entry gate

Phase 3 PR #81 is merged at `2ff29a1a70f0ad678969d48006a4c21c183dd29a`. Its tree matches independently reviewed `6107f1d`; the parent verified all 13 safe gates. Phase 4 now opens from that merged main under the user's phase-by-phase authority, with one draft PR and no Phase 4 merge yet. The earlier stacked opening is historical, not current authorization. Preserve main's #86 reply/recovery and D2 trust-anchor rulings. Final privileged Ubuntu VM checks remain deferred and unpassed. Browser timeout history remains disclosed, not proven fixed. The current implementation boundary and missing contracts are recorded in `docs/phase4-foundation-handoff.md`.

Keep Ubuntu 24.04, PHP 8.3, MariaDB 10.11, home `0751`, docroot `site:www-data 2750`, non-login site UID and private per-file/subtree modes. A traversable home is not private. Worker UID is the site UID; FPM socket owner/group is `www-data:www-data 0660`; reject `listen.acl_users` and `listen.acl_groups`. Keep compatibility PHP defaults, F15 typed execution and explicit environment, F6/F19 claims, admin socket authentication and application TCP `127.0.0.1`. No installer, host bootstrap, host service mutation, public CA, phpMyAdmin, additional PHP version or destructive WordPress operation.

Adopt WP-CLI 2.12.0 from `https://github.com/wp-cli/wp-cli/releases/download/v2.12.0/wp-cli-2.12.0.phar`, SHA-256 `ce34ddd838f7351d6759068d09793f26755463b4a4610a5a5c0a97b68220d85c`, signer fingerprint `63AF7AA15067C05616FDDD88A3A2E8F226F0BC06`. Download to quarantine, verify digest and detached signature against the separately reviewed official fingerprint, then install root-owned 0644 outside site homes at `/usr/local/lib/novapanel/wp-cli-2.12.0.phar`. Refuse execution if installed bytes or custody differ. The URL is versioned, not immutable. Reverify approved bytes, signature and reviewed official fingerprint for every acquired or installed artifact. Signature verification evidence is retained in /root/.hermes/cache/scratch/phase4-decision-review-zisocmmu/signature-verification.log and reverified in this packet; trust is official fingerprint over HTTPS plus valid signature, not independently certified web of trust. Do not invent a trust key, fetch an arbitrary key on mismatch, or call the PHAR executable just to inspect it.

Generate eight independent WordPress keys/salts locally with crypto/rand and store them only in the sealed operation until protected config publication. Use WP-CLI config create with `--skip-salts` and stdin DB password, then add the generated constants with the agent's typed config publisher before core install. Do not use --extra-php stdin, PHP eval or the remote salt fallback. wp-config is a no-follow, atomically published site-owned 0400 regular file under the site lock. Refuse an existing/unmanaged or nonempty docroot; restart may resume only files bound to its own journal. Adopt WordPress 7.1.4, locale en_US, official ZIP https://downloads.wordpress.org/release/wordpress-7.1.4.zip, SHA-256 a147146c159362778ecbbbfb07bb20ddf4e85b130f971c824e656a8f938c5f87, 37239942 bytes. Pin the full official metadata from https://api.wordpress.org/core/checksums/1.0/?version=7.1.4&locale=en_US. Retained ZIP matched all 3782 entries. Retained full official checksum JSON SHA-256 is bb718bddf197e0098f05395dd9b228fad70b394926ee9d70a8c6a67cf20781e7; preserve its exact bytes as the offline fixture alongside the archive, and retain the official runtime verification path. This SHA is a locally computed pin over official HTTPS bytes, not an upstream signed SHA. Retain the unchanged official wp core verify-checksums command as an additional runtime gate; do not replace it with our ZIP scan, change checksum source files, use --insecure, or assume PHP/WP-CLI compatibility without the later real lab. Candidate grant allowlist stays exactly the phase spec's DB-scoped list until real install and upgrade/schema acceptance for that release passes. Failure blocks release; it does not justify global privileges or plugin-driven permission expansion.

Add only `d /run/php/novapanel 0755 root root -` and reviewed dependency metadata to packaging. make release-stage still builds binaries and maintenance.tar, not units/accounts/dependency installation. Remove phase-4's obsolete home 0750, `or wp.install`, secret argv example, nightly-admin_password claim and all-verbs-jobs language. Remove host secure-installation execution from Phase 4. Only Slow mutations queue; ssl.auto_set stays synchronous.

Identifier quoting and privilege-pattern escaping are different operations. Validate generated identifiers, escape literal backslashes and `_`/`%` for the GRANT database pattern, then quote the identifier with the typed SQL renderer. Do not apply grant-pattern escaping to CREATE/DROP DATABASE identifiers. Example intent is a grant to literal d_abc, never dXabc. Inspect SHOW GRANTS and prove the application account cannot read a lookalike database whose underscore is replaced by a character, as well as an unrelated DB. Preserve exactly SELECT,INSERT,UPDATE,DELETE,CREATE,DROP,ALTER,INDEX,CREATE TEMPORARY TABLES,LOCK TABLES,REFERENCES. Do not silently change sql_mode or assume backticks remove wildcard semantics. Real MariaDB 10.11 tests must validate the chosen driver/renderer escaping under its actual mode before acceptance.

Defer the owner opt-in hardened PHP profile until a later owner-approved request shape, persistence model and compatibility-warning acceptance contract. Phase 4 exposes no hardened field, toggle or claim of support. Its verification and DoD require the compatibility default, cgi.fix_pathinfo=0, bounded limits, log confinement, UID/filesystem/socket/DB isolation and explicit UI statement that optional hardened PHP is unavailable. The optional feature is outside this phase's amended completion contract, not a silently skipped required test. Home 0751 enables traversal only; docroot site:www-data 2750 and private files/subtrees enforce confidentiality.

Shared contracts in 00, 01, 02 and 03 are mandatory. Recovery, standalone deletion identities, ordered locks, atomic job/claim/journal terminality and bounded expiry require crash, concurrency, reopen and plaintext-leak tests. Nonroot labs do not prove final Ubuntu VM checks. Full privileged acceptance requires both tiers in 06. Implementation/local-safe completion and the narrow stacked-development exception follow the delegated completion amendment and are not release approval.
