# Terminal-only daemon startup lifecycle

## Adopted policy

The parent adopted corrected rulings 1-8 from the startup design before this
implementation. Ordinary serve classifies fixed installed custody before opening
SQLite. Pending armed/confirming/reverting invokes independent manager admission
and stops that attempt, including a helper transition during readback. Corrupt
custody and legacy v1 acknowledged refuse before state initialization.

An opaque retained plan pins journal custody without holding flock through
ordinary OpenState. Ordinary migrations, seal loading, audit replay, injected
append-only enforcement and agent.start remain unchanged. Completion takes Audit,
journal flock, then Store transaction, rechecks the classified value and finishes
before socket reservation, RPC/job configuration, listeners or workers. Absence
also requires no orphan SQL pending rows. Failures are fatal, not Phase 4's
read-only blocked mode. Future Phase 4 PrepareStartup belongs after firewall
completion and socket reservation but before listener/workers under its own policy.

## File-format migration

All arms and pre-acknowledgement records remain canonical version 1. Only an
acknowledged receipt uses version 2 and adds required terminal_state, exactly
confirmed or reverted. Every other field is preserved. OriginalTerminal restores
the original version-1 record, including its exact audit JSON bytes. Store accepts
only normalized version-1 terminal events. No migration SQL or hash changes.

A terminal boot syncs the terminal directory, durably appends the ordinary audit
event, commits exact SQL event/projection consumption, then publishes the receipt
with caller cancellation checked before rename. A post-rename directory-sync
failure returns ErrAcknowledgementCommitted and refuses startup. It does not
claim the rename or SQL rolled back.

Every receipt boot rechecks the receipt, syncs the directory, requires the exact
post-replay SQL event and zero pending rows, then durably appends that same event
and completes the transaction. Verification itself inserts/deletes nothing.
Ordinary F11 replay may legitimately reconstruct SQL from exact file evidence
before this check. File-only plus zero pending is valid. File-only plus any
pending, missing-both, collision and competing pending refuse. Terminal retries,
unlike receipts, still consume an exact matching row after ordinary replay.

Legacy v1 acknowledged lost the outcome and has no automatic conversion. Do not
infer it from SQL verbs, markers or live UFW state. Old binaries reject v2, so
rollback is not transparent. Install daemon/helper binaries together and perform
final-product installation/restart/downgrade acceptance separately.

## Failure handling

- Preserve journal, snapshot and markers. Do not remove state.next manually for a
  normal interrupted publication. A private regular staging leaf is validated and
  replaced on retry. Hostile staging objects refuse and require separate review.
- After a failed pre-rename publication, retry ordinary startup with retained v1
  terminal evidence. After visible v2 publication, retry verifies SQL/file and
  reestablishes directory durability. Never change the outcome to make boot pass.
- An audit sink failure leaves the Log unhealthy. Close/reopen and ordinary replay
  are required; do not clear health or bypass the independent file.
- Missing/colliding events and any pending under a receipt are inconsistent.
  Refuse service and investigate preserved evidence; automatic row deletion,
  inferred conversion and outcome repair are not authorized.
- Trusted root must not replace custody concurrently. Revalidation is not a
  defense against an omnipotent trusted root or future live-apply concurrency.

No terminal startup restore/timer commands, archive, deletion, Arm reuse or new
pending ID are introduced. Public FirewallRecovery stays unset; apply unsupported.

## Executed acceptance and limits

The implementation evidence is external at
`/root/.hermes/cache/scratch/phase6-terminal-startup-evidence/RESULT.md`.
Original actual-serve behavioral RED is retained separately from inherited
codec/API REDs and fixture failures. Expanded serve tests exercise both outcomes
and enabled states, ordinary replay, two boots, exact event identity, receipt
inconsistencies, absent/orphan startup and real SQL DELETE/deferred COMMIT failure.
Process tests kill owned actual serve at file Sync, SQL COMMIT, receipt file Sync,
receipt rename and durable publication before listener, then restart twice. Fault
cases reject receipt write/Sync/close/rename/directory Sync and reopen safely.
All privileged commands use fixed fake runners against owned scratch paths.

There is no registered firewall.status verb on this source base. Scope excludes
verbs implementation, so actual authorized service.status plus valid unsupported
firewall.apply on the same connection proves serving without activating apply.
This is an explicit deviation from the design's named firewall.status read.

Root/GID-zero installed cases skip for ordinary users; codec, Store and private
cases remain ordinary-user executable. Source gates are not VM acceptance.
Real UFW/systemd/cgroup cleanup, append-only capability, reboot, clock rollback,
network reachability, installation/downgrade and power loss remain deferred
final-product acceptance. Full Phase 6, public fresh-request authorization/actor
attribution, full desired-set apply, retention/reuse and backup/updater completion
are not established by this bounded lifecycle.
