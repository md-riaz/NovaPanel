# Phase 4 acceptance map and bounded staged-custody policy

Base reviewed source: `d927c57f14b9482728bd860bd30336ee5255cbaf`.
This is a local implementation slice, not Phase 4 completion or publication authority.

## Spec to implemented path and test map before implementation

| Canonical requirement | Existing implemented path | Existing test evidence | Remaining acceptance |
| --- | --- | --- | --- |
| phase-4 deliverables 1–2, lines 119–123: site UID, FPM socket ownership, PHP compatibility | `internal/php`, PHP coordinated jobs and renderers | PHP runtime/coordination fixtures and confined lab tools | Final Ubuntu cross-site/service acceptance remains deferred |
| deliverable 3, lines 124–128, 172: socket admin, loopback application DB, literal least-privilege grants | `internal/database`, typed operation journals | SQL renderer/lifecycle tests and MariaDB lab | Complete queue-owned WordPress DB/install orchestration remains absent |
| lines 164–168: approved artifact bytes, signature, complete checksum inventory | `wordpress.VerifyStaged`, `verifyStagedArchive` | `TestStagedPreflight*`, `TestStagedArchive*`, opt-in `TestStagedRetainedOfficialArtifacts` | No extraction or durable stage existed at this base |
| lines 168: restart only own files; empty unmanaged docroot refusal | config publisher owns only protected config records | `configcustody` tests and `TestWordPressConfigQueueCancellationReopen` | Add a private operation-owned extracted tree, with exact inventory and restart validation in this slice. Live docroot transfer remains gated |
| lines 168: independent salts and site-owned 0400 typed config | `jobs.CreateWordPressConfig`, `configcustody.ComposeConfig` and protected publication | config queue cancellation/reopen, terminal lifecycle and callback protocol tests | Runtime writer quiescence and trusted relocation exclusion have no production implementation |
| lines 21–23, 166–168: download, unchanged official verify, install under site UID | `wordpress.BuildInstall` is a pure plan, not a consumer | command-plan and stdin separation tests | Reviewed PHP ini, offline response/cache mechanism, isolated runner, actual install/checksum failure path remain absent. No PHP execution in this slice |
| F19 and data-model operation contract: locks, sealed input, original-ID recovery, atomic claim/metadata terminality | queue worker lease, `phase4_operations`, complete/fail/recovery store methods | queue/store reopen, audit/claim leak and terminal rollback tests | Staging must neither invent a checkpoint nor publish job completion or claims |
| phase-4 guard 6: no destructive WP operations | verb surface/architecture guards | registered verb and architecture tests | Preserve guard until Phase 6 |

## Bounded policy chosen before code

Use a dedicated root-controlled 0700 custody root outside site homes. Each stage is named by the original operation job ID, with an exclusive durable nonsecret identity record written and synced before any extracted file. The record binds job/site/docroot/UID/GID and canonical artifact evidence. It is not a SQLite checkpoint or a completion claim. A stage retry authenticates its identity, re-verifies the pinned held archive and signature, validates all existing bytes/types/permissions, refuses unexpected entries, and completes only missing approved files. No overwrite, recursive cleanup or trust of an arbitrary ZIP is permitted.

Extraction consumes the same held archive descriptor that passed verification, before it closes. Files are root-owned single-link 0444 and directories root-owned 0700. No site UID receives write access. Traversal uses no-follow directory descriptors; stage/root pathname identity is rechecked before reporting success. Failed/canceled writes can leave only an owned partial stage; partial files are never silently replaced. File and directory sync errors refuse success and retain the original operation identity. Retry revalidates and syncs exact complete contents. Trusted root relocation exclusion remains required for eventual live publication; an inode comparison is detection, not physical quiescence.

## Implemented slice and acceptance boundary

`wordpress.ExtractStaged` now consumes the verified retained archive descriptor inside
`consumeStaged`. `jobs.StageWordPress` requires the original active queue-worker
lease, validates the sealed reservation, and derives the stage binding from the
persisted operation. The complete tree remains root-owned and private. No interface
accepts caller-supplied artifact pins or a claimed verification result.

| Requirement | Concrete new path | Test | Still not accepted |
| --- | --- | --- | --- |
| Canonical verified bytes become owned files | `consumeStaged` to `extractStaged`, descriptor-relative exclusive creation | `TestExtractVerifiedStageRestart`; official queue test uses real gpgv and all 3782 files | This does not replace unchanged official WP-CLI checksum verification |
| Original-operation restart custody | synced `identity.json`, exact byte/type/mode inventory, no replacement of approved files | `TestWordPressStageOfficialQueueReopen` runs original `Queue.Run`, cancellation, SQLite and audit reopen, same file inode | No live docroot transfer or installation checkpoint |
| Refuse changed binding or untrusted stage | identity byte comparison, owner/mode/nlink checks, exact inventory | `TestExtractStageRefusesChangedCustody` covers binding, extra entry, symlink, hardlink, bytes, mode, distinct site owner | Root is trusted; this is not a kernel immutable-file guarantee |
| Detect path relocation | reopened custody root, operation directory and tree inode comparisons | root and tree replacement tests, tree replacement observed RED before fix | Eventual live publication must physically exclude relocation, not just detect it |
| Canceled/sync-failed work cannot claim success | cancellation checks and file/directory sync failures refuse, original binding retained | canceled context, cancellation after sync and sync retry tests | An incomplete 0400 file refuses restart rather than being overwritten. Automatic partial-file repair is not implemented |
| No PHP before ini/quiescence acceptance | only gpgv runner is called; stage has no executable step | existing fixed-runner checks plus real official-byte Queue.Run test | PHP ini, offline adapter, UID execution, account/FPM quiescence and core install remain required |

The official-byte queue test injects only the prior DB checkpoint. It does not
claim MariaDB provisioning. It leaves the original job interrupted with sealed
inputs and `db_applied`, not done, verified or claim-published. `StageWordPress`
can prepare data before the existing `CreateWordPressConfig` path, but no bridge
to site-writable files is enabled. Calling the config isolation callback with an
unconditional success implementation would not satisfy the remaining runtime gate.

Final checks must run after committing this document and source. Exact-head logs
are kept outside the repository. Opt-in official artifacts are not copied into git.
