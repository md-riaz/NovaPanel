# Phase 4 opening implementation plan

> Historical pre-merge record. Current authority and implementation status are in [Phase 4 foundation handoff](phase4-foundation-handoff.md). PR #81 is now merged at `2ff29a1a70f0ad678969d48006a4c21c183dd29a`; statements below about its pending merge or test-only authorization describe the earlier snapshot.

> For Hermes: Use subagent-driven-development to implement this plan task-by-task only after its exact opening record exists.

Goal: open independent PHP and DB work without treating external review or final VM deferral as completed acceptance.

Architecture: reuse the existing queue, SQLite store, F19 claims, typed execution and nginx apply/recovery design. Worker B alone owns shared schema/jobs/terminal changes; worker A owns PHP and worker C consumes their committed contracts. Do not introduce another scheduler or credential vault.

Tech stack: existing Go module and modernc SQLite pin, PHP 8.3, MariaDB 10.11, WP-CLI 2.12.0 and WordPress 7.1.4 en_US.

## Controlling opening record

Parent approves the documented internal decisions under the user's delegation. No additional policy questionnaire is required. The older packet's d4878578 publication question is obsolete; the publication worker owns existing draft PR #81 at candidate `82f76cc0ec17dfa8f3fc46a0536e371c5fe9b08f`.

Standard opening needs actual published exact-head source review after remote CI, separately authorized merge, the resulting main SHA, canonical amendment publication SHA and explicit slice scope. No main merge is authorized here.

If review is externally waiting, parent may instead record this exact bounded exception before dispatch:

```text
mode: stacked-independent-development
base_sha: 82f76cc0ec17dfa8f3fc46a0536e371c5fe9b08f
canonical_contract: this packet's manifest SHA-256 plus canonical-amendment.patch SHA-256
allowed: scratch worktrees; pure PHP renderer/limits tests; isolated SQLite schema tests; typed MariaDB grant renderer tests; minimal implementations for those tests
forbidden: production services; source application in the prerequisite worktree; main merge; push; PR writes; daemon wiring; operational DB/PHP/WP installation; container builds/runs unless separately opened
prerequisite_state: unmerged, publication/review/remote-CI not waived
completion: development only; rebase, review and rerun after prerequisite merge
```

This plan prepares the exception, it does not assert that parent has recorded it. Without that record, planning/validation only. Final privileged VM acceptance is already explicitly deferred to project end, never passed and never a blanket development stop.

## Actual existing seams and non-overlapping ownership

These signatures were read at the candidate; `existing-api-inventory.md` records their source lines.

- `internal/jobs/keyedlock.go` has only `(*keyedLock).lock(key string) func()`. `Queue.execute` in worker.go takes that singleton before StartJob and invokes `finish`. A full lock-set API does not exist yet. B must replace the acquisition once at that boundary, not recursively take its primary lock.
- `internal/jobs/queue.go` exposes `Enqueue`, `EnqueueSealed` through input.go, `PublishClaim(ctx,jid,fields)`, `Claim(ctx,jid,token)` and `SetClock`. Existing `PublishClaim` seals then calls the store separately; it does not implement the new atomic terminal contract.
- `internal/store/jobs.go` has `CreateJobWithClaim`, `UpdateJob`, `PublishClaim`, `ConsumeClaim`, `DeleteExpiredClaims` and private `insertClaim`/`execer`. `jobs.Persistence` lives in `internal/jobs/job.go`, not a fictional persistence.go. B owns extending it and its test doubles, all new terminal code, jobs.go and lifetime.go as required.
- `Queue.finish` records completion audit, appends a job event and calls UpdateJob separately. B must preserve that audit policy while adding the typed atomic metadata/job/F19/journal publication, including consumed-claim recovery exceptions. Calling PublishClaim followed by UpdateJob is not sufficient.
- `Queue.SetStartupRecovery` in recovery.go and the production startup hook are the recovery integration points. Existing jobs recovery must not destroy Phase 4 resumable ciphertext or clear blockers before the new startup expiry pass.
- `internal/exec/exec.go` has `Runner.Run(context.Context,string,[]string,RunOpts)` and `RunOpts.Stdin`, `Uid`, `Gid`, `Groups`. A/C use it, never shell/sudo/su/runuser or secret argv.
- `internal/platform/sites.go` has `EnsureSite(ctx,runner,sites,acme,name)`; Agent.ProvisionSite is the existing injected creation hook. `internal/platform/ubuntu.go` is ManageableUnit policy, not a PHP service implementation. B owns shared policy/wiring; A does not silently edit it.
- `internal/render/nginx/apply.go` has `Applier.Apply` and its internal recover/commit apply path. A copies the proven validate/publish/probe/restore behavior into its new PHP package, not raw nginx config parsing or a second process runner.
- C owns site_create.go/site_delete.go/site_mutations.go/site.go and web callers. A owns PHP projections in site_writes.go and new php.go; B owns new DB/journal store files and migrations. Before implementation, B publishes exact new Go signatures and their tests. `EnsurePHP`/`RemovePHP` and DB lifecycle APIs in the older prompts are proposed handoffs, not existing callable APIs.

## First independent wave, only after opening record

### Task A1: PHP renderer refusals

Files to create: `internal/render/php/render.go`, `internal/render/php/render_test.go`.
Owner: A. No shared file edits.

