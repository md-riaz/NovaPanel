# Phase 3 acceptance map

Reviewed production source: `40cbb3bc4d508e7fc83dad299799ed8dca2884cf`. This is not whole-phase approval. Only tests, a lab launcher and this evidence map were added.

## Evidence and limits

- Existing exact-source evidence: `/root/.hermes/cache/scratch/acme-canonical-final-evidence/manifest.json`, `ci.log`, `race.jsonl`, `integration.log`.
- Independently parsed race output contains 290 unique top-level test passes, 388 unique subtest passes and 24 passing packages, no failures. Three opt-in integration cases were skipped in that run. Those are not 678 independent top-level tests.
- Existing real-nginx evidence: `/root/.hermes/cache/scratch/phase3-realnginx-flxunswt/acceptance.log`, two cases repeated three times. Includes manual TLS identity, render/probe rollback and successful/failed primary rekey.
- New real Pebble evidence: `/root/.hermes/cache/scratch/phase3-pebble-40cbb3-evidence/`. Manifest records exact source, binary hash, images and exit. `pebble.log` proves genuine ACME authorization, one account, three issued orders and ARI replacement on both renewals. `acceptance.log` records real nginx challenge GETs by Pebble returning 200, versioned publication, duplicate-issue no-op, renewal, SAN expansion and staging force-HTTPS refusal.
- The new lab runs the real production RPC dispatch and queue, ACME coordinator, lego library, durable SQLite/account policy and certificate publication. No CA executor or probe is replaced. The existing test runner translates production nginx commands into scratch-local real nginx commands and ports 8080/8443. It does not run systemd. No production defaults are accessed.
- Both servers run in one nonroot container with network `none`, loopback only, capability drop ALL, no-new-privileges, no mounts or published ports, 512 MiB RAM, 1 CPU, 64 PIDs. No private bridge is needed. Actual public :80 binding and Ubuntu 24.04 nginx 1.24 are not established by nginx 1.29.5 on Alpine with test port overrides.

## Deliverables and gates

| Canonical requirement | Artifact and exercised assertion | Status |
|---|---|---|
| Phase 3 D1: sites/domains, synchronous reads, queued mutations | `internal/store/sites*`, `internal/verbs/site*`, `domain*`; passing `TestSiteReadProjection`, `TestSiteMutations`, `TestQueuedSiteCreate`, store read/lifecycle tests | Safe logic covered; real account lifecycle remains VM-deferred |
| D2 / F8 / gates 1,3: structured fields, hostile sink rejection, validation/reload/probe/LKG rollback | `internal/render/nginx`; passing render refusal/apply tests; exact-source real nginx `TestRealNginxRenderPublishRollback` asserts syntax failure never reloads and real failed probe restores serving | Covered in safe lab with test port/path adapter |
| D3 / gate 2: static creation, per-site Unix user and 0751 home / 2750 site:www-data docroot | `internal/platform/sites.go:79-151`; `TestQueuedSiteCreate` injects provisioner; platform tests inject getent/useradd and skip privileged ownership when nonroot | Partial: source + unit seam, not real multi-UID ownership/useradd/served create journey |
| D3: reject php/wordpress in Phase 3 | `internal/verbs/site.go`; queued-create refusal tests | Covered at unit seam; PHP/FPM itself correctly later-phase |
| D4 / gate 4: real ACME HTTP01 and atomic pair publication | `internal/acme/client.go`, `webroot.go`, `internal/verbs/ssl_acme.go`, `ssl_publication.go`, `internal/tls`; new `TestRealPebbleNginxJobs` obtains from genuine Pebble through real nginx, validates stored pairs/current and metadata, renews/reloads, expands SANs | Covered locally, not public :80/LE acceptance |
| F6 effect-specific issuance test / phase lines 152-155 | New Pebble job test verifies first version and second issue returns unchanged without reload; Pebble logs establish exactly three orders for issue + renew + expanded renew, not duplicate issue | Covered |
| Force-HTTPS/HSTS installed validity and all-domain coverage | `internal/verbs/site_mutations.go`, nginx TLS loading/rendering; `TestTLSAwareSiteMutations`, `TestTLSRenderRefusals`, new Pebble staging refusal, prior real-nginx manual TLS force-HTTPS case | Covered safe logic and real manual TLS rendering; privileged delivery boundary deferred |
| Gate 5: `ssl.issue{wildcard:true}` returns unsupported without DNS provider | Canonical `03-verb-shapes.md:70`; implementation `sslACMEArgs` has only site_id/staging at `ssl_acme.go:21-24`; shared decoder rejects unknown fields | Hard contract gap: canonical flag is missing, resulting bad_request instead of unsupported. Wildcard-domain checks in lower ACME layers do not implement this RPC shape |
| Gate 6 / F6: all mutations queued, audited, per-site serialized | Registry tests, `TestKeyedLockSerializesAndReleasesReferences`, site mutation tests and `TestACMEWorkerCanonicalResults`; real Pebble production queue completes jobs with canonical results | Covered by separate assertions, not a new exhaustive every-verb combined concurrency/audit matrix |
| Structured redirects, subdomains, maintenance | Domain/redirect/site verbs, render tests, `TestTLSAwareSiteMutations`, prior real nginx maintenance HTTP503 and challenge bypass | Covered at safe seams; not whole browser acceptance |
| Manual upload and F6 sealed input | `TestSSLUploadSealedAndPublished`, `TestSSLProductionWorker`, sealed worker tests; real nginx manual cert publication/recovery | Covered; real owner/group enforcement VM-deferred |
| Domain add on forced HTTPS / manual uncovered domain stays HTTP | `TestTLSAwareSiteMutations`, `TestCoveredAndUncoveredServerIsolation`; new Pebble job adds alias and renews full SAN set | Covered separately. New Pebble scenario is staging and cannot legitimately force HTTPS |
| Primary promotion, removal safeguards and deletion order | `TestTLSPrimaryPublicationExecutor`, `TestSiteMutations`, store atomic promotion tests, prior real-nginx primary rekey case | Covered safe seams; real account purge VM-deferred |
| F18 daily renewal, <=30-day window, manual/disabled exclusion and expansion | `internal/jobs/acme_schedule.go`, `TestAutomaticACMERenewalDailyPersisted`; CAS/stored authority/busy audit scheduler tests | Unit/store covered; scheduler-to-Pebble timed firing is not the new test, which dispatches renewal directly |
| F16 configurable directories/email/environment, separate sealed accounts and persistent budgets | Config loading, `acme.NewPersisted`, account/policy tests; new Pebble uses nondefault local directory with default staging arg | Covered at component seams; new local config is injected, not TOML-launched production daemon |
| Startup/recovery and browser | `TestTLSQueueStartupReopensOriginalACMEJob` passed in prior race run, but full v2 startup/browser sibling work is explicitly pending; manifest marks browser/assets not run | Open hard integration gate, not resolved by Pebble |

