# Restored PHP service probing

Built on combined foundation `f30a212`. Executable validation revision is `1436b0e`; later handoff-only changes do not alter the tested code.

## Corrections and stable callback contract

- Standalone `php.Applier.Apply` previously restored, validated and reloaded the old pool without probing it. It now calls its probe after successful recovery reload, with the existing independent ten-second context, only when a proven prior pool exists. Probe errors join the original publication failure. The typed desired identity remains unchanged; a probe must not infer the old quota tuple from the new desired limits.
- `nginx.Applier.ApplyPHP(ctx, pools, desired, commit, restoredProbe)` retains its signature. The caller holds ordered site locks; the coordinator holds nginx then FPM locks. `commit` runs after both desired probes and LKG promotion, including on a proven unchanged apply. `restoredProbe` uses prior metadata captured by the caller and must verify both services after successful rollback validation and reload. It is not called for validation-only rejection, failed recovery validation/reload, or first-create absence. First-create cleanup still removes pool, vhost, LKG and enabled link.
- Recovery does not change certificate pointers. Phase 3 TLS publication retains certificate custody; this change adds no duplicate TLS switch or certificate rollback implementation.

## Verification

- TDD red reproduced the missing standalone restored probe and the erroneous first-create coordinated probe; both regressions pass after correction.
- `make ci` and uncached `go test -race ./... -count=1` pass with 240-second outer bounds. Logs are `/root/.hermes/cache/scratch/phase4-restored-probe-final-ci.log` and `phase4-restored-probe-final-race.log`. Govulncheck reports no called vulnerabilities, with four in required modules not called by the code.
- `python3 tools/phase4_php_coordinated_lab.py /root/.hermes/cache/scratch/phase4-nginx-bookworm.deb` passes twice at `1436b0e`. Evidence is `/root/.hermes/cache/scratch/phase4-coordinated-0c_o5n9g` and `phase4-coordinated-4qut2loh`.
- The lab invokes the real production coordinator and pool/vhost publication. A confined command adapter runs real PHP 8.3.31 and nginx 1.22.1 validation/reload commands, then injects one cancellation/failure at each of FPM/nginx validation, reload, probe, and metadata callback. These are injected failures after real command execution, not claims that the service binaries themselves rejected bad syntax. Every case verifies exact original live/LKG bytes, enabled link and executed `old:256M` response. Candidate FPM probing verifies `old:512M` through the prior route, candidate nginx probing verifies `new:512M`, and rollback probing verifies both old routing and old PHP quota together.
- Reload signals return asynchronously. The adapter allows 300 milliseconds after signaling before injection; HTTP probes still retry with a five-second bound. An earlier lab failed because a valid old response raced unfinished nginx rollback. That failed evidence remains in `phase4-coordinated-aw_1loyk`; it is not counted as a pass. Earlier harness-only failures remain in `phase4-coordinated-94avmtzm` and `phase4-coordinated-fo3a28x4`.
- Exact-production-renderer transport acceptance also passes at `1436b0e`, including executed index/directory index and hostile suffix/upload denials, with evidence `/root/.hermes/cache/scratch/phase4-php-nginx-ujyk3aa2`.
- Both labs inspect confinement before/after and at teardown, use UID 33, network none, read-only root, no capabilities/host binds/published ports, bounded tmpfs/memory/CPU/PIDs, and remove only their owned container. Teardown reads back container absence. No host service, public CA or shared branch was changed.

## Remaining gates

This is in-process rollback and same-UID real-service acceptance, not a durable transaction. No persisted full PHP/nginx crash journal, power-loss/reopen recovery, atomic store/job/claim/journal callback, worker wiring, system-global FPM validation, root-FPM distinct UID/socket custody, cross-site private-file denial, HTTPS certificate recovery or privileged Ubuntu acceptance is proven. Store/jobs and sibling recovery workers remain untouched. The confined lab does not separately validate first-create recovery with an otherwise-running global FPM service; unit tests verify deletion and suppression of a nonexistent prior-service probe.
