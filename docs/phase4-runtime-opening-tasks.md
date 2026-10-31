# Phase 4 runtime-opening tasks and PHP transport acceptance

> Historical pre-merge record. Current authority and implementation status are in [Phase 4 foundation handoff](phase4-foundation-handoff.md). PR #81 is now merged at `2ff29a1a70f0ad678969d48006a4c21c183dd29a`; statements below about its pending merge or test-only authorization describe the earlier snapshot.

## Current authority

This branch starts at pure integration `cb6a49552242aa5947ab75523e6fa67a2b2ac693`. The parent opened only continued pure tests and confined fixture integration. No production migration, daemon wiring, PHP/DB installation, WordPress execution, prerequisite merge, push or PR mutation is authorized.

At the read-only GitHub check, PR #81 remained OPEN at `129714b0980f906d32774118f612e46368d04579`, mergeCommit null, and all returned checks successful. D1 SAN-expansion implementation/review belongs to the sibling worker. Green CI is not that review or a merged prerequisite.

Canonical planning commit `936d848024db3f38ff101757d2a71cb962879d56` remains the approved local packet. Main's #86 accepts additive reply fields and idempotent secret-input roll-forward only with an intent journal. Effectful secret-input recovery remains interrupted, with sealed input retained for recovery. Reconcile that F6 paragraph explicitly without replacing the approved Phase 4 documents wholesale. Preserve D2 trust-anchor changes. Actual merged main and amendment publication SHAs remain unset, not predicted.

## Runnable test-only PHP acceptance

```
python3 -m unittest discover -s tools -p test_phase4_php_nginx_lab.py -v
python3 tools/phase4_php_nginx_lab.py /path/to/nginx.deb
python3 tools/phase4_php_nginx_lab.py /path/to/nginx.deb --require-production
```

The package is Debian bookworm `nginx_1.22.1-9+deb12u9_amd64.deb` from `https://deb.debian.org/debian/pool/main/n/nginx/`, SHA-256 `4e7b52f8b31f26d77ffb5c427c41e36223e81b047242ecfa8dc1d7f939734885`. Acquire it separately into scratch. The launcher refuses any other bytes and extracts it inside the pinned PHP image without installing packages or running maintainer scripts. This is a byte pin over HTTPS, not independent Debian signature verification or target Ubuntu acceptance. No downloaded WordPress/WP-CLI code executes.

The launcher requires clean committed input and retains HEAD, exact rendered fixtures, command results, inspections and response hashes under TMPDIR. PHP uses the existing pinned 8.3 image. A single UID/GID 33 container runs nginx and FPM, network none, no ports, binds, devices or added capabilities, cap-drop ALL, no-new-privileges, read-only root, 512 MiB RAM/no extra swap, one CPU, 64 PIDs. Only bounded tmpfs are writable. `/tmp` permits executable extracted nginx; socket and log tmpfs remain noexec. Container-only tmpfs ownership is explicit, no host chmod/chown, root exec, docker cp or host mount is needed.

Exact PHP renderer bytes start FPM with the original `/run/php/novapanel/lab-site.sock` and log path. Globals alone are scratch settings. Socket stat is 33:33 0660. Current nginx renderer bytes change only listen ports for the production baseline. That static renderer returns PHP source for a PHP file, the expected missing PHP-routing requirement. It must never be deployed as a PHP-site renderer. The default run records PENDING rather than calling production complete; `--require-production` exits nonzero. Unexpected baseline failures are hard errors, not pending successes.

A separately labeled test-only nginx block then proves real HTTP execution, `cgi.fix_pathinfo=0`, exact SCRIPT_FILENAME, empty PATH_INFO, and 404 for `/missing.php`, `/uploads/hostile.jpg/missing.php`, and `/index.php/extra`. It uses `try_files $uri =404`, a fixed Unix socket, `SCRIPT_FILENAME $document_root$fastcgi_script_name`, explicit empty PATH_INFO and refusal of PHP path suffixes. This fixture is an executable acceptance specification, not an implemented Phase 4 nginx renderer. It is deliberately not production FastCGI parameter coverage, WordPress routing or a complete upload policy.

## Opening record required before runtime work

Parent must record all these fields after their actual events:

```
mode: phase4-runtime-integration
prerequisite_pr: 81
reviewed_exact_head: <D1-fixed published SHA and source review link>
remote_ci: <successful exact-head runs>
merge_authorization: <separate authorization>
merged_main_sha: <actual main after merge>
canonical_publication_sha: <actual reconciled amendment>
shared_contract_commit: <B implementation plus reviewed exact API signatures/tests>
allowed_slice: <B first; then explicitly A or C>
vm_status: deferred-unpassed
```

Before dispatch, rebase/reapply pure patches and this test-only patch, compare stable patch IDs and candidate SQL, inspect current migration inventory, and run uncached race/CI plus the confined real-engine tests on the resulting exact source. Do not mark a default fixture PASS as a production opening. No runtime handler may expose PHP sites while nginx is still static-only.

## Next workstreams, in dependency order

### B. Durable storage and queue contracts first

Own new `internal/store` DB/operation files, production migration/hash, and shared `internal/jobs`, Agent/policy/startup changes. A/C do not edit those paths concurrently.

