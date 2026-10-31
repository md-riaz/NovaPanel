# Firewall bounded integration

The structured planning package is registered through the actual dispatcher for
`firewall.apply`, `firewall.confirm` and `firewall.revert`. Apply and its worker
always return `unsupported` without commands or queue writes. Confirm/revert use
`Agent.FirewallRecovery`, which is nil by default and accepts injected dependencies
only. No production snapshot adapter or independent root deadman is installed.
All firewall tests use injected runners and scratch SQLite or in-memory state.
No firewall test invokes UFW, systemd, iptables, SSH or a network probe.

## APIs

- `Render(Desired, sshPort, sshSource)` validates the whole desired set and returns UFW argv suffixes. SSH is always rule 1. Ports and CIDRs reuse `internal/validate`. Actions, protocols, defaults and comments have bounded allowlists. Omitted source means `any`. Empty desired rules retain SSH.
- `DryRun(ctx, exec.Runner, Desired, sshPort, sshSource)` executes only fixed `/usr/sbin/ufw --dry-run` argv through F10's existing runner.
- `Recovery.Confirm`, `Recovery.Revert` and `Recovery.Fire` serialize through injected `PendingStore.WithPending`. `pending_id` is the existing opaque ULID nonce, consumed once, with no additional field or column. Confirm/revert are synchronous. At the exact deadline they return `not_found`; only the deadman can recover an expired pending.
- `Recovery.Confirm` fails closed with `unsupported` for live pending IDs before any cancellation or consumption. Unknown/expired IDs still return `not_found`. There is no unsafe injected-runner bypass. A best-effort re-arm cannot close a process-death window.
- `SnapshotRecovery.RestoreAndMark` is an injected, idempotent recovery contract bound to the same pending ID. It must restore rules and prior enabled state and durably publish the audit marker before consumption. Tests write scratch markers, not system firewall files. Restoration, marker, deletion and actual deferred-constraint COMMIT failures retain pending state and replay after close/reopen.

## Confined journal foundation

`journal.go` is an unwired alternative authority, not a `PendingStore` adapter.
It never imports or opens SQLite. The approved phase-6 amendment supersedes the
old direct-DB helper contract. `Arm` retains a single record permanently and
refuses rearming even after acknowledgement. Production admission/archival is
intentionally absent. Provision the root and stable empty lock before opening;
this package never creates or replaces the lock.

`Confirm` samples its injected clock under the cross-process flock, publishes
recoverable `confirming`, then commits `confirmed` and fsyncs before timer stop.
A preparation sync failure restores on subsequent fire. A final commit sync
failure returns `ErrConfirmationCommitted`, never an ordinary failed confirm.
Readers must sync visible `confirmed` before suppressing recovery or reconciling;
if syncing fails they return an error and leave the timer and projection alone.
No rollback of a committed confirmation is attempted. Pre-rename cancellation
retains recovery; post-commit cancellation does not undo the outcome.
`Revert` is an internal synchronous manual recovery operation sharing the same
flock and publication sequence as `Fire`. Fresh matching `armed` or `confirming`
requests must precede the exact deadline. Matching interrupted `reverting` may
retry after expiry to finish recovery. Confirmed, reverted and acknowledged
manual retries return `not_found`; `Fire` retains idempotent terminal success.
Neither operation stops timers or consumes SQLite. Recovery intent is durably
published before the adapter, and failed restoration or audit-marker publication
retains same-ID replay. A terminal publication error may leave visible `reverted`;
manual retries still return `not_found`, and daemon reconciliation retains the
terminal evidence. Cancellation before intent publication prevents restoration;
after successful restoration it does not prevent terminal publication.
`Fire` publishes reverting
before the idempotent recovery adapter and reverted afterwards. Terminal records
remain until daemon-only `Reconcile` invokes a successful durable DB/audit commit
and publishes acknowledgement. The commit callback must deduplicate by ID.
These callbacks are trusted seams, not implemented production adapters.

Metadata contains only version, ID, fixed unit, deadline, prior enabled state,
snapshot digest and state. The snapshot itself must remain in separate private
custody; UFW snapshots can contain sensitive topology/comments and must never be
logged or copied into JSON. The recovery adapter must bind the full snapshot to
both ID and digest and restore rules plus enabled state before durable audit.

