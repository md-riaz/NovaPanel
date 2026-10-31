# Phase 6 combined integration

This is the partial implementation candidate for existing PR #80 and issue #64. It starts from merged main `2ff29a1a70f0ad678969d48006a4c21c183dd29a`. It is not a completed Phase 6 or a replacement phase PR.

## Preserved history and patch ownership

The integration merges only the original PR80 documentation commit `193a13c76db0253503d08f91674fcb3c7592f0c8` so the published branch can advance without force. Its blocker report is historical. Current canonical specifications and the limits below take precedence.

| Source | Integrated commit | Content |
| --- | --- | --- |
| ceb4e653 | 76756f2 | Offline signed updater |
| 9b60d5c | e21b648 | Pure firewall planning/recovery |
| c053a75 | 898ad91 | Firewall dispatch and durable pending adapter |
| d03dd25 | 587dba5 | WAL-consistent private snapshot |
| fad858624 | e3ab110 | Sealed repository metadata and injected restic contract |
| 9c73f307 | 8788943 | Updater RPC integration owned by the updater worker |
| 65bcc2b | 70b06f6 | Stable snapshot ancestry and consumption identity checks |
| fe860d8 | a2f468d | Fail-closed firewall confirmation and durable replay regressions |

The safety follow-up starts at exact remote PR80 head `f474346321a39496a1b6632ac8db7c7c7f51422e` in an exclusive worktree. Snapshot parent `fad8586` and firewall parent `c053a75` have matching stable patch IDs with their already-integrated counterparts. The snapshot source's secret-test external-package conversion was already byte-identical at PR80 head and was not duplicated; all other snapshot source changes are preserved. The firewall patch is unchanged. Before applying the fixes, snapshot writable-parent/rename regressions and firewall pre-cancellation refusal were run through overlays and failed for the expected unsafe behavior.

Final acceptance must run after the documentation commit, with no later tracked edits. Evidence is kept outside the tree at `/root/.hermes/cache/scratch/phase6-pr80-safety-evidence-v2/`, including exact HEAD, command exit codes, whole-repository CI, uncached full race JSON, phase6-test, signed dispatcher and independent snapshot overlay results. The PR body records actual results and final SHA after execution, not a claim of production acceptance.

The first five source/destination stable patch IDs match. Updater base `1fe8b9e` is equivalent to `ceb4e653` and was not applied twice. The updater RPC commit required one integration conflict resolution in `internal/rpc/arch_test.go`: retain both updater verbs and all three firewall verbs. Its remaining changes are unchanged. Worker source worktrees were not edited.

## Production schema stack

The explicitly approved schema-only stack carries exact published PR87 migration `0006_phase4.sql` and full production `0007_phase6.sql`. It imports no Phase 4 runtime. See `phase6-production-schema-integration.md` for immutable provenance, hashes and the incompatible PR85 version-0006 history.

Production 0007 contains the entire canonical Phase 6 DDL from `docs/spec/02-data-model.md` plus the partial unique index enforcing one panel repository. Fresh and prefix-0005/0006 upgrade tests exercise ordinary Store.Open. The retained testdata is a historical contract reference, not a runtime installer.

Canonical section 02 places `webhook_deliveries` in Phase 6 even though Phase 8 owns the deploy-hook replay consumer. Reserve creation here and coordinate with the Phase 8 owner before activation. Phase 8 must reuse this table, not create a second replay seen-set or duplicate migration. This is a documented ownership handoff, not a claim that the Phase 8 owner has acknowledged it. Firewall confirmation uses canonical `pending_id`, with no extra nonce column or argument.

## Runtime limits

- The compiled updater release key is unset. `update.check` and `update.apply` fail closed with `unsupported` before network access. Signed-check execution is tested with an injected test key and HTTP server. Apply remains unsupported because independent helper lifetime and durable recovery are not wired.
- `firewall.apply` remains unsupported. With an injected recovery dependency, confirm now returns `unsupported` for live pending IDs before cancelling the timer or consuming the ID; unknown/expired IDs retain `not_found`. Revert/Fire remain injected restoration contracts. Production startup does not arm a deadman, install a real ruleset or recover a pending change. The pending store adapter now has its production schema; public apply/recovery remain closed. Terminal-only startup now consumes ordinary Audit/Store and outcome-preserving v2 receipts; pending and inconsistent retained evidence still refuse. See phase6-terminal-startup-lifecycle.md. Scratch replay tests cover restore, audit-marker, DELETE and actual deferred-constraint COMMIT failures across reopen; they do not establish process-death recovery or independent deadman DB ownership.
- Repository metadata and private SQLite snapshots are internal contracts. `repo.create` and `panel.backup_config` workers are not registered. No complete panel backup, config/key archive, restore drill, snapshot ID publication or real restic success is claimed.
- Snapshot ancestry admission now rejects untrusted ownership and group/other-writable ancestors before SQL, with a root-owned sticky-directory exception and independent descendant checks. Creation and restic consumption revalidate path identities. Root and the agent UID must still keep exclusive custody during child pathname consumption; this is not descriptor-based child consumption or protection against a malicious trusted owner after validation.
- Site and MariaDB backup/restore still depend on Phase 4. No host services, production paths or public CA are touched by this integration.

## Non-root Unix ping regression

Both unit jobs at `2ebffc7bc62d10fe0acf09eebeef150237d870be` failed `TestPingRealSocketAndDeadline` with `update health unavailable`. The fixture left `Server.WebUID` at zero, so the canonical F1 peer gate correctly rejected the unprivileged CI process. The same test passed as root and failed as UID/GID 65534 with no supplementary groups before the fix. The fixture now explicitly trusts `uint32(os.Getuid())`; production peer authorization is unchanged.

