# Independent firewall recovery runtime

## Implemented boundary

`/usr/sbin/novapanel-agent fw-revert /var/lib/novapanel/firewall/snapshots/<ID>`
selects one canonical uppercase ULID before daemon flag parsing, configuration,
web-account lookup, SQLite, seal-key, audit, socket or worker initialization.
The helper requires effective UID and GID zero. No flags, configuration roots or
environment overrides are accepted. Unknown daemon arguments still use daemon
flag validation.

Production recovery admits root:root non-writable no-follow ancestors, private
0700 firewall/journal/snapshots/restore/markers directories and an existing empty
single-link 0600 root:root stable journal lock. It never provisions or repairs
these paths. Tmpfiles uses create-if-absent `f`, not truncating `F` or `f+`.
Upgrade/removal must not replace or remove roots, the lock or evidence while a
recovery record exists. No age-based cleanup is authorized.

The actual execution is Journal.FireVerified, immutable snapshot custody and
InstalledRestore. The installed destination opens lazily inside the verified
callback. Terminal matching fire does not require /etc/ufw, restore or markers.
Only journal/custody layout is admitted before the decision. Restoration still
requires the same strict destination owner, group, modes and no-follow ancestors.
The initial journal lock wait honors the recovery context while preserving inode
validation. Recovery restores the
six admitted rule files, preserves approved installed modes, restores prior
enabled state with fixed argv, publishes the ID/digest/enabled-bound private
marker, then publishes reverted. Failures retain same-ID journal/custody evidence.
Fire still refuses predeadline reverting. There is no SQL acknowledgement here.
Lock order is stable journal flock, custody verification, private restoration
flock, commands, marker, restoration unlock, terminal journal publication.
Never seek audit or Store locks inside this callback.

The internal `OpenInstalledJournalRecovery(ctx)` consumer owns journal/custody
handles and shares this lazy callback for synchronous manual RevertVerified.
Manual admission is predeadline except matching interrupted reverting replay.
Its Confirm uses Journal.Confirm followed by a ten-second detached timer stop and
readback. This trusted-root in-process consumer is not the independent helper;
helper cgroup admission is unchanged. Public wiring remains closed. See
`docs/phase6-journal-recovery.md` for ownership and completion limits.

## Required service ownership

Every production execution must be in exactly
`/system.slice/novapanel-fwrevert-<ID>.service` in the unified system cgroup tree.
Admission reads bounded no-follow root-owned proc membership, checks procfs and
cgroup2 filesystem types, root-controlled cgroup directories, domain type and
membership in cgroup.procs, then rereads membership and pinned identities.
`INVOCATION_ID` is not evidence. User-manager, daemon, nested, legacy and hidden
namespace layouts fail closed. An unmanaged root shell must use the managed unit.
Root is trusted not to forge service custody or migrate processes concurrently.

The future system-manager launcher must set all of these explicitly:

- `Type=exec`, `User=root`, `Group=root`.
- `KillMode=control-group`, `SendSIGKILL=yes`, `RemainAfterExit=no`.
- `Restart=on-failure`, `RestartSec=5s`, `TimeoutStopSec=10s`.
- `TimeoutStartSec=infinity`, `StartLimitIntervalSec=0`.
- No runtime timeout and no PartOf, BindsTo, Requires, Requisite or Conflicts edge
  to either novapanel-agent.service or novapanel-web.service.

One ID has one service and matching timer identity. The manager must drain the
entire prior service cgroup before restarting it. The recovery-only exec runner
sets Linux direct-child parent-death SIGKILL, keeps the creating OS thread alive
through Start/Wait, retains process-group context cancellation and synchronous
wait/pipe drain. General Real callers are unchanged. Parent-death signalling does
not cover grandchildren. Manager cgroup cleanup is required for unexpected helper
death; scratch process tests do not validate that manager contract.

## Implemented timer and fail-stop startup consumer

The daemon now calls a mandatory fixed-layout preflight before OpenState, socket
reservation and worker configuration. Locked Journal.Inspect distinguishes a
missing state leaf from invalid custody. A valid empty journal permits ordinary
startup. Every pending, terminal and acknowledged record retains its evidence and
blocks ordinary serve. No production migration or terminal acknowledgement is
introduced.