Tests use real scratch files, fixed clocks, private files and fake restoration.
Two actual helper processes contend on flock. SIGKILL after durable confirmation
before cancellation leaves fire harmless; SIGKILL after fake restoration/marker
before terminal publication leaves reverting and replays the same ID on reopen.
Strict canonical bounded JSON rejects duplicate/unknown/missing fields. Tests
reject symlinks, hard links, foreign ownership, weak permissions, replaced lock
and tampered version/unit, and recover an interrupted private temporary file.

The independent review's post-rename EIO and clock-triggered cancellation faults
are committed regressions using real scratch files and close/reopen. Tests also
inject final-commit sync failure, reject unproven reads/reconciliation, retain a
commit after cancellation, and SIGKILL a child before preparation directory sync.
These prove process-death replay, not filesystem power-loss behavior.

Not proved: power-loss or injected fsync/rename failures at every boundary,
real snapshot capture/verification, actual SQLite reconciliation/deduplication,
boot/retry policy and wall-clock rollback/reboot handling, timer ownership,
apply-before-arm ordering, daemon cgroup isolation or real UFW restoration.
Root/agent UID is trusted; file integrity is not cryptographic authenticity against
that UID. Lock replacement is detected for open handles, not across trusted-UID
replacement followed by all participants reopening. Keep live apply disabled and
public confirmation unsupported pending independent review and disposable-VM
production adapter acceptance. No CLI/RPC wiring changed.

## Consumer work not implemented here

1. Keep canonical 01 authorization at the privileged dispatch boundary. Apply is slow/effectful, admin+, MFA, `firewall:write`, object key `firewall`. Confirm/revert are synchronous, MFA-gated and automation-token reachable. This package is not an authorization layer.
2. Use 03's exact apply shape. `Desired.DefaultIncoming` is a trusted planning option, not a new RPC argument: 03 currently omits `default_incoming`, despite phase prose including it. Do not expose it without a canonical amendment. Read SSH port and timer defaults from config.
3. `store.CreateFirewallPending` and `store.WithPending` implement singleton reservation and one-shot transaction consumption with a SQLite writer lock held through the callback. Tests prove reopen durability, concurrency, deadline refusal and restore retry. The canonical DDL exists only in `internal/store/testdata/firewall_pending.sql`, matched exactly to 02. No migration is added because Phase 4 reserves 0006 and combined Phase 6 numbering needs integration-owner approval. Production calls therefore fail closed on the absent table. Independent process access remains blocked by the daemon lifetime flock. Confirmation now refuses before stopping the timer. A scratch replay matrix covers restoration, audit-marker, DELETE and actual COMMIT failures; it is not a process-kill or power-loss test. A real Store open test confirms that the lifetime flock blocks an independent opener until the daemon closes its store.
4. Capture a confined root-owned snapshot directory with UFW rules, prior enabled state, iptables-save, ip6tables-save and numbered status. Implement no-follow safe restore plus durable audit marker before pending consumption. Reconcile markers on daemon startup. No filesystem snapshot adapter is supplied here.
5. Apply requires full desired-set replacement, dry-run, persisted pending plus independently armed transient timer BEFORE risky commands, SSH-first application and rule verification. Rendered argv are not an append-to-live implementation, and there is deliberately no live apply API in this slice.
6. Root `panel-agent fw-revert` must work with the daemon down. Its locking/DB ownership must be reconciled with the existing daemon lifetime lock. Cancel/fire races must use the same cross-process pending lock. Manual revert may leave a harmless timer which later finds no row; never reuse IDs or delete snapshots before the timer can no longer need them.
7. Fresh authenticated confirm HTTP requests need `Cache-Control: no-store` and `Connection: close`. Do not claim external reachability from an internal probe. Audit actor/resolved principal through the existing audit path.
8. Disposable-VM UFW/systemd access restoration, root subcommand, actual snapshot restoration, singleton apply conflict, restart reconciliation and full Phase 6 acceptance remain unverified.

Contracts: 00 F3/F6/F9/F10/F11, 01 firewall rows, 02 `fw_pending`, 03 firewall shapes, 06 unit seams, phase-6 B-D7, and PR80's nonce ruling merged in #83. #84 concerns updates and does not change this package.