1. After actual opening, allocate the next unused forward migration. Use 0006 only if still free. Do not modify shipped migrations. Add approved candidate tables/FKs/checks/expiry index and actual hash. Red/green fresh, upgrade, reopen, integrity, rollback and deferred-commit cleanup tests precede implementation.
2. Publish concrete compiled Go type/method signatures before consumers begin. Required responsibilities are prepare immutable reserved identities plus sealed recovery payload, load operation, compare-and-advance checkpoint, inspect resource blocker, expire one indexed unfinished-only batch of at most 100, reconcile failed/non-serving disposition, and typed atomic terminal commit. Names are not prescribed existing APIs. Current `jobs.Persistence` lacks these methods.
3. Terminal input must carry typed verified metadata changes, expected job/operation binding, sanitized result, optional already-sealed F19 claim and injected time. In one SQLite transaction publish metadata/job/result/claim/journal done and remove nonce/ciphertext. No separate PublishClaim then UpdateJob. Preserve completion audit policy and consumed-claim exception. Delete operations omit claims and retain immutable historical identities after resource rows disappear.
4. Extend the queue's single lock acquisition, not a second nested lock manager. Reserve create IDs/names before the complete site-first then DB bytewise lock set. Revalidate membership under locks, release in reverse. Site deletion holds site lock before enumerating attached DBs. Non-Phase-4 jobs keep singleton behavior.
5. Before mutation admission opens, expire unfinished operations using fixed preparation+24h lifetime and injected clock; repeat through existing scheduling once per minute. Completed rows cannot consume the 100-row budget. Expired unreconciled resources remain blocked/non-serving; never regenerate claims or continue old external effects. Integrate #86's recovery classes without destroying resumable ciphertext early.
6. Gate with real queue/dispatcher, reopen/crash at each effect/checkpoint/terminal boundary, concurrent PHP/DB/site locks, rollback with no visible claim, consume/expiry retry without republication, done-row starvation, FK job-retention refusal and SQLite/WAL/audit/result plaintext scans. Publish B's commit and exact API tests for A/C.

### A. PHP persistence, nginx publication and rollback second

Reuse real pure APIs `php.Render(php.Desired) ([]byte,error)` and `php.MergeLimits`; `nginx.Desired` currently has no PHP discriminator/socket input. Any typed extension is NEW, requires tests first, and must preserve static defaults.

1. Consume B's committed APIs. Add typed PHP desired state and validated site socket selection, no raw nginx/PHP passthrough. Project persisted limits rather than parsing config. Static php.set refuses; site.update PHP changes delegate to the same path.
2. First make production-route acceptance fail without fixture substitution. Implement PHP index/routing, missing-file guard, SCRIPT_FILENAME/PATH_INFO policy and upload-path tests, preserving ACME, hidden-file denial, redirects, maintenance, HTTP/TLS and both supported nginx HTTP2 grammars. Explicitly test no PHP source response and no hostile fallback. The current lab proves only its fixture policy, not these combinations.
3. Implement pool lifecycle and typed validate/publish/reload/probe/restore using existing runner and nginx apply recovery design. Persist merged limits only after verified publication, restore desired state on failure. Refuse symlink/custody violations and unmanaged pool/socket paths. Add only reviewed php8.3-fpm policy/tmpfiles metadata, not an installer.
4. Remove the fixture substitution from production acceptance when the real typed renderer exists. Require `--require-production` and all adverse-path probes against exact generated production bytes. Test failed FPM/nginx validation, reload, probe, LKG recovery and restart without returning a serving half-state.
5. Keep VM proof outstanding for root FPM dropping to distinct site UID, www-data socket custody, cross-site private-file denial, home 0751/docroot site:www-data 2750 and real systemd restart. Same-UID container success is transport evidence only.

### C. Lifecycle/WordPress/UI only after B and A

Own site create/delete/mutations/read projections and web callers; consume committed B/A APIs. Additional pure artifact trust tests may proceed only within the current test-only scope. Runtime WordPress execution is not opened here.

1. Reverify WP-CLI 2.12.0 digest/signature with reviewed fingerprint and installed custody, WordPress 7.1.4 en_US archive/full official checksum fixture; never execute PHAR for inspection or choose a new key on mismatch. Retained signature evidence is trust provenance, not execution compatibility.
2. Once runtime separately opens, exercise fixed typed argv as actual site UID, distinct stdin DB/admin password consumers, eight local salts, no allow-root/shell/eval/secret argv. Prove nonempty/tamper refusals, checksum command, no-follow atomic site-owned 0400 config, interrupted effect roll-forward and grant compatibility without permission expansion.
3. Bind verified installation through B terminal publication, preserve owner-bound one-shot F19 delivery and secrecy. Implement independent purge_files/purge_databases flags with snapshots of ALL attached DBs/accounts, retained-live-credential warnings and no unmanaged adoption. Add browser/read contract tests, compatibility-default warning and explicitly unavailable hardened PHP. Destructive WP ops stay unsupported.

## Observed evidence

At test commit `10fa612`, fixture transport passed in `/root/.hermes/cache/scratch/phase4-php-nginx-wb9zv55h`. Strict production mode intentionally exited 1 in `/root/.hermes/cache/scratch/phase4-php-nginx-du46248a`. Three oracle unit tests and uncached race tests for PHP/nginx/database/store passed. Later exact-source results are recorded in the parent handoff; these paths do not claim later commits were already tested.

Final Ubuntu VM identity/custody, root MariaDB unix_socket auth, real publication/recovery and WordPress installation remain unpassed. No production source or migration was changed by this acceptance work.
