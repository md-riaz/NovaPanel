# WordPress site execution identity adapter

## Bounded decision before implementation

The production offline transport, trusted CLI ini/custody and all-site-UID writer quiescence remain absent. The tools-only Docker checksum transport is not a production dependency. Composing it into Queue.Run would incorrectly open execution. This slice instead implements the missing account-matched credential-drop adapter over the existing internal/exec boundary. It does not execute PHP, acquire files, stop services, freeze accounts or publish an installation.

The adapter resolves the exact reserved non-login account with fixed getent argv, matches its numeric UID/GID and canonical home, and resolves www-data to exclude both its UID and GID. The queue consumer `WordPressSiteRunner` opens only its active worker's sealed original reservation under the persisted operation locks. Its returned runner rechecks the sealed binding for each call and refuses use from a different context or after worker completion. The platform runner enforces the original numeric identity with an explicitly empty supplementary group list on every invocation. It revalidates the account before each invocation, refuses conflicting credential options, never inherits environment, and preserves internal/exec cancellation and output limits. Raw resolver output never leaves an error. The adapter is an internal trusted dependency, not an RPC or an authorization/custody gate. A caller must still own the original operation locks and all filesystem/runtime prerequisites before executing any WordPress step.

No quiescence success callback is added. Account lookup cannot prevent concurrent root account replacement or site-UID writers. Runtime must separately exclude trusted identity changes, root relocation, FPM workers and every other site-UID writer for the entire protected effect. These remain source gaps, not only deferred VM checks.

## Requirement to consumer and evidence map

| Canonical requirement | Actual consumer | Executed evidence | Remaining |
| --- | --- | --- | --- |
| Phase 4 privilege drop, matched non-login identity, no nginx membership | New internal/platform site credential runner over internal/exec.Real | Tests recorded with final commit evidence | Runtime invocation from the queue remains gated by custody/ini/offline/quiescence dependencies |
| Original operation, complete locks, sealed recovery | Existing Queue.Run and StageWordPress/CreateWordPressConfig | Existing queue cancellation/reopen regression suite | No production PHP orchestration added |
| Official runtime checksum command | tools/phase4_wp_offline_lab.py only | Existing confined-lab evidence, not transferred to this adapter | Production offline transport and approved ini absent |
| Protected 0400 config and original-operation retry | Existing configcustody and queue config consumer | Existing actual Queue.Run reopen tests | Physical all-UID quiescence absent |
| Final installation, one-shot claim, serving publication | Existing atomic terminal API, not a live installer | Not claimed here | DB/core install, live transfer and runtime probes remain source gaps |
| Ubuntu identity/FPM/systemd/power loss | No fixture substitution | Deferred and unpassed | Final VM acceptance |
