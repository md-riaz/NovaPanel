# Combined admission and restored-service candidate

## Integration record

The exclusive `integration/phase4-admission-restored-unique` worktree starts at `f30a212145cd7b053c9f1612548d16982f10831b`. It preserves the original PR87 head `42dfcae0f9ec92989a5a82629c03a24a3ef0c8a2` as an ancestor. Only missing committed patches were cherry-picked, without a branch merge or source adaptations.

| Source | Destination | Scope |
| --- | --- | --- |
| a406c530 | 32277b3 | Bounded operation startup recovery, admission and credential envelope |
| c2552b9 | 7d22e10 | Restored PHP probing and first-create probe suppression |
| a8b75f2 | 6afa42c | Confined real-service coordinator lab |
| ce41317 | 4f3836f | Lab error naming |
| 96384f0 | 704748c | Lab nginx pidfile distinction |
| 92b6033 | 6c71c70 | Owned FPM signal handling |
| c85f2b3 | 8d9ff52 | Executed limit and restored-probe assertions |
| 1436b0e | e218e3c | Asynchronous reload settling |
| 0d2e95fc | bcf05af | Historical worker acceptance handoff |

All nine source/destination stable patch IDs match. Author identity and author date were preserved. The detailed patch map is retained outside the repository as `phase4-admission-restored-patch-map.json` in scratch. Production migration SQL and hashes remain unchanged against `42dfcae`.

The operation boundary drains batches and recovers operation journals before opening operation admission. It does not gate every mutation or prevent listener publication. The PHP change probes proven prior services after rollback, not nonexistent first-create services. The confined labs remain same-UID in-process acceptance, not persisted cross-service recovery.

## Review blockers and publication hold

The independent review of `f30a212`, retained in scratch as `phase4-f30-independent-review-report.md`, still requires sibling-owned fixes for:

1. Deadline crossing after startup can record `agent_error` instead of atomic `interrupted` expiry cleanup.
2. Attached database creation does not recheck changed site eligibility before external effects and terminal publication.

Those fixes are not part of this integration owner's source scope. Do not call either blocker closed because the regular suite passes. Canonical store-error mapping, authorized reconciliation, the synchronous global pre-listener recovery boundary and common mutation gate remain incomplete. No database or WordPress consumers are enabled. No phase completion, production service, live database, public CA, main merge or release approval is authorized by this candidate.

PR87 was open with head `42dfcae` when integration began. Its actual API draft flag was false despite historical draft wording. This task prepares an update and does not mark it approved or ready. Publication remains held pending sibling fixes and review. Any later publication must refresh the exact remote branch and PR state, prove every prior head is an ancestor, use a non-force push, and read back the branch and PR head.

## Exact-commit acceptance contract

This documentation is committed before final gates. Gate results belong to the complete commit containing this document, including its documentation, not merely to the executable source revision `1436b0e` cited by the historical worker handoff. No test result in that historical handoff is promoted to acceptance of this combined candidate.

Run `make ci`, uncached `go test -race -count=1 -json ./...`, packaging checks, release-stage build with artifact enumeration, and both the confined coordinator and production-renderer labs. Store logs and a manifest outside the repository, keyed to `git rev-parse HEAD`, so recording results does not move the tested commit. Parse race JSON for actual package/test outcomes and report skips separately. Preserve any failed attempt. The final report must name the exact commit, command outcomes, artifact counts and remaining blockers.

The labs use the pinned package and image, UID 33, network none, read-only root, dropped capabilities, no host binds or published ports, bounded resources and owned-container teardown with absence readback. These constraints do not describe the ordinary host-side unit/build gates. Privileged Ubuntu, root FPM UID switching, cross-site custody, real MariaDB, persisted full PHP/nginx crash recovery and power-loss gates remain unpassed.
