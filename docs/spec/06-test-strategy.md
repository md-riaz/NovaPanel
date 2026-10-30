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
Full privileged/release acceptance requires both tiers. Under the delegated completion amendment below, implementation/local-safe completion is a distinct qualified status and does not mark deferred checks passed.

[pebble]: https://github.com/letsencrypt/pebble

## Phase 4 acceptance amendment

Require actual Go SQLite fresh/upgrade/reopen tests for all five shipped migrations plus the new migration, deferred primary-user and same-site binding constraints, atomic terminal rollback, indexed bounded expiry, and immutable shipped hashes. Require lock-order concurrency and journaled standalone deletion replay; literal GRANT underscore lookalike refusal in real MariaDB 10.11; independent stdin password consumers and F19 publication TTL/leak tests; unchanged official WordPress checksum verification plus tamper refusal.

Optional hardened PHP is deferred until a later approved request shape and is not a Phase 4 completion check. Test that no unsupported field/toggle is exposed and that the UI states unavailability.

Nonroot isolated labs may prove parsing, real program execution, TCP/socket transport and application grants, never root@localhost peer authentication, root FPM UID switching, www-data ownership or multiple-site-UID denial. Those remain explicit final Ubuntu 24.04 VM checks along with paths/systemd/tmpfiles/reboot, append-only audit, public ports and power-loss. Do not run privileged containers as a substitute under this bounded authorization. The project-end privileged VM exception does not mark either phase fully verified. Standard integration still requires prior-phase publication/review/merge; the explicit stacked exception below permits only bounded independent development before those external dependencies finish.

## Delegated phase-completion amendment

Phase 3 candidate 82f76cc0ec17dfa8f3fc46a0536e371c5fe9b08f is implementation/local-safe acceptance complete subject to published exact-head architecture review and actual remote CI. The unresolved browser create/poll timeout risk is accepted for dependent development under the user's delegation, not dismissed as fixed. Parent approves the documented internal Phase 4 decisions. Final privileged Ubuntu VM checks are explicitly deferred until project-end acceptance and remain unpassed. Standard Phase 4 integration requires reviewed and separately authorized merged Phase 3 plus canonical amendment publication and an explicitly opened slice. No main merge is authorized by this packet. While external review waits, only a parent-recorded stacked-development exception may open isolated pure PHP/DB tests and their minimal implementations on this exact unmerged SHA. Such work is not merged prerequisite completion, canonical publication, deployment or full Phase 4 acceptance.

See `docs/phase3-completion-ledger.md` and `docs/phase4-implementation-opening.md` for exact remaining gates, accepted browser risk, test ownership and restrictions.
