# Phase 4 bounded dependency map

Planning only. Do not implement this map until Phase 3 is review-ready and the
next implementation scope is authorized. The controlling contract is
[Phase 4](spec/phases/phase-4.md), with foundations F6–F12 and F15.

## Entry prerequisites

- Pin the combined Phase 3 source and retain CI, uncached race JSON, safe root
  integration, auth/console Chromium, assets, packaging and confined nginx/Pebble
  evidence. Separate skips, intentional worker failure and deferred VM gates.
- Resolve any open core TLS/startup/security review findings before using those
  workflows as a Phase 4 dependency. This map neither approves nor changes them.
- Confirm the static-site ownership model, job claim secrecy, typed exec privilege
  drop, per-site locks, mutation admission/recovery and audit contracts.
- Resolve Phase 4's conflicting home modes before coding: its opening blueprint
  and verification say 0750 while the later Phase 3 alignment says home 0751 and
  docroot `site:www-data 2750`. Preserve nginx traversal without cross-site reads.
- Pin target Ubuntu/PHP 8.3, MariaDB, WP-CLI and WordPress versions. Resolve
  WP-CLI admin-password stdin support, unique salt generation and the exact
  least-privilege WordPress grant list. Do not infer answers from example argv.

## Dependency order and acceptance

| Slice | Depends on | Bounded deliverable | Required proof |
|---|---|---|---|
| A. Contracts and schema | Entry review and decisions | Define typed PHP/DB/WP args, read projections, durable job metadata and secret claims without plaintext durable credentials | Registry/auth/input/refusal and migration tests; mutations queued and audited |
| B. Site identity and exec | A, Phase 3 provisioning, F15 | Reuse non-login site identity; typed argv/stdin; explicit uid/gid/groups; no sudo, su, shell or allow-root | Disposable multi-UID tests prove cross-site denial and effective child credentials; no secret argv |
| C. PHP 8.3 pools | A+B, existing nginx publisher | One supported version, per-site pool/limits, tmpfiles socket directory, transactional validate/reload/recovery | Worker uid is site uid; socket is www-data-owned 0660; nginx serves PHP without 502; failure restores known-good config |
| D. MariaDB lifecycle | A+B, show-once claim | Admin root via unix_socket; generated names; per-site DB/user; TCP 127.0.0.1 app transport; no global grants | Real scratch MariaDB isolation, grant inspection, transport mismatch refusal, rotation recovery, single-use password claim and leak scan |
| E. WordPress install | B+C+D | Empty-docroot-only install as site uid; pinned download and checksum verification; password via stdin; unique salts; config mode 440/400 | Real disposable install plus tamper/nonempty refusal, file ownership, no secret persistence and failure cleanup/recovery |
| F. Web and combined review | C+D+E | Canonical UI/read/job contracts, explicit optional hardened-profile warning | Real-backend Chromium, safe CI/race, multi-UID nginx/FPM/DB/WP acceptance tied to one source SHA |

DB rotation must reconcile the DB password and wp-config in one recoverable job;
a crash between writes needs an explicit recovery decision before implementation.
Use the existing sealed-input/claim approach where its contract fits; do not add
a parallel credential store or expose passwords through job results or audit.

## Scope limits

- Default PHP compatibility profile keeps child-process functions available and
  omits open_basedir. Unix identities, permissions, sockets and DB grants enforce
  isolation. Optional restrictions require a tested compatibility warning.
- No destructive WordPress update, clone or staging before Phase 6 restore.
- No multiple-PHP-version rollout, phpMyAdmin, installer, production deployment,
  public ports, host service changes or public CA calls in this bounded map.
- Final privileged VM acceptance remains a separately authorized activity. A
  confined Alpine nginx lab is not Ubuntu systemd/FPM/account acceptance.
