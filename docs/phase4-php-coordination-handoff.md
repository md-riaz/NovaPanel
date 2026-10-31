# Coordinated PHP renderer handoff

## Recovered work and scope

Worktree `/root/.hermes/cache/scratch/NovaPanel-phase4-php-coordination-20261031`, branch `phase4/php-coordination-20261031`, starts at foundation `42dfcae0f9ec92989a5a82629c03a24a3ef0c8a2`. Runtime patches `9acce0b`, `c2e0399` and their transport handoff were replayed once as `c959c1a`, `2f50d87`, `10a479b`. The interrupted worker committed coordinator and offline WordPress trust documentation as `0d5b028`. Its untracked first-create regression was recovered without reset.

## Bounded API

`(*nginx.Applier).ApplyPHP(ctx, pools, desired, commit, restoredProbe)` holds the shared nginx lock and then the shared FPM lock. Caller must already hold ordered site locks. It stages both rendered configurations, validates both before any reload, reloads and probes FPM before nginx, then promotes both LKG files and calls the supplied metadata callback. Validation failure restores both files without reload. Later failure restores the prior LKG, validates both restored configurations, reloads FPM before nginx, and calls the restored-state probe only for a prior enabled vhost. It never probes a nonexistent first-create service. Recovery uses an independent ten-second context even when the request is cancelled. First-create rollback removes the new pool, vhost, LKG files and enabled link.

This is an in-process coordinator, not a durable cross-service transaction. The metadata callback must itself provide the approved atomic store/job/claim/journal publication before worker wiring. No journal is implemented here, and promoting files plus a callback does not prove metadata atomicity or crash recovery. No handler, queue consumer, migration or shared store code was changed. FPM validation covers the dedicated managed pool directory, not the complete system global configuration. Runtime consumers remain gated.

## Recovered independent findings

The interrupted read-only reviewer left `/root/.hermes/cache/scratch/phase4-independent-review-report.md`. It approved only the bounded foundation and reproduced one PHP defect: repeated proven applies always reloaded FPM, contrary to F7/F8. Its deferred-primary-FK regression passed and migration 0001 through 0005 remained unchanged. Its report is not whole-phase approval.

That no-op defect was reproduced again with tracked tests against this coordinator branch before correction. PHP now skips publication only when desired bytes match both live bytes and a separately proven LKG. Combined publication additionally requires an enabled vhost and matching nginx live/LKG bytes. The metadata callback still runs for an unchanged combined apply, so a caller can complete its own durable operation. Live-byte equality alone never skips publication. Tests require no command, probe or file replacement for a proven unchanged tuple; stale live bytes and regular temporary files retain the original LKG on failed retry.

## Verification and remaining gates

Focused uncached PHP/nginx race tests and `make ci` passed after the correction. CI includes build, formatting, vet, staticcheck, repository tests, uncached architecture checks, Python backstop, govulncheck and systemd verification. Govulncheck reported no called vulnerabilities and four vulnerabilities in required modules not reached by this code. The first-create and restored-validation failure regressions passed.

The original gates below are superseded only for same-UID transport and confined in-process rollback by `phase4-restored-service-probe-handoff.md`. The exact-production-byte confined PHP/nginx lab must be rerun on the committed handoff SHA and reported separately. It proves same-UID transport only. Actual coordinated service rollback under a running FPM/nginx pair, system-global FPM validation, durable cross-service crash/reopen recovery, root-FPM distinct UID/socket custody, cross-site private-file denial and privileged Ubuntu acceptance remain unpassed. No host services, public CA, push or PR mutation were performed. Phase 6 backup source is owned by the dedicated backup worker and was not started here.
