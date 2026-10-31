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

The first five source/destination stable patch IDs match. Updater base `1fe8b9e` is equivalent to `ceb4e653` and was not applied twice. The updater RPC commit required one integration conflict resolution in `internal/rpc/arch_test.go`: retain both updater verbs and all three firewall verbs. Its remaining changes are unchanged. Worker source worktrees were not edited.

## Migration reservation

Reserve `0007_phase6.sql` after Phase 4's `0006` migration. Main currently stops at `0005_sites.sql`. This candidate deliberately has no production `0007` migration and does not import Phase 4 or conflate PR #87 with PR #80. Activate the migration only after the approved predecessor lands, or in an explicitly approved stacked candidate after the Phase 4 API stabilizes.

`internal/store/testdata/0007_phase6.sql` is a test-only reservation, not an embedded production migration. It contains the entire canonical Phase 6 DDL from `docs/spec/02-data-model.md`, plus the partial unique index enforcing one panel repository. The regression test proves that fresh production stores do not install these tables, that the fixture matches canonical DDL, and that the fixture installs all four tables and rejects duplicate panel repositories.

Canonical section 02 places `webhook_deliveries` in Phase 6 even though Phase 8 owns the deploy-hook replay consumer. Reserve creation here and coordinate with the Phase 8 owner before activation. Phase 8 must reuse this table, not create a second replay seen-set or duplicate migration. This is a documented ownership handoff, not a claim that the Phase 8 owner has acknowledged it. Firewall confirmation uses canonical `pending_id`, with no extra nonce column or argument.

## Runtime limits

- The compiled updater release key is unset. `update.check` and `update.apply` fail closed with `unsupported` before network access. Signed-check execution is tested with an injected test key and HTTP server. Apply remains unsupported because independent helper lifetime and durable recovery are not wired.
- `firewall.apply` remains unsupported. Confirm/revert operate only with an explicitly injected recovery dependency. Production startup does not arm a deadman, install a real ruleset or recover a pending change. The pending store adapter fails closed while its production schema is deferred.
- Repository metadata and private SQLite snapshots are internal contracts. `repo.create` and `panel.backup_config` workers are not registered. No complete panel backup, config/key archive, restore drill, snapshot ID publication or real restic success is claimed.
- Snapshot safety currently assumes trusted callers do not mutate the scratch ancestry. The sibling snapshot race/ancestry review remains pending; a passing scratch snapshot test is not proof against a mutable same-owner parent. Preserve that limitation until its reviewed fix is integrated.
- Site and MariaDB backup/restore still depend on Phase 4. No host services, production paths or public CA are touched by this integration.

## Verification

The combined package corpus includes updater, firewall, backup, store, verbs and RPC architecture tests. `make phase6-test` runs that corpus uncached with the race detector and is part of the existing Go CI workflow. `make ci` remains the whole-repository build, formatting, vet, staticcheck, tests, architecture backstop, vulnerability and systemd gate. Opt-in production-like integrations are separate acceptance, not implied by unit CI.

The reserved-schema regression was observed failing for the missing test fixture before the fixture was added, then passed. Integration does not retrospectively claim test-first development for the carried worker commits.

The first whole-repository `make ci` on `4c5ef5b` exposed a test import cycle: the in-package secret tests imported store, whose repository adapter now imports secret. Commit `f7b63eb` moved only that existing test to `secret_test` with qualified public calls. No production API changed. On `f7b63eb`, `make ci`, `make phase6-test` and `go test -race -count=1 ./...` passed. Govulncheck reported no called vulnerabilities and four vulnerabilities in required modules not reached by this code.

The signed updater dispatch opt-in test also passed with the deterministic test seed's public key derived by Node's Ed25519 implementation. An earlier invocation used the wrong test public key and correctly failed signature verification; Python key derivation was unavailable because cryptography was not installed. No key was persisted or injected into the production candidate. Ordinary race runs skip this opt-in test and production-like integration gates unless their environment flags are set.
