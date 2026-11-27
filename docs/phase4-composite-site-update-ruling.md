# Phase 4 composite site.update ruling (#137)

This is the upfront ruling for the PHP/domain/TLS/maintenance composite. The
canonical specifications remain authoritative. Existing-site WordPress reinstall
and live database password rotation that rewrites wp-config remain out of scope;
this change needs no drain, IO quiescence, or per-site FPM master.

## Parent and children

One `site.update` job owns one `phase4_operations` parent and an ordered vector
of effect children, not separate jobs. The job and its `site:<site_id>` lock stay
held while children commit. Generalize the held-lifetime ordered-child precedent
in `internal/jobs/phase4_fresh_install.go` into a reusable composite driver.

The disruptive dimensions are PHP and domain/TLS. PHP alone (no other mutable
field) delegates to `php.set`; any single domain, force HTTPS, HSTS, or
maintenance field keeps its existing `site.update` path. Every multi-field
combination uses the composite driver. The auto-maintenance bracket is used only
when both disruptive dimensions are present: `php_version` and at least one of
`primary_domain_id`, `force_https`, or `hsts_max_age`. With fewer than two
disruptive dimensions there is no auto-bracket. An explicit maintenance value is
always applied as the terminal metadata state. For example, `php_version` plus
`maintenance` uses a composite with a `php_change` child and terminal maintenance
metadata application, without an auto-bracket.

Instantiate composite children in order:

1. `maintenance_on`, transient, only for the auto-bracket.
2. `php_change` when `php_version` is supplied.
3. `domain_tls` when `primary_domain_id`, `force_https`, or `hsts_max_age` is supplied.
4. `maintenance_off`/restore when the bracket ran: restore to true only when
   the caller explicitly supplied `maintenance:true`, otherwise false.
   Without a bracket, apply explicit maintenance as the terminal metadata step.

At the R9 refusal-removal gate, update phase-4.md to describe this routing,
removing the combination refusal and retaining the PHP-alone delegation verbatim;
do not edit that spec before the gate.

## Durable state and reusable effects

Add `phase4_operation_children(job_id, ordinal, child_kind, checkpoint, status,
typed_error, identities_json, journal_ref, updated)`, keyed by `(job_id, ordinal)` and referencing
the parent operation. Status is `pending`, `committed`, or `failed`; terminal
children have immutable status, checkpoint and identity, enforced by a trigger.
Each child commits checkpoint, status and its
journal entry atomically while its parent stays non-terminal. Parent checkpoints
are coarse: `prepared` → `verified` → `done`. Each PHP child keeps its validated,
secret-free original and desired version and full limits snapshot written ahead
at `prepared` before any effect; PHP metadata
and its child journal completion commit atomically while the parent remains running.
Standalone PHP terminal atomicity is unchanged. `CompleteOperation` performs
the sole terminal commit after asserting all children are committed or failed and
records their aggregate. Existing standalone PHP and SSL operations do not use the
child table and retain their semantics.

Extract PHP publish and verify into a step shared by `php.set` and the composite;
standalone checkpoints and recovery remain `prepared` → `config_published` →
`verified`. Reuse the existing SSL publication path for domain/TLS, with
`tls_committed` as its child checkpoint. Reuse `UpdateSite` plus `applySite` for
maintenance and the maintenance-only path. Prove standalone operations,
checkpoints and recovery unchanged with regression tests.

## Recovery, failures and audit

Recovery requeues a non-terminal parent. Its driver reloads children, skips
terminal (`committed` or `failed`) children and resumes the first pending child
only if no child failed. Effects are
idempotent on replay. TLS startup recovery must defer composite jobs to this
driver, without changing standalone TLS recovery or recovering a TLS child twice.

Transient child failure returns `ErrRetry`; replay skips terminal children.
A permanent failure commits the child's `failed` status, typed error and journal
durably in its own transaction before parent terminalization. Forward progress
stops: no later pending effect child starts. On replay any failed child routes
directly to failure finalization. Record each other pending productive child as
failed with typed error `not_attempted` and its journal, so the aggregate identifies
skipped work. The restore finalizer is mandatory and idempotent: after
`maintenance_on` commits, neither success nor failure may terminalize until the
caller-desired maintenance state has been restored. A restore effect failure is
transient: return `ErrRetry` and re-drive the pending finalizer. An unfinished
restore remains in the roll-forward set and is exempt from terminal expiry using
the same machinery as `teardown_started` in 02. If restore cannot be recovered,
the operation remains unreconciled, blocks conflicting mutations, and is surfaced
for authorized reconcile under the existing 02 unreconciled-recovery disposition;
the known maintenance state is never silently abandoned. A permanent productive
child failure terminalizes the parent only after restore commits, with the typed
error and child aggregate. Earlier committed changes
remain applied: a later failure does not undo them. Terminal audit aggregates
`{ordinal, child_kind, status, typed_error}` and per-child journal entries remain.
No child produces credentials or changes the no-secrets invariant.

Authorization and locking remain admin plus `sites:write`, with the single site
lock held by the parent for the entire job. No new lock class is introduced.

## Build and verification

Land and push in order: (1) this ruling; (2) the next-free migration, child store
and inert driver behind the refusal; (3) shared effects and standalone regression
tests; (4) end-to-end composite and refusal removal; (5) child-boundary crash
replay and composite TLS recovery deferral; (6) transient and permanent failure
tests including restore, and canonical spec amendments. The refusal and spec
deferral are removed only after the composite path is proven green.

The gate is Linux build and vet, staticcheck, `go mod verify`, govulncheck, and
root-container untagged and lab tests. Inventory containers and images before
and after; use unique container names, touch only containers created by this run,
and confine cgroups to the managed subtree. Rerun and identify known flakes
#130, #131 and #157 if encountered. A genuine shared-contract, standalone TLS
recovery, or standalone effect preservation conflict is raised, not guessed.
