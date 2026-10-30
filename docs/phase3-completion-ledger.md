# Phase 3 completion ledger

Candidate: `82f76cc0ec17dfa8f3fc46a0536e371c5fe9b08f`.

## Decision under delegated authority

Accept implementation and local-safe acceptance with the disclosed intermittent Chromium failure risk. Final Phase 3 dependent-development signoff remains conditional on completed-source architecture review of the published exact head after actual remote CI. Do not claim that review or merge has occurred. The parent publication worker owns the existing draft PR #81 update and review request; no duplicate publication or new policy questionnaire is needed.

Canonical 06's unqualified both-tiers completion rule is not silently satisfied. This amendment distinguishes implementation/local-safe completion from final privileged acceptance and production release. Final VM checks remain mandatory at project end, not blockers for every dependent development task.

## Executed safe requirements

Evidence: `/root/.hermes/cache/scratch/p3-final-82f76cc0-pkuilnff/manifest.json`. Logs and exact SHA were rehashed by this packet's validator.

| Requirement | Evidence and disposition |
|---|---|
| Model, static create, read projections, queued mutations and refusals | Exact-head CI, integration, race and real-backend console pass; actual account creation is deferred below. |
| Structured nginx and hostile inputs | CI/race plus real nginx 1.24.0 and 1.29.5 render/publication/rollback and primary rekey tests, repeated three times per version. |
| ACME HTTP01, pair publication, renew, SAN expansion and staging force-HTTPS refusal | Both real Pebble launchers passed against exact-source binaries; original scenario has three orders, scheduler scenario four orders. No public CA request is implied. |
| Audit/concurrency and durable principal | Exact-head queued actor/principal regressions and independent legacy-scope token overlay pass; scheduler matrix covers queue/mirror fields, one intentional nginx rejection is expected, not a suite failure. |
| Startup/recovery, primary promotion, manual TLS and sealed inputs | Exact-head CI, race, integration and confined real-nginx assertions pass. They do not establish all kernel durability boundaries. |
| UI, packaging and maintenance | Auth browser, real-backend console, assets, packaging and release staging pass. Staging is not installation. |

The uncached race run has 314 unique top-level passes, 478 unique subtest passes and 24 passing packages. Six opt-in/helper skips remain recorded; integration and both lab launchers separately execute the relevant opt-in gates. Do not add top-level and subtest counts into a claim of independent tests.

## Browser risk accepted only for development

The earlier exact-SHA full race run failed once at the create-stage 15-second browser poll assertion. The cause remains unknown because that run retained insufficient HTTP/job/DOM evidence. The succeeding full safe run and diagnosis found 21 further unchanged browser executions passing, including contention and full-suite race. Passing repeats do not erase the failure. Do not weaken the assertion or increase the timeout. Add bounded redacted job/poll diagnostics when changing this path; reopen as a source blocker on a reproducible failure or concrete lost-job/poll evidence. Source: `/root/.hermes/cache/scratch/p3-browser-diagnose-82f76cc0-e4env5a9/report.md`.

## Exact remaining musts before dependent-development signoff

1. Publication worker reads back PR #81 at the accepted exact SHA, revised body, draft state and auto-merge off after its authorized non-force update.
2. Retain actual remote CI conclusions for that head, including failures/skips and logs. Local CI is not remote CI.
3. Obtain completed-source architecture review tied to that published SHA, resolve source/security findings and rerun affected exact-head acceptance. An old six-question ruling or a review request is not completed-source approval.
4. Record final result as implementation/local-safe complete with browser risk accepted and the enumerated final-VM exception. Keep production/release verification incomplete.

Standard Phase 4 integration additionally requires separately authorized actual merge and amendment publication. This assignment authorizes neither merge nor source application.

## Owner final-VM acceptance, deferred and not passed

- Ubuntu 24.04 root daemon startup, systemd install/start/stop/reboot and real production path/custody checks.
- Real useradd/userdel, multiple site UIDs/GIDs, known-path cross-site denial, www-data read-but-not-write, dormant retained accounts and actual purge.
- Certificate-key secrecy between UIDs, peer ownership transitions and append-only audit enforcement on suitable storage.
- Literal public 80/443 serving and distribution-specific nginx/systemd behavior.
- Process-kill and true power-loss at every required durability boundary beyond reconstructed/panic and existing targeted kill fixtures.
- External staging/production CA acceptance only with separately authorized controlled-domain access. No credentials/domain are needed for independent local development.

PHP root-FPM switching/socket ownership and MariaDB root@localhost peer authentication are Phase 4 final-VM extensions, not missing Phase 3 implementation.

## Non-blocking historical gaps

The checked-in acceptance map's historical scheduler/1.24 gaps are superseded by the exact-head scheduler Pebble and both nginx results. Do not carry them as unimplemented requirements. Optional hardened PHP is expressly outside amended Phase 4 scope; no extra policy question or invented toggle. Final VM cannot block pure render/schema/contract tests under an explicitly recorded stacked exception.
