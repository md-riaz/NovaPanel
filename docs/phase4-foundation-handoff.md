# Phase 4 foundation handoff

## Opening and scope

Base is verified merged main `2ff29a1a70f0ad678969d48006a4c21c183dd29a`, PR #81. The user authorized Phase 4 development and one draft phase PR, not merging Phase 4. Earlier stacked records are preserved as historical evidence, not current authorization. Main's #86 reply/recovery ruling and D2 trust anchor remain intact.

Canonical packet `936d848` was applied as a documentation-only patch, then reconciled to the actual merged prerequisite. Pure sources `93fe16a` and `13784d05` were applied exactly once. Lab commits were applied in dependency order `f0b1127`, `cb6a495`, `10fa612`, `b834257`. No stale branch was merged.

## Implemented bounded foundation

- Production `0006_phase4.sql` exactly matches approved `testdata/phase4_candidate.sql`. Migrations 0001 through 0005 are unchanged. The actual new SHA-256 is registered in hashes.json.
- Real SQLite fresh schema, shipped-v5 upgrade, reopen, tuple checks, deferred composite primary-user FK, same-site binding, journal retention and SQL rollback/expiry constraints are tested. SQL contract tests do not imply a production journal API.
- `Store.Tx` explicitly rolls back the pinned connection after deferred-FK commit failure. If rollback fails it discards the connection. The regression reproduced the prior leaked transaction before the fix.
- Typed metadata-only PHP limit merge and primary DB/account creation/read APIs exist. Generated DB/user names derive from validated reserved ULIDs. Collision refuses rather than adopts.
- Existing PHP/FPM and MariaDB grant renderers and confined lab launchers remain pure/test-only. No runtime PHP site, DB command or WordPress installation is exposed.

## Exact compiled API for consumers

```go
// novapanel/internal/render/php
func MergeLimits(current *Limits, patch LimitsPatch) (Limits, error)
func Render(Desired) ([]byte, error)

// novapanel/internal/store
func (*Store) GetPHPLimits(context.Context, string) (php.Limits, error)
func (*Store) MergePHPLimits(context.Context, string, php.LimitsPatch) (php.Limits, error)
type DatabasePrimary struct { ID, UserID, SiteID string }
type Database struct {
    ID, SiteID, Name, Engine, PrimaryUserID string
    Created int64
}
func (*Store) CreateDatabasePrimary(context.Context, DatabasePrimary) error
func (*Store) GetDatabase(context.Context, string) (Database, error)
```

`MergePHPLimits` rejects static sites and validates the full merged tuple. It is a metadata helper, not a pool publication operation. A caller must hold the site lock and publish/probe before storing verified desired state, with rollback of the prior tuple on failed runtime publication. `CreateDatabasePrimary` is metadata-only, not an external resource allocator or recoverable terminal publication. Runtime workers must not compose it with separate claim/job writes.

## Still required before runtime consumers

B remains incomplete. The recovered operation-foundation slice adds the internal APIs below for database create/rotate/delete and secondary-account create/delete. Prepare persists resolved identities, authority, a sealed payload and a reserved claim hash in one transaction. The worker acquires the journal's ordered lock set once; ordinary jobs keep singleton locking. Terminal publication commits metadata, job result/done, sealed claim, journal done and recovery ciphertext removal atomically. Done replay does not recreate a claim. Non-interrupted queue failures clear recovery ciphertext and keep an unreconciled blocker; interrupted failures retain recovery input.

```go
// novapanel/internal/store
// OperationRequest carries trusted IDs, not RPC arguments.
type OperationRequest struct {
    Kind OperationKind
    SiteID, DatabaseID, UserID string
}
// DatabaseCreate, DatabaseRotate, DBUserCreate, DatabaseDelete, DBUserDelete
// are the supported OperationKind constants.
func (*Store) PrepareOperation(context.Context, Job, JobClaim, OperationRequest, []byte, []byte) (Operation, error)
func (*Store) LoadOperation(context.Context, string) (Operation, error)
func (Operation) Keys() []string
func OrderedOperationKeys([]string) []string
func (*Store) CheckOperation(context.Context, string) error
func (*Store) CheckpointOperation(context.Context, string, string, string) error
func (*Store) CompleteOperation(context.Context, string, *JobClaim) error
func (*Store) FailOperation(context.Context, string, string) error
func (*Store) ExpireOperations(context.Context) (int, error)
func (*Store) ReconcileOperation(context.Context, string) error

// novapanel/internal/jobs
func (*Queue) EnqueueOperation(context.Context, store.OperationRequest, map[string]any) (EnqueueResult, error)
```

`Operation` exposes JobID, Owner, Kind, SiteID, DatabaseID, Checkpoint, typed Identity, sealed Nonce/Ciphertext, Expires, Updated and Reconciled. Identity contains Version, Database, Accounts and TargetUserID; each account contains ID, Username and Host. These are internal trusted contracts, not public RPC handlers. `EnqueueOperation` generates create IDs before admission and returns the existing `EnqueueResult` with a claim token for secret-bearing operations. The caller must validate the credential payload and authorize before admission. `CompleteOperation` receives already-sealed claim bytes; it never performs external IO. Callers must hold `Operation.Keys()` through external postcondition checks and terminal publication. Preparation lifetime is fixed at 24 hours; claim lifetime starts at publication.

Verified coverage includes typed create publication rollback with a real SQLite trigger, close/reopen identity retention, owner-bound one-shot claim and done replay, exact-deadline expiry blocking/reconcile, queue deletion replay after reopen, ordered overlapping locks, and queue failure cleanup. These checks are not full DB/WordPress recovery acceptance.

Before runtime consumers: implement WordPress/site-delete journal variants and dynamic attached-DB lock planning; strengthen immutable identity validation and complete under-lock membership/account/binding revalidation; define typed credential/config payloads and claim projections; map store errors to canonical RPC codes; wire startup-before-admission and minute expiry batches plus authorized reconciliation; prove success/failure and crash/restart through the real queue for all operation kinds, including session/token authority, audit failure boundaries, blocked-clock publication, bounded expiry/query-plan/concurrency and leak checks. The generic `RecoverJobs` behavior is unchanged and is not a complete Phase 4 recovery supervisor. No runtime verb is enabled by this foundation. Do not start DB/WordPress consumers until these remaining contracts are complete.

A can continue pure production nginx PHP-routing tests without exposing PHP site types. Runtime publication requires complete B contracts, safe FPM/nginx validate/publish/probe/restore, existing-site compatibility tests and production-mode lab acceptance. Current lab fixture substitution is not production success.

C must wait for complete B/A runtime contracts before wiring WordPress, database lifecycle, independent purge flags and one-shot secrets. Artifact/checksum/argv/stdin validation can proceed as pure work. No host service, privileged container, public CA or production resource was touched. Final Ubuntu VM custody, root FPM identity changes, MariaDB root socket authentication and power-loss remain unpassed.