The unit workflow explicitly requires a non-root runner and runs the real Unix socket ping/deadline regression uncached before the full gates. It retains the successful RPC assertion and the stalled-peer `context.DeadlineExceeded` assertion without skipping either path. Local least-privilege verification executes the compiled test through inherited read-only binary and owned-directory descriptors, with an isolated UID-65534-owned 0700 scratch directory and no ancestor permission changes. No host configuration, service, public CA or production key is involved.

The first acceptance on fixture-fix commit `ba40ed9` passed `make ci` and full race but exposed an existing deadline race in `make phase6-test` and repeated least-privilege runs. The socket deadline can return a timeout before the context timer publishes `ctx.Err()`. Ping now maps that RPC socket timeout to `context.DeadlineExceeded`, retaining context cancellation precedence and generic errors for other failures. The real stalled-peer assertion observed this failure before the timeout mapping was added.

This follow-up's exact-commit acceptance runs after this documentation is committed. Evidence is outside the tree at `/root/.hermes/cache/scratch/pr80-nonroot-evidence/`: root and UID-65534 ping/deadline runs, `make ci`, `make phase6-test`, and uncached full-repository race output. Remote checks must be read against the published SHA, not the preceding head; pending checks are not green.

## Saved journal and helper integration

The next candidate preserves remote head `fbd4cc2f2fbcdce496ca49f46576a10a76f4b58f`, whose two unit and two integration checks and GitGuardian check were read back as successful. Journal source `106b1df7dba00ccffbbb8b710835afa8f4dbbf24` is carried as `43bf57e`; inspection-helper source `6f3258052159912da08a54c59ad3e91cedd84c63` is carried as `8f6661d`. Both source/destination stable patch IDs match, with author identities and dates preserved.

The confined journal uses a stable flock inode, bounded canonical metadata, file and directory fsync, confirmation publication before timer cancellation, and same-ID recovery/reconciliation. Its committed process tests kill only owned scratch helpers through internal/exec. Snapshot restoration and auditing are fake adapters. The journal remains disconnected from production RPC, CLI, timers and SQLite reconciliation. Terminal records cannot be reused. Power-loss and fsync fault injection remain unverified.

The new helper command only inspects signed sealed memfds and a versioned installation. Production signing trust stays unset, service units are unchanged, and apply/recovery activation stays unsupported. Scratch child-survival and post-switch kill tests exercise existing offline apply with fake runners, not the prepare command or real systemd cgroup survival. Independent helper probes are test-only overlays with no implementation replacements.

Final candidate gates run after this documentation commit. Evidence is external at `/root/.hermes/cache/scratch/phase6-journal-helper-review/` and must be tied to the resulting full SHA. No retrospective test-first claim is made for saved commits. No Phase 4/7 paths or production migrations are added; `0007` stays testdata behind the absent production `0006`. Passing these gates does not complete Phase 6 and does not authorize merge.

## Journal confirmation fault correction

The fixed journal source `647139e8a7eeeed3b9459ffd00a8b72023ef8d49` is carried unchanged as `a41fef2`, preserving saved combined candidate `9fa9982ff2fb5435505f0be52208376596161b74` and PR80 remote head `fbd4cc2f2fbcdce496ca49f46576a10a76f4b58f` as ancestors. Both the shared journal prerequisite and the correction have matching stable patch IDs; no equivalent prerequisite was duplicated.

Confirmation now publishes recoverable `confirming` preparation before the terminal `confirmed` record. Cancellation before publication leaves the armed record and timer untouched. A directory-sync failure during preparation permits restoration after reopen. Errors after confirmed rename carry `ErrConfirmationCommitted`, and readers must successfully sync that visible commit before suppressing restoration or projecting it. These are confined file-journal guarantees, not production firewall confirmation support or power-loss acceptance.

All final-head gates run after this documentation commit. Evidence belongs outside the tree at `/root/.hermes/cache/scratch/phase6-journal-fixed-evidence/`. Independent review remains pending until its separate result is received; passing local gates alone does not authorize publication or merge. Public firewall apply/confirmation and updater apply remain unsupported, the production update trust anchor remains unset, and production migration `0007` remains deferred behind absent `0006`.

## Verification

The combined package corpus includes updater, firewall, backup, store, verbs and RPC architecture tests. `make phase6-test` runs that corpus uncached with the race detector and is part of the existing Go CI workflow. `make ci` remains the whole-repository build, formatting, vet, staticcheck, tests, architecture backstop, vulnerability and systemd gate. Opt-in production-like integrations are separate acceptance, not implied by unit CI.

The reserved-schema regression was observed failing for the missing test fixture before the fixture was added, then passed. Integration does not retrospectively claim test-first development for the carried worker commits.

The first whole-repository `make ci` on `4c5ef5b` exposed a test import cycle: the in-package secret tests imported store, whose repository adapter now imports secret. Commit `f7b63eb` moved only that existing test to `secret_test` with qualified public calls. No production API changed. On `f7b63eb`, `make ci`, `make phase6-test` and `go test -race -count=1 ./...` passed. Govulncheck reported no called vulnerabilities and four vulnerabilities in required modules not reached by this code.

The signed updater dispatch opt-in test also passed with the deterministic test seed's public key derived by Node's Ed25519 implementation. An earlier invocation used the wrong test public key and correctly failed signature verification; Python key derivation was unavailable because cryptography was not installed. No key was persisted or injected into the production candidate. Ordinary race runs skip this opt-in test and production-like integration gates unless their environment flags are set.
