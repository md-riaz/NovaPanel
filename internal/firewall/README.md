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
- `SnapshotRecovery.RestoreAndMark` is an injected, idempotent recovery contract. Tests write a scratch marker, not system firewall files. A failed restoration or timer cancellation retains pending state.

## Consumer work not implemented here

1. Keep canonical 01 authorization at the privileged dispatch boundary. Apply is slow/effectful, admin+, MFA, `firewall:write`, object key `firewall`. Confirm/revert are synchronous, MFA-gated and automation-token reachable. This package is not an authorization layer.
2. Use 03's exact apply shape. `Desired.DefaultIncoming` is a trusted planning option, not a new RPC argument: 03 currently omits `default_incoming`, despite phase prose including it. Do not expose it without a canonical amendment. Read SSH port and timer defaults from config.
3. `store.CreateFirewallPending` and `store.WithPending` implement singleton reservation and one-shot transaction consumption with a SQLite writer lock held through the callback. Tests prove reopen durability, concurrency, deadline refusal and restore retry. The canonical DDL exists only in `internal/store/testdata/firewall_pending.sql`, matched exactly to 02. No migration is added because Phase 4 reserves 0006 and combined Phase 6 numbering needs integration-owner approval. Production calls therefore fail closed on the absent table. Independent process access remains blocked by the daemon lifetime flock. A delete-failure test explicitly demonstrates that a stopped timer plus retained row is still an unresolved recovery gap, not crash safety.
4. Capture a confined root-owned snapshot directory with UFW rules, prior enabled state, iptables-save, ip6tables-save and numbered status. Implement no-follow safe restore plus durable audit marker before pending consumption. Reconcile markers on daemon startup. No filesystem snapshot adapter is supplied here.
5. Apply requires full desired-set replacement, dry-run, persisted pending plus independently armed transient timer BEFORE risky commands, SSH-first application and rule verification. Rendered argv are not an append-to-live implementation, and there is deliberately no live apply API in this slice.
6. Root `panel-agent fw-revert` must work with the daemon down. Its locking/DB ownership must be reconciled with the existing daemon lifetime lock. Cancel/fire races must use the same cross-process pending lock. Manual revert may leave a harmless timer which later finds no row; never reuse IDs or delete snapshots before the timer can no longer need them.
7. Fresh authenticated confirm HTTP requests need `Cache-Control: no-store` and `Connection: close`. Do not claim external reachability from an internal probe. Audit actor/resolved principal through the existing audit path.
8. Disposable-VM UFW/systemd access restoration, root subcommand, actual snapshot restoration, singleton apply conflict, restart reconciliation and full Phase 6 acceptance remain unverified.

Contracts: 00 F3/F6/F9/F10/F11, 01 firewall rows, 02 `fw_pending`, 03 firewall shapes, 06 unit seams, phase-6 B-D7, and PR80's nonce ruling merged in #83. #84 concerns updates and does not change this package.