The private manager adapter executes fixed systemd-run, busctl and timer-only
systemctl commands through Runner. It requests explicit cleanup/retry properties,
rounds the remaining absolute deadline up to microseconds, queries typed manager
properties, and repeats object readback. A launcher success alone is never proof.
Existing conflicting units are not replaced. A verified existing service without
a timer starts immediately. Predeadline reverting uses immediate retry while Fire
continues to reject early restoration. Confirm remains disconnected publicly.

The consumer does not capture, arm, apply, reconcile or archive transactions.
Transient timers do not survive reboot. Startup reconstructs scheduling from the
retained journal but still refuses daemon admission until later SQL integration.
Clock rollback has no bounded elapsed-time guarantee. Root remains trusted not to
reconfigure admitted manager units concurrently. Real manager property encoding,
cgroup drain, UFW, reboot and power-loss checks remain deferred VM acceptance.

## Earlier consumer design

The timer launcher must invoke only `/usr/bin/systemd-run`, explicit system service
mode and the fixed installed helper argv, with the ID-bound unit and all properties
above. Use `--on-active=<remaining authoritative deadline>` and
`--timer-property=AccuracySec=1s`. No shell, --user, --scope, --wait, --pipe, arbitrary
properties, at or cron fallback. Inspect actual timer activation, service ExecStart,
credentials, dependency and cleanup properties before admitting apply. Exit zero
from the launcher is not arming proof. Confirm stops only the timer after durable
journal confirmation, never the recovery service.

Transient monotonic timers do not survive reboot. The next startup coordinator
must inspect the journal under its stable lock before mutation workers run.
Absence is explicit; malformed state blocks mutation. Inspection returns a value
copy and does not authorize decisions outside the lock. Fire/Confirm/Reconcile
must reread. Predeadline armed/confirming reestablish and verify the remaining
window. Expired armed/confirming and reverting start the same managed service.
Predeadline reverting retries until Fire can admit it, without resetting deadline
or substituting manual Revert. Terminal outcomes require daemon audit/Store
reconciliation; acknowledged still refuses new arms. Clock rollback provides no
bounded elapsed-time guarantee.

Production migration 0007 remains absent pending the independently owned 0006
predecessor. Do not acknowledge terminal files using file evidence alone or treat
missing SQL tables as absence. Later reconciliation runs after ordinary audit
replay and before mutation workers. Public apply, confirmation and legacy wiring
remain closed. This helper neither captures nor arms, applies, archives or deletes.

## Evidence scope and outstanding acceptance

Connected scratch tests restore actual files for both enabled states, preserve
modes, bind markers and terminal journal metadata, reject early/wrong-ID/tampered
or unsafe state, and replay command/marker failures through independent opens.
Subprocess barriers exercise independently opened helper compositions, journal
contention, command-entry cancellation and same-ID replay with fake commands.
The exec tests check the direct-child signal policy and actual parent SIGKILL.
All command execution is scratch test binaries or injected runners, not real UFW.

The correction retains independent-review RED regressions for all terminal states
with both destinations unavailable and for an already-canceled initial lock wait.
A separate held-lock deadline test covers cancellation during constructor wait.
The command matrix crosses enabled/disabled command positions with exit failure,
runner error and context cancellation, then verifies reverting, intact custody,
absent markers and same-ID reopen/replay. Filesystem-backed service fixtures run
the complete admission algorithm with private directory/type probes, including
unsafe membership leaves, forged environment, wrong identities, membership change
and root/group/manager replacement. Production still fixes its paths and probes.
A test-only build overlay connects the actual dispatcher to the private recovery
constructor in a child while the parent retains a real Store lifetime lock. It
checks all installed bytes/modes, marker binding, terminal state and unchanged
SQLite/WAL/SHM/pid bytes. The installed binary has no override.

The raw parent-death subprocess test also proves a grandchild can still write
only after the direct child is observed dead, then cleans up the owned processes.
This demonstrates why service cleanup remains mandatory.

Independent review of 3407c9e supplied enabled/disabled death-after-marker
reopen/replay coverage in its external overlay. That is independent evidence,
not an original-author test run. The actual-dispatcher
child uses a fake Runner and test-only service/layout injection, not the installed
CLI or real manager cleanup. This is not full acceptance of every design test.
Independent exact-commit review remains required. Real manager cleanup, UFW
restoration, reboot, clock change and power-loss acceptance are deferred
final-product VM work. Root/GID-zero behavioral tests skip for ordinary users.
