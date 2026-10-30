# Phase 4 bounded dependency map

Phase 3 candidate 82f76cc0ec17dfa8f3fc46a0536e371c5fe9b08f is implementation/local-safe acceptance complete subject to published exact-head architecture review and actual remote CI. The unresolved browser create/poll timeout risk is accepted for dependent development under the user's delegation, not dismissed as fixed. Parent approves the documented internal Phase 4 decisions. Final privileged Ubuntu VM checks are explicitly deferred until project-end acceptance and remain unpassed. Standard Phase 4 integration requires reviewed and separately authorized merged Phase 3 plus canonical amendment publication and an explicitly opened slice. No main merge is authorized by this packet. While external review waits, only a parent-recorded stacked-development exception may open isolated pure PHP/DB tests and their minimal implementations on this exact unmerged SHA. Such work is not merged prerequisite completion, canonical publication, deployment or full Phase 4 acceptance.

The controlling contract is [Phase 4](spec/phases/phase-4.md), with foundations F6-F12 and F15.

## Entry prerequisites

- Pin the combined Phase 3 source and retain CI, uncached race JSON, safe root
  integration, auth/console Chromium, assets, packaging and confined nginx/Pebble
  evidence. Separate skips, intentional worker failure and deferred VM gates.
- Resolve any open core TLS/startup/security review findings before using those
  workflows as a Phase 4 dependency. This map neither approves nor changes them.
- Confirm the static-site ownership model, job claim secrecy, typed exec privilege
  drop, per-site locks, mutation admission/recovery and audit contracts.
- Canonical home is 0751 and docroot is site:www-data 2750. Preserve traversal without cross-site file reads using private modes.
- Pin target Ubuntu/PHP 8.3, MariaDB, WP-CLI and WordPress versions. Resolve
  WP-CLI admin-password stdin support, unique salt generation and the exact
  least-privilege WordPress grant list. Do not infer answers from example argv.

## Dependency order and acceptance

| Slice | Depends on | Bounded deliverable | Required proof |
|---|---|---|---|
| A. Contracts and schema | Entry review and decisions | Define typed PHP/DB/WP args, read projections, durable job metadata and secret claims without plaintext durable credentials | Registry/auth/input/refusal and migration tests; Slow mutations queued and all mutations audited |
| B. Site identity and exec | A, Phase 3 provisioning, F15 | Reuse non-login site identity; typed argv/stdin; explicit uid/gid/groups; no sudo, su, shell or allow-root | Disposable multi-UID tests prove cross-site denial and effective child credentials; no secret argv |
| C. PHP 8.3 pools | A+B, existing nginx publisher | One supported version, per-site pool/limits, tmpfiles socket directory, transactional validate/reload/recovery | Worker uid is site uid; socket is www-data-owned 0660; nginx serves PHP without 502; failure restores known-good config |
| D. MariaDB lifecycle | A+B, show-once claim | Admin root via unix_socket; generated names; per-site DB/user; TCP 127.0.0.1 app transport; no global grants | Real scratch MariaDB isolation, grant inspection, transport mismatch refusal, rotation recovery, single-use password claim and leak scan |
| E. WordPress install | B+C+D | Empty-docroot-only install as site uid; pinned download and checksum verification; password via stdin; unique salts; config mode 0400 | Real disposable install plus tamper/nonempty refusal, file ownership, no secret persistence and failure cleanup/recovery |
| F. Web and combined review | C+D+E | Canonical UI/read/job contracts, explicit optional hardened-profile unavailability | Real-backend Chromium, safe CI/race, multi-UID nginx/FPM/DB/WP acceptance tied to one source SHA |

DB rotation must reconcile the DB password and wp-config in one recoverable job;
a crash between writes needs an explicit recovery decision before implementation.
Use the existing sealed-input/claim approach where its contract fits; do not add
a parallel credential store or expose passwords through job results or audit.

## Scope limits

- Default PHP compatibility profile keeps child-process functions available and
  omits open_basedir. Unix identities, permissions, sockets and DB grants enforce
  isolation. Optional restrictions are deferred until a later approved request shape; test unavailability rather than invent a toggle.
- No destructive WordPress update, clone or staging before Phase 6 restore.
- No multiple-PHP-version rollout, phpMyAdmin, installer, production deployment,
  public ports, host service changes or public CA calls in this bounded map.
- Final privileged VM acceptance remains a separately authorized activity. A
  confined Alpine nginx lab is not Ubuntu systemd/FPM/account acceptance.

## Approved internal contract decisions

The scratch authoritative decision packet fixes 24-hour sealed recovery, atomic job/claim/journal done, generated-only names, independent deletion flags, complete ordered locks after identity reservation, standalone deletion snapshots in the same journal, grant-pattern escaping, PHP limits and artifact pins. These become canonical only when this amendment is published; they do not open code. WP-CLI 2.12.0 and WordPress 7.1.4 en_US byte pins are specified in phase-4 and packaging-reference.
