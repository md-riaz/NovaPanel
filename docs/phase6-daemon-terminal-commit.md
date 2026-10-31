# Internal daemon terminal commit

This delegated bounded policy is recorded before production implementation. Authority is the canonical Phase 6 journal contract and the terminal event contract in phase6-terminal-reconciliation.md. This is not migration or public activation approval.

Agent.CommitFirewallTerminal uses the daemon's already-open ordinary audit and Store. It opens only the fixed installed journal, bounds caller context to ten seconds, and closes only that new journal. Callers must serialize against Agent cleanup. There is no detached work, startup registration, public command or configurable installed path.

Lock order is audit Log, journal stable inode, Store. The terminal method rereads under flock and holds it through the callback. Only confirmed and reverted are admitted. It syncs the directory before SQL for both outcomes. A confirmed sync error preserves ErrConfirmationCommitted. Missing, malformed, unsafe, unresolved and acknowledged records fail closed without invoking the callback. Acknowledged version 1 has lost its original outcome and cannot prove this commit.

The existing Store transaction validates the exact singleton projection and deterministic metadata-only recovery event. It appends and syncs the ordinary audit file before inserting/verifying the event and consuming the matching row in one SQL COMMIT. Matching startup-replayed events do not bypass deletion. Missing projections require exact existing evidence. Snapshot paths in SQL are never opened. File-only events prove intent, not SQL completion. Retries append identical replayable events. Audit sink failures retain existing sticky health behavior; SQL errors do not broaden that policy.

Success leaves journal and immutable snapshot unchanged. No acknowledgement, terminal deletion, archive, timer command, restoration or new arm follows. The separate acknowledging API now preserves the original outcome in a v2 receipt. Actual serve admits eligible terminal records only through the lifecycle in phase6-terminal-startup-lifecycle.md; pending and legacy ambiguous receipts still refuse. Public FirewallRecovery remains nil and apply remains unsupported.

Connected tests now use the complete embedded production 0007 schema through ordinary Store.Open and OpenState. The schema-only stack carries exact published Phase 4 migration 0006 without its runtime. The older fixture-only evidence remains historical. Process and filesystem fault tests use owned scratch paths and injected command runners. Real UFW, systemd, append-only enforcement, reboot, power loss and external reachability remain deferred final-product acceptance.

Remaining source work includes retention/reuse policy, public authorization and actor audit, capture/projection/arm/timer/full desired-set apply, and other Phase 6 backup/updater requirements. This internal consumer does not complete Phase 6.
