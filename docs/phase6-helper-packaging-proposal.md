# Phase 6 independent helper and versioned packaging proposal

Status: inspection implementation only. `update.apply` admission and execution remain `unsupported`. No active service units changed. This does not complete Phase 6.

## Implemented contract

`panel-update-helper prepare` accepts no flags or configuration overrides. It inspects `/opt/novapanel` and inherits three Linux sealed memfds, descriptor 3 for exact manifest bytes, 4 for the base64 signature, and 5 for the bundle. All require WRITE, GROW, SHRINK and SEAL seals. Mutable regular files and read-only descriptors without inode seals are refused. Section readers ignore inherited offsets. No URL is fetched or accepted as input.

The existing release-build Ed25519 anchor must be owner-supplied with `-X novapanel/internal/update.releasePublicKeyBase64=...`. The default stays unset and fails before filesystem or descriptor inspection. Tests create ephemeral keys only. The helper authenticates bounded exact manifest bytes before parsing, validates the existing version layout, refuses downgrade, and hashes the sealed bundle against signed size/digest. It returns only current/candidate versions. It does not unpack, install, restart, reconcile jobs, or open SQLite.

The version-layout validator walks ancestors with no-follow directory descriptors, requires root ownership and no group/world writes, rejects unsafe current links and binaries, and requires installation/version directory and binary world traversal/execution modes. It is an inspection, not a retained path-custody guarantee for active apply. A privileged actor can still rename directories. Real panel-web credentials must separately prove traversal through every installation ancestor, including mount policy and ACLs.

## Proposed packaging, not installed

- Keep a stable root-owned `/usr/libexec/novapanel-update-helper` outside version directories. Validate helper executable, ancestors and owner before any future launch. Never execute a helper from a downloaded bundle.
- Retain complete `/opt/novapanel/<final-semver>/panel-agent` and `panel-web` pairs, owned by root, directories and binaries mode 0755. `current` is a single relative final-semver link. Preserve the previous pair for rollback.
- After explicit migration acceptance, agent ExecStart would use `/opt/novapanel/current/panel-agent`; web ExecStart would use `/opt/novapanel/current/panel-web`. Current committed units still use `/usr/sbin/novapanel-agent` and `/usr/bin/novapanel-web` and must not be silently switched.
- A separately managed root systemd service must own helper lifetime outside both daemon cgroups, with no PartOf/BindsTo dependency that stops it during their restart. Shell backgrounding, setsid and a daemon child alone do not prove systemd survival. Descriptor transport to that service is unresolved; the existing exec.Runner cannot pass ExtraFiles. Do not bypass the sole process boundary.
- Boot recovery must run outside the daemon before normal service startup, authenticate local installation custody, retain pending evidence on failure, and prove rollback health. Existing offline Recover is not exposed by this command.

## Scratch acceptance

`go test -race ./internal/update ./cmd/panel-update-helper -count=1` exercises sealed descriptors, signature/digest refusal, mutable input refusal, non-installing preparation, layout checks, command refusal, and existing signed-update tests.

The process tests execute the real test binary through internal/exec, with test-only fake unit runners. One launcher starts a child and exits. The child observes reparenting, reaches a real stalled Unix RPC socket, expires its health deadline and rolls the scratch current link back. A separate child is SIGKILLed by runner cancellation after switch publication; a fresh recovery call restores the retained version and clears pending evidence. This proves ordinary process lifetime and local pending recovery only, not survival of a real systemd cgroup kill, actual service recovery, durable jobs, or power loss.

A real scratch Store.Open test proves the helper cannot open the live daemon's lifetime-flocked SQLite store and can open it after daemon Close. Therefore no atomic job/installation transaction or worker reconciliation is claimed. Store handoff, durable job identity and terminal audit delivery need an approved contract before apply can be admitted.

## Activation blockers

Owner signer provenance, stable helper packaging and ownership, daemon-to-independent-unit descriptor custody, store shutdown/handoff, durable interrupted-job reconciliation, terminal auditing, versioned unit migration, real rollback ping, disposable Ubuntu cgroup/power-loss acceptance and unprivileged web execution remain required. No host systemctl, release downloads, production signing key, public CA, push or PR operation was performed.
