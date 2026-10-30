# 06 — Test strategy & CI seams

Resolves the review's recurring "this verification can't run in CI" problem (root, `chattr +a`,
systemd, a reachable ACME domain). Two tiers; every phase's Verification gate marks each check as
**[unit]** or **[integration]**.

## Tier 1 — unit (runs in stock CI, non-root, no network)

Pure logic and anything made testable by a **seam**. The code never calls a privileged primitive
directly; it goes through an interface the test can substitute:
- `exec.Runner` is an interface; tests inject a fake that records argv and returns canned
  stdout/exit. The no-shell arch test greps that nothing calls `os/exec` outside `internal/exec`.
- Privilege checks use injectable `geteuid`/`getuid` (a package var `var geteuid = os.Geteuid`); the
  no-root-web test overrides it.
- Filesystem ops take a root path; tests use a temp dir. Path-traversal/symlink/archive-escape tests
  (F9) run fully in unit.
- Renderers are pure (`desired → bytes`); hostile-input tests are unit. `nginx -t`/`php-fpm -t` are
  run via the fake `exec` in unit, and for real in integration.
- The **negative arch tests** ("demonstrably fails when the invariant is violated") run as a
  subprocess `go test` / `go vet` against a **fixture module** under `testdata/` that contains the
  violation, asserting a non-zero exit — so the main suite stays green while proving the guard bites.
- SQLite is real in unit (temp file DB); migrations, job state machine, audit rows, seal round-trip,
  session/token logic all run here.

## Tier 2 — integration (root, in a disposable container/VM; a separate CI job or on-demand)

What genuinely needs the kernel/systemd/network. Runs in a privileged container (GitHub Actions
`container:` or a VM), never on the dev host:
- `SO_PEERCRED` peer-gate, real socket perms, `systemd-analyze verify`, unit start, `chattr +a`
  append-only enforcement (needs a backing FS that supports it — ext4 in the VM, not tmpfs).
- Real `nginx -t` + reload + the apply→probe→revert loop; real PHP-FPM socket serving; the 502
  isolation test; MariaDB least-privilege (one DB user can't read another).
- **ACME against [pebble]** (the Let's Encrypt test server) with a local DNS/host override — never the
  real LE endpoints in CI; manual staging runs for a final check.
- restic backup+restore into a fixture repo; the UFW safe-apply deadman (in a VM, asserting the timed
  revert restores access).

## Fixtures & tools

- `testdata/` holds hostile names, crafted zip/tar (slip entries), malformed RPC frames, bad
  migrations, and the arch-violation fixture module.
- Pinned CI tools: `staticcheck`, `govulncheck`, `gofmt`, `go vet`, plus `pebble` and a MariaDB/nginx
  image for Tier 2. Versions pinned in the workflow; `govulncheck` needs network (Tier 2 or a cached
  DB).

## CI layout

```
unit job        (every push): build, vet, staticcheck, gofmt, go test ./... (Tier 1), arch tests
integration job (every push to main / nightly / label): Tier 2 in a privileged container/VM
supply-chain    : govulncheck; systemd-analyze verify on packaged units
```
A phase is "done" only when **both** tiers' checks in its Verification gate pass.

[pebble]: https://github.com/letsencrypt/pebble