## Remaining categories

### Executable safe gaps and defects

1. Fix and regression-test missing canonical wildcard argument through RPC admission. This audit does not edit core source.
2. Integrate pending sibling startup/browser fixes and run browser/assets acceptance against that final committed combined source. Current evidence cannot approve a future SHA.
3. Additional safe coverage can join F18 scheduler firing to real Pebble and test the combined per-verb audit/concurrency matrix. Existing separate component passes must not be described as those combined scenarios.
4. Run the pinned Ubuntu nginx 1.24 compatibility path in an equally confined lab when a suitable image/tool is available. Current real lab tests nginx 1.29.5 and emits accepted legacy-http2 deprecation warnings.

### Required acceptance deliberately deferred to the authorized final VM

- Actual root daemon startup/systemd service lifecycle, boot/reboot and installation.
- Real useradd/userdel, per-site UID/GID, www-data readability and non-writability, purge and dormant retained account behavior.
- Actual production-path ownership, certificate key secrecy from other UIDs, peer ownership transitions and append-only audit enforcement.
- Real :80/:443 serving and distribution-specific nginx/systemd integration. No host service or network change was attempted here.
- Process-kill/power-loss at all durability boundaries is not established by reconstructed/panic recovery fixtures.

Public CA requests were not authorized or attempted. Any final external staging/production issuance needs separate approval and a real controlled domain. This local Pebble result does not claim public-CA acceptance.

## Reproduction

`tools/pebble_nginx_lab.py` expects the recorded pinned nginx image and the real Pebble executable extracted from `ghcr.io/letsencrypt/pebble@sha256:ddf230642b1a584f519f32e347de1b05a6e4c1f6c35c1863b33effeab5f78199` at the evidence directory's `pebble` path. It compiles a CGO-free test with the existing Go toolchain, generates an ephemeral localhost API certificate, verifies isolation before/after execution and verifies owned-container absence after teardown. The API certificate is a local trust anchor only; issued site certs come from Pebble. Original startup/ownership harness failures remain in commands.log and are not product defects.
