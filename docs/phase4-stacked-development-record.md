# Phase 4 stacked development record

> Historical pre-merge record. Current authority and implementation status are in [Phase 4 foundation handoff](phase4-foundation-handoff.md). PR #81 is now merged at `2ff29a1a70f0ad678969d48006a4c21c183dd29a`; statements below about its pending merge or test-only authorization describe the earlier snapshot.

## Controlling authority and exact contract

The controlling parent explicitly recorded the bounded exception in this task's delegation. The user's instruction is to apply the approved canonical packet in a new isolated planning branch and prepare PHP and DB ownership for immediate implementation. This record activates only the stacked independent wave in `docs/phase4-implementation-opening.md`. It does not assert prerequisite merge, completed-source approval, remote CI success or publication to main.

```text
mode: stacked-independent-development
base_sha: 82f76cc0ec17dfa8f3fc46a0536e371c5fe9b08f
planning_branch: planning/phase4-canonical-stacked-20261031
packet: /root/.hermes/cache/scratch/phase3-phase4-final-ledger-55g8ghsn
packet_manifest_sha256: f6b5cb4a4a91715932465d3bfe3f718a3b03c47a0cc7deaf39d6821c8456509f
canonical_amendment_patch_sha256: 8a6f2e928199fd3615b23aa3db579f2e76ca6eda553fcbbc46818e5847d20566
allowed: new scratch worktrees; pure PHP renderer/limits tests; isolated SQLite schema tests; typed MariaDB grant renderer tests; minimal implementations for those tests; local commits with retained red/green evidence
forbidden: production services; changes in the prerequisite worktree; main merge; push; PR writes; daemon wiring; operational DB/PHP/WP installation; container builds/runs; public CA requests
prerequisite_state: unmerged for this exception; publication/review/remote-CI requirements not waived and not claimed passed
completion: development only; rebase, review and rerun after prerequisite merge
```

All eleven proposed canonical documents are applied byte-for-byte from the validated packet. This additional record and `docs/phase4-stacked-task-prompts.md` are local planning records. The publication worker owns PR #81, remote status and any later prerequisite merge. No new questionnaire or additional user approval is needed for the already-approved narrow wave.

Final privileged Ubuntu VM acceptance remains explicitly deferred to project end and unpassed, including site UID separation, production path permissions, root-FPM switching and socket ownership, root MariaDB unix_socket authentication, systemd/reboot and power-loss checks. The unresolved Phase 3 browser intermittent failure is accepted only as disclosed development risk, not repaired by repeated passes. Optional hardened PHP remains deferred and unavailable.

## Scope and ownership

- Worker A owns only `internal/render/php/{render,render_test,limits,limits_test}.go` in this wave.
- Worker B owns only `internal/store/phase4_schema_contract_test.go` and `internal/store/testdata/phase4_candidate.sql` in this wave. No migration or hash registry changes.
- Worker C owns only `internal/database/{grants,grants_test}.go` in this wave. This delegates the pure B2 renderer subtask, not DB orchestration or shared-file ownership. B remains the future schema/jobs/lock/journal owner.
- Parent owns canonical documentation and reviews returned local commits. Do not integrate any worker runtime change into this documentation-only planning commit.

Use separate new worktrees rooted at the planning commit containing this record. Return the exact commit SHA, changed paths, named-test red/green logs and race results. A candidate production API is not an existing callable interface. Do not edit shared store/jobs/Agent files or create placeholder wiring while the narrow exception is active.

After actual prerequisite merge, the parent must record the real merge base and canonical publication SHA, recheck migration numbering and APIs, rerun the exact-source gates and separately open wider implementation. Integrate future production shared contracts through B first, PHP persistence/publication through A second, and WordPress/lifecycle/UI consumers last.
