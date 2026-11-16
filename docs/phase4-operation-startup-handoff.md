# Phase 4 operation startup boundary

Base: `f30a212145cd7b053c9f1612548d16982f10831b`. This is a bounded store/jobs change, not permission to enable runtime consumers.

## Implemented

- `Queue.PrepareOperations(ctx) error` closes operation admission, drains expiry in transactions of at most 100 until empty, restores ordinary interrupted-running job state, and executes each recoverable queued operation through the queue's existing complete ordered locks and terminal transaction. Any failure/cancellation leaves admission closed. Unresolved failed journals block startup rather than being silently adopted.
- `Queue.Run` performs this boundary before normal dequeue. `EnqueueOperation` refuses before the boundary succeeds and shares its admission mutex. Only operation admission is gated; this does not claim a global RPC mutation gate or a listener-publication barrier.
- `Store.PendingOperations(ctx) ([]Job,error)` reads at most 100 pending journals per pass. Existing expired tombstones remain resource blockers until reconciliation.
- `OperationCredentials{Password string}` is the credential-only DB payload. Existing map admission accepts exactly one nonempty string password, refusing arbitrary recovery/config fields. The sealed versioned envelope binds job, kind, database, owner and the full durable authority snapshot. `OpenOperationCredentials(ctx,jobID)` strictly decodes and checks that envelope after current eligibility/expiry validation. Queue preflight verifies secret-bearing inputs before invoking the runner; terminal delivery projects only `{password}`.
- Real SQLite/file-audit close/reopen queue tests cover create, primary rotation and secondary-user creation with local-root, disappeared session and disappeared token authority, deliberately different current owner context, owner-bound one-shot password claims, sanitized file records and no identity elevation. Existing standalone-delete restart tests remain intact.
- Added a 205-operation exact-deadline startup drain, actual SQLite trigger failure on journal-done through `Queue.Run` with atomic metadata/claim/job rollback and reopen retry, real held SQLite transaction startup cancellation, and sealed-authority tampering refusal before runner invocation.

## Verification

- Observed RED for pre-start admission, arbitrary recovery fields, envelope recovery, authority tampering and preflight refusal; all targeted tests pass after implementation.
- `go test -race ./... -count=1` passed after the production changes.
- Expanded final create/rotate/dbuser authority matrix passed `go test -race ./internal/jobs ./internal/store -count=1`.
- Final `make ci` passed build, format, vet, staticcheck, tests, architecture negative fixtures/backstop, govulncheck and systemd verification. Govulncheck reported no reachable vulnerabilities and four required-module advisories not called by this code.
- `git diff f30a212 -- internal/store/migrations` is empty. No migration/hash edits.
- Toolchain PATH used `/root/.hermes/cache/scratch/novapanel-toolchain/bin`, Go 1.27.3.

## Still blocked

The production listener currently opens before `Queue.Run`, and ordinary RPC mutations/scheduler admission are not covered by this narrow gate. Before enabling consumers, the integration owner must wire a synchronous pre-listener boundary and common mutation gate without deadlocking recovery audit or changing existing read availability. Do not treat the new operation gate as that complete global startup contract.

WordPress/site-delete typed journal variants, dynamic attached-database lock planning, protected WordPress configuration/salt recovery, canonical store-error mapping and authorized reconciliation remain unimplemented. Do not use this credential-only envelope for WordPress. Existing pre-envelope sealed operations fail closed; this branch enables no production DB handlers, so no compatibility upgrade is claimed.

Authority assertions here prove restoration, binding and audit projection, not production RPC authorization for all DB kinds. Standalone deletion does not carry a credential envelope and retains its existing trusted-column authority. The queue runner remains an injected external-effect seam, not real MariaDB or WordPress acceptance. The test audit file is real and replayed on reopen, but final append-only filesystem enforcement, PHP recovery and power-loss acceptance remain unpassed. No host DB/services, public CA, push, PR or merge was used.
