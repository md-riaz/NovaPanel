# Phase 4 resumed integration

## Scope and patch ancestry

This exclusive integration starts at combined candidate `65fe7bdea45b3c86b0ab6a5f330524dec617855a`, preserving PR87 head `42dfcae0f9ec92989a5a82629c03a24a3ef0c8a2` and actual remote main `2ff29a1a70f0ad678969d48006a4c21c183dd29a` as ancestors. Remote main and PR87 were fetched and read before integration. PR87 was open and unmerged, with head branch `feat/phase4-durable-foundation`.

Only missing sibling patches were applied:

| Source | Integrated | Stable patch ID |
| --- | --- | --- |
| `783883761e4816e90710a3fee0e92669ae2a3de9` | `693ae6b` | `1a5275b6e3a7b7b670220d106e36747ebef7c748` |
| `4446c6c10271c4eb3e9be03474a6154b10a345eb` | `e2d864a` | `5954b99a25ff44fd04a7eff16d95d7f440a7faf2` |

The common admission patch `a406c53` already exists as `32277b3`, with stable patch ID `cbb0355002d3a4d1edc6ae2717d169143211ff51`, and was not reapplied. The two new source/destination patches match, including author identity and date. No source adaptation, branch merge, WordPress-pure change or migration/hash change was needed.

## Combined behavior and review boundary

The blocker patch maps typed expiry to interrupted, atomically removes expired recovery input and claim reservations, and rechecks active attached-site eligibility before effects and terminal publication. The tracked real Queue.Run regressions retain the original independent review's deadline-after-startup, changed-membership, zero-command, terminal rollback, reopen and one-shot-claim assertions. Their admission setup follows the newer startup sequence rather than stopping at its gate.

The startup patch performs synchronous recovery before listener publication. An unresolved recoverable journal leaves authorized read RPC available while blocking mutation RPC, ordinary/sealed/operation admission and scheduler ticks. Successful startup is reused by Queue.Run. Attached database discovery takes the site lock, then reacquires the complete ordered set and revalidates membership. Existing site deletion refuses attached databases before effects until its typed durable journal exists.

Focused combined race tests for store, jobs, Agent and panel-agent passed at `e2d864a`. That result is preliminary and is not exact-final-head acceptance. Final gates run only after committing this document, with logs and a manifest outside the repository keyed to the complete final SHA. A failed final-head attempt is retained rather than replaced by an earlier sibling pass.

This handoff supersedes the publication hold and missing-global-startup statements in the historical `phase4-admission-restored-integration.md` only for the two integrated fixes and startup boundary. It does not grant independent approval or full Phase 4 completion. Detailed historical source evidence remains in the sibling handoffs and scratch review report `phase4-f30-independent-review-report.md`.

## Publication and remaining gates

The user authorized an existing-PR87 update only after combined exact-head gates pass. Before any non-force push, re-read current PR state, fetch its exact branch and remote main, and require both to remain ancestors. Preserve current remote PR content when adding this bounded update. No main merge is authorized without independent approval.

DB and WordPress consumers remain gated. Missing acceptance includes all-kind real runtime recovery, authorized reconciliation, typed WordPress/site-delete journals, protected WordPress config recovery, durable cross-service PHP/nginx crash recovery, system-global FPM validation, root FPM UID/socket custody, cross-site private-file denial, production MariaDB unix_socket authentication and privileged Ubuntu/power-loss checks. Store-error mapping now covers the integrated typed operation boundary, not every future consumer. Same-UID confined PHP labs cannot close privileged acceptance. No production services or public CA may be used.