1. Write table tests `TestPhase4PHPRender` and `TestPhase4PHPRenderRefusals` for PHP 8.3 only, validated site/UID fields, socket www-data:www-data 0660, no ACLs, cgi.fix_pathinfo=0, fixed pm.max_children=2 and confined log paths.
2. Run `go test ./internal/render/php -run '^TestPhase4PHPRender' -count=1 -v` and retain the intended missing-implementation failure.
3. Implement the pure typed renderer only. Test exact directive values, no raw passthrough, static refusal and default compatibility behavior without hardened fields.
4. Rerun the same command and `go test -race ./internal/render/php -count=1`. Require named tests to execute, not no-test success.
5. Return isolated changed paths and red/green logs. Local commits only when the opening record includes them.

### Task A2: Pure partial-limit validation

Files to create within A's new package: `internal/render/php/limits.go`, `limits_test.go`.

1. Write `TestPhase4PHPLimitsMerge` for defaults 256/64/60, bounds 32..2048 / 1..1024 / 1..600 and upload <= memory after partial merge.
2. Run the named test and observe failure, then implement a pure merge/validate function.
3. Rerun named tests and race package tests. No database persistence, pool publication, service reload or php.set wiring in the stacked wave.

### Task B1: Isolated SQLite schema contract

Files to create: `internal/store/phase4_schema_contract_test.go` and `internal/store/testdata/phase4_candidate.sql`.
Owner: B. These are test fixtures only in stacked mode, not a production migration or hashes.json change.

1. Write `TestPhase4SchemaContract` against real scratch SQLite with all five shipped migrations and the exact approved candidate DDL from proposed-phase4.sql. The packet's standalone SQL probe is prior evidence, not this typed application regression.
2. Prove missing candidate tables fail before loading the fixture. Then load the fixture and test fresh/upgrade/reopen/integrity and FK checks.
3. Add negative tests for deferred primary insertion/commit, cross-DB primary, bound-site detach, primary deletion, host and PHP limits. Explicitly rollback a failed deferred-FK commit and prove a subsequent transaction opens on the same driver connection.
4. Add atomic rollback and indexed expiry tests with more than 100 done rows before expired unfinished rows. No completed jobs change; no claim recreation. Actual typed terminal API waits for the standard/shared-contract opening.
5. Run `go test ./internal/store -run '^TestPhase4SchemaContract' -count=1 -v` then `go test -race ./internal/store -run '^TestPhase4SchemaContract' -count=1` and return logs.

### Task B2: Literal generated-name grant rendering

Files to create: `internal/database/grants.go`, `grants_test.go`.

1. Write `TestPhase4GrantPattern` asserting the generated d_ prefix is literal in GRANT patterns, identifier quoting remains distinct, and CREATE/DROP do not inherit privilege-pattern escaping.
2. Observe failure, implement the smallest typed renderer and rerun `go test ./internal/database -run '^TestPhase4GrantPattern' -count=1 -v` plus package race tests.
3. Keep exact canonical privilege allowlist. No driver connection, host database, DSN/password logs or new dependency installation. Pure rendering cannot prove real MariaDB wildcard denial; that later runtime gate remains mandatory.

### Task P1: Parent review before wider integration

1. Check disjoint changed files, red/green evidence and all new named test execution. Do not integrate fixture DDL as a migration by accident.
2. Track actual PR #81 remote CI and completed-source review independently. Fix findings in Phase 3 before rebasing consumers.
3. After separately authorized prerequisite merge and canonical publication, record actual new base and revalidate migration numbering, API signatures and patch application.
4. Open B's production schema/ordered-lock/journal/atomic terminal work first. B publishes exact typed signatures and regression tests before A persistence/apply or C lifecycle consumers integrate.
5. Integrate B, then A, then C. No concurrent edits of B-owned shared jobs/store/Agent files. Reserve artifact/UI-only tests for C only after a separately opened slice; no runtime WP before PHP/DB contracts pass.

## Later runtime gate ownership

A owns real PHP 8.3 parsing/FPM/limits and its confined launcher. B owns real MariaDB 10.11 TCP/socket/grants/rotation/recovery and its launcher. C owns real pinned WP-CLI stdin consumers, WordPress checksums/install/core schema compatibility, combined lifecycle/UI and its collector. Lab scripts in the older packet do not exist today and their names are future requirements, not executed evidence.

Use only a separately authorized reviewed nonroot image, network none, no mounts/ports/capabilities, no-new-privileges, bounded resources and owned cleanup. Root FPM switching, www-data socket ownership and root@localhost peer auth cannot pass in that substitute. The parent owns canonical docs, full combined exact-source evidence and the final VM ledger.

## Fixed internal decisions, no questionnaire

Keep 24-hour sealed operation recovery and indexed unfinished-only 100-row expiry; resource blockers and reconciliation; generated-only DB/account names; one persisted primary account; standalone deletion snapshots; independent purge_files/purge_databases; atomic metadata/job/claim/journal terminal publication; complete site-first then DB ordered lock sets after reserved identities; exact grant allowlist and literal pattern escaping; home 0751/docroot site:www-data 2750; PHP bounds/defaults/fixed children; local salts; separate stdin passwords; signed WP-CLI and full official WordPress pins. Optional hardened PHP is deferred and unavailable. Preserve registry roles/scopes/MFA, F19 secrecy and synchronous ssl.auto_set.

No host bootstrap, installer, phpMyAdmin, extra PHP versions, destructive WordPress update/clone/staging, public CA or production services. Final VM scope is deferred, not waived. No merge, source application or push occurred in this planning assignment.
