# Phase 4 typed journal integration candidate

This candidate starts at remotely verified open PR87 head `eace6847929e04dbe63caf4f18e5092c8d3beb7e`. It integrates only the missing saved patches, not the saved branch's older history. No remote write or merge is authorized in this worktree.

| Saved patch | Integrated patch | Stable patch ID |
| --- | --- | --- |
| `d8e6a794c489ca8048e4190c59d798f61cf11880` | `7314caa454ef2a9ae1fd6d4707506928a89fa011` | `9f7569b530c83aa7f443211e2930da80ccaf2e6b` |
| `a13c6ff83342175a72772ba84ce58a211ec137d6` | `7c9a75365f5044aab1b99ca3d0499381432d3a6f` | `93ef30d7e04ced5b648abdb6af376c26180f3b32` |
| `0de80783e4dc4becc1814bd8c29d1b55e163d404` | `9c98f4ef7aec441fd8457348ef89f1b1c440af36` | `230e0ed35ecfbb38e9dcbdcf111e7c052517df39` |

Author identity and dates are preserved. The pure WordPress patch also matches historical `67612c56822008d1dd330b00841a312d8f842bed`, which is not an ancestor of the requested base. No migration or hash changes were needed.

## Assessment and correction

The saved patches add typed WordPress and site-delete reservation, sealed credentials, metadata publication and journal recovery. Existing scratch SQLite and injected-runner tests pass. The new code does not open any runtime consumer or enable production installation or deletion effects.

Review found that unfinished WordPress reservations protected site names and database identities but not domain identities. Two different site names could reserve the same primary domain before metadata existed, allowing both workflows to begin effects before one terminal publication failed. The regression first failed with `primary domain reservation collision: <nil>`. WordPress operation keys now also include the reserved domain ID and FQDN. Existing complete-set locking and durable blocker checks therefore reject overlapping domain reservations before insertion. The regression also checks domain-ID collisions and absence of rejected job rows.

The sealed envelope still binds the complete reservation and durable authority. Salts remain excluded from delivered claims. Existing site-delete membership snapshots, independent purge flags, expiry, atomic terminal rollback and ordinary effectful-job restart semantics are retained. There is no production ownership bypass or artifact-execution bypass.

## Acceptance scope

Final-head `make ci`, full uncached race JSON and repeated focused Phase 4 race tests are run after this document is committed. Logs and the exact-SHA manifest live outside the repository in `/root/.hermes/cache/scratch/phase4-typed-wp-reviewed-evidence-20261031`. Preliminary focused race acceptance passed before this final documentation commit and is not substituted for final-head evidence.

This is ready for independent review as a bounded source integration candidate, not whole-Phase-4 completion. Remaining source work includes production DB and WordPress consumers, trusted provisioning identities, protected config publication/recovery, artifact descriptor custody, concrete offline fixture and reviewed PHP ini, and external site-delete cleanup. Cross-service PHP/nginx durable recovery and all-kind runtime/audit/authority acceptance remain required. Real VM and power-loss acceptance are deferred to product finish by user authorization; they do not block source implementation and remain unpassed. No host services, accounts, firewall, CA or signing keys were changed.
