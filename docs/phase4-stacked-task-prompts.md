# Phase 4 stacked task prompts

> Historical pre-merge record. Current authority and implementation status are in [Phase 4 foundation handoff](phase4-foundation-handoff.md). PR #81 is now merged at `2ff29a1a70f0ad678969d48006a4c21c183dd29a`; statements below about its pending merge or test-only authorization describe the earlier snapshot.

> For Hermes: Use subagent-driven-development and strict test-driven-development for each slice. The controlling opening record is `docs/phase4-stacked-development-record.md`.

**Goal:** Produce disjoint pure PHP, SQLite and grant-rendering contracts without opening operational Phase 4 integration.

**Architecture:** Each worker branches from the local canonical planning commit in its own new scratch worktree. Consume approved docs, use the existing Go module, and return local commits only. No shared production wiring, service commands or artifact execution.

**Tech stack:** Go 1.27 module, modernc.org/sqlite v1.44.3, PHP 8.3 configuration grammar and MariaDB 10.11 grant grammar.

## Prompt A, PHP renderer and partial limits

Read `docs/phase4-stacked-development-record.md`, `docs/phase4-implementation-opening.md`, `docs/phase4-architecture-decisions.md`, `docs/spec/02-data-model.md` and `docs/spec/phases/phase-4.md`. Implement only A1/A2 using strict vertical red/green cycles. Own exactly `internal/render/php/render.go`, `render_test.go`, `limits.go`, `limits_test.go`. No existing file edits.

1. Write `TestPhase4PHPRender` and `TestPhase4PHPRenderRefusals`. Assert PHP 8.3 only, validated site/UID/GID fields, socket www-data:www-data mode 0660, no ACLs, `cgi.fix_pathinfo=0`, fixed `pm.max_children=2`, confined paths, static-site refusal and no arbitrary directive passthrough. Capture an intended missing-renderer test failure before implementing each behavior.
2. Add the smallest pure typed renderer. Document the actual exported input/output signatures in the handoff. They are new APIs, not preexisting `EnsurePHP` or `RemovePHP` functions.
3. Write `TestPhase4PHPLimitsMerge` first for defaults memory/upload/seconds 256/64/60, bounds 32..2048, 1..1024, 1..600, and upload <= memory after partial merge. Preserve omitted fields and reject the full invalid merged tuple without mutating the input.
4. Run `go test ./internal/render/php -list '^TestPhase4PHP'`, then `go test ./internal/render/php -run '^TestPhase4PHP' -count=1 -v` and `go test -race ./internal/render/php -count=1`. Require all named tests to run, with no skip or no-test substitution.
5. Return local commit SHA, exact new API signatures, changed paths and retained red/green/race logs. Do not persist limits, publish pools, create accounts, reload FPM, register php.set or edit site lifecycle.

Existing later execution seam is `internal/exec/exec.go`, `Runner.Run(context.Context,string,[]string,RunOpts)`, with Stdin/Uid/Gid/Groups. Existing publication reference is `internal/render/nginx/apply.go`, `Applier.Apply`. Neither is permission to implement operational PHP in this wave. Home 0751/docroot site:www-data 2750 and hardened-profile deferral are fixed canonical decisions, not toggles to invent.

## Prompt B, real SQLite schema contract fixture

Read the controlling record, canonical data model and opening plan. Own exactly `internal/store/phase4_schema_contract_test.go` and `internal/store/testdata/phase4_candidate.sql`. Use the exact first SQL block under `## Phase 4 databases, PHP limits and bounded recovery` in `docs/spec/02-data-model.md`; it must equal `/root/.hermes/cache/scratch/phase4-authoritative-amendment-20261031/proposed-phase4.sql` after surrounding whitespace removal. Do not add a production migration or change `internal/store/migrations/hashes.json`.

1. Write `TestPhase4SchemaContract` using real modernc SQLite in scratch after all five shipped migrations. Retain the expected missing-candidate-table failure before loading the candidate fixture.
2. Load the fixture and prove fresh/upgrade/reopen, `PRAGMA integrity_check` and empty `foreign_key_check`. Test deferred primary insertion and failed commit, cross-database primary refusal, primary deletion refusal, WordPress cross-site binding and bound detach refusal, host 127.0.0.1 restriction and PHP tuple bounds. Explicitly rollback the failed deferred-FK transaction and prove another transaction can start on the same driver connection.
3. Add SQL atomic rollback checks without pretending a typed terminal method exists. Add indexed expired-unfinished-only selection with more than 100 completed rows ordered before unfinished expired rows. Preserve completed jobs and prove no claim recreation. Recovery journal runtime, mutation admission and completion-audit integration remain unopened.
4. Run `go test ./internal/store -list '^TestPhase4SchemaContract'`, `go test ./internal/store -run '^TestPhase4SchemaContract' -count=1 -v` and `go test -race ./internal/store -run '^TestPhase4SchemaContract' -count=1`. Return local commit, fixture hash, named red/green logs, driver version and explicit test limits.

B retains exclusive future ownership of production schema, ordered lock sets, operation journal and atomic terminal APIs. Current `internal/jobs/keyedlock.go` exposes only `(*keyedLock).lock(key string) func()`; `internal/jobs/job.go` contains `Persistence`. Current `Queue.finish` and `PublishClaim` are separate writes, not atomic completion. Reserve generated resources before the complete site-first/DB-second ordered lock set, then commit verified metadata/job/result/claim/journal done/ciphertext removal atomically in the future production slice. Publish concrete typed API signatures and tests before A/C consume them. None exists merely because this prompt names the requirement.

## Prompt C, literal generated-name grant renderer

Read the controlling record, canonical decisions and `docs/spec/phases/phase-4.md`. This is the delegated pure B2 subtask, not WordPress/lifecycle work. Own exactly `internal/database/grants.go` and `internal/database/grants_test.go`. Coordinate exported signatures with B but do not edit B's files.

1. Write `TestPhase4GrantPattern` before implementation. Require generated-only database/account names and exact canonical privilege allowlist. Test the literal generated `d_` prefix with separate grant-pattern escaping for backslash, underscore and percent, distinct from identifier quoting. Reject invalid names and injection. CREATE/DROP identifier quoting must not inherit grant-pattern escaping.
2. Run `go test ./internal/database -run '^TestPhase4GrantPattern' -count=1 -v` and retain the intended missing-implementation failure. Implement the smallest pure typed renderer, not a SQL execution hook.
3. Run `go test ./internal/database -list '^TestPhase4GrantPattern'`, the same named test command and `go test -race ./internal/database -count=1`. Return exact exported API signatures, local commit, changed paths and red/green/race logs.
4. No MariaDB connection, DSN, passwords, dependency install, host command or container. Pure string tests cannot establish actual denial for a dX... lookalike of d_...; retain that as B's later real-MariaDB gate.

C's eventual WordPress/lifecycle/UI ownership waits for a separate opening and committed A/B contracts. Preserve retained WP-CLI 2.12.0 signed official digest and WordPress 7.1.4 en_US with all 3782 official checksums. Do not execute downloaded artifacts in this wave. Later config-create and core-install receive distinct passwords over stdin only; salts are local and F19 claims remain owner-bound and one-shot.

## Parent review and limits

Reject overlapping paths, unexpected runtime edits, missing red evidence, skipped selectors or invented test results. Review B fixture first, then the independent A and C pure packages. The final privileged VM ledger stays deferred and unpassed. No merge, push, PR mutation, production service or public CA authority is delegated to these workers. Rebase and rerun after the real Phase 3 merge; only the parent records that actual state.
