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
callback. Terminal matching fire does not require /etc/ufw. Recovery restores the
six admitted rule files, preserves approved installed modes, restores prior
enabled state with fixed argv, publishes the ID/digest/enabled-bound private
marker, then publishes reverted. Failures retain same-ID journal/custody evidence.
Fire still refuses predeadline reverting. There is no SQL acknowledgement here.
Lock order is stable journal flock, custody verification, private restoration
flock, commands, marker, restoration unlock, terminal journal publication.
Never seek audit or Store locks inside this callback.

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

## Next consumer, not implemented

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

Outstanding source-test coverage is explicit: no connected actual-dispatcher
subprocess with a concurrently held Store, no death-after-marker overlay, no raw
Pdeathsig grandchild survival demonstration, and no fixture-backed full service
filesystem replacement matrix. Therefore this is runtime source progress, not
full acceptance of every test criterion in the implementation design. Independent
exact-commit review remains required. Real manager cleanup, UFW restoration,
reboot, clock change and power-loss acceptance are deferred final-product VM work.
Root/GID-zero scratch behavioral tests skip on ordinary-user execution.
