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

B is incomplete. No production prepare/load/checkpoint/blocker/expiry/reconcile/atomic terminal operation API has been implemented. Ordered multi-resource queue acquisition and trusted durable reserved identity metadata remain unimplemented. Existing singleton queue behavior is unchanged. F19 metadata/job/claim/journal atomicity is tested only as SQL, not as a typed worker path. The next B slice must implement those contracts and publish exact signatures before DB/WordPress workers start. There is deliberately no stub API pretending otherwise.

A can continue pure production nginx PHP-routing tests without exposing PHP site types. Runtime publication requires complete B contracts, safe FPM/nginx validate/publish/probe/restore, existing-site compatibility tests and production-mode lab acceptance. Current lab fixture substitution is not production success.

C must wait for complete B/A runtime contracts before wiring WordPress, database lifecycle, independent purge flags and one-shot secrets. Artifact/checksum/argv/stdin validation can proceed as pure work. No host service, privileged container, public CA or production resource was touched. Final Ubuntu VM custody, root FPM identity changes, MariaDB root socket authentication and power-loss remain unpassed.
