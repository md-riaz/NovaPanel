# PHP runtime renderer slice handoff

Base: merged Phase 3 `2ff29a1`. Pure PHP source replayed as `0038e10`; its acceptance note as `b7429de`. Runtime commits are `9acce0b` and `c2e0399`. No shared store/schema/jobs/agent/web files changed, and no push or PR mutation occurred.

## Implemented

- `nginx.Desired.PHP *php.Desired` validates site name binding, PHP 8.3, resolved nonzero UID/GID and limits through the pure PHP renderer. Socket paths are generated, not raw input.
- Static sites deny PHP-like requests without a FastCGI route. PHP sites use index.php, an existing-file guard, exact SCRIPT_FILENAME, empty PATH_INFO and explicit FastCGI parameters. Hidden files and uploads/files PHP are denied before interpreter routing. Maintenance and force-HTTPS servers do not run PHP.
- `php.Applier{Root,Runner,Probe}.Apply(ctx,php.Desired)` serializes pool publication, refuses symlinks, writable/foreign custody and unmanaged initial pool adoption, atomically writes/fsyncs, validates via `/usr/sbin/php-fpm8.3 -t --fpm-config <root>/novapanel-validate.ini`, reloads with the existing runner, probes and promotes LKG only on success. Failed validation restores disk; reload/probe failure restores original LKG and validates before recovery reload. Dedicated managed directory is required.

## Verified

At `c2e0399`, `go test ./... -count=1`, PHP/nginx uncached race tests, targeted vet, `make build arch` passed. RED failures were observed before static denial, typed PHP routing, publication, writable-custody refusal and unmanaged refusal implementations.

`python3 tools/phase4_php_runtime_lab.py /root/.hermes/cache/scratch/phase4-nginx-bookworm.deb --require-production` passed using exact renderer bytes. Evidence: `/root/.hermes/cache/scratch/phase4-php-nginx-5fhmhrhg`. PHP executed for `/index.php` and `/`; six missing/upload/path-info/case probes returned 404 without source or hostile execution. Lab retained confinement inspections and removed its container. No fixture FastCGI replacement is used.

## Integration still required

This is a bounded renderer/publication API, not completed runtime integration. Consume foundation worker's committed contracts before exposing PHP handlers. Persist merged limits only after combined verified publication. Coordinate original nginx and FPM snapshots under shared publication/site locks, then commit metadata; current separate APIs do not provide atomic cross-service rollback. Add crash/reopen and actual FPM/nginx reload/probe failure lab acceptance for that combined coordinator. `php.Applier` currently restores/reloads but does not probe restored service or implement a durable cross-service transaction. Validate the system global FPM configuration as part of final wiring; this API validates its dedicated managed pool set only. Provision and verify runtime socket directory custody separately, and add reviewed packaging policy without installers.

No canonical document packet was applied to this branch because the parent setup worker owns canonical reconciliation/publication. Pure acceptance note still describes its historical unmerged base and must not be used as current opening authority.

Final root-FPM distinct UID, socket custody, cross-site private-file denial, real systemd restart and privileged Ubuntu VM acceptance remain unpassed. TLS/HTTP2 combinations retain existing package regression coverage but were not exercised as real TLS PHP transport in this lab.
