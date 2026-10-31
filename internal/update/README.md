# Signed updater foundation

This package includes the signed-check RPC integration and an offline apply foundation, not a completed Phase 6 updater.

## RPC integration on canonical main

`update.check` is synchronous, admin with the canonical read-only override, and token-reachable only with `system:read`. It returns only `{current, latest?, security_fix?, notes?}` from the verified signed manifest. The release trust gate runs before network access. Checks use the configured `update_manifest_url` and `deadline_default_ms`, bounded further by dispatch. Transport/body failures are sanitized before dispatch logging, without losing cancellation codes.

`update.apply` declares owner, MFA, slow, effectful, empty scope and `system` lock metadata, but refuses with `unsupported` before enqueueing even with an injected key. Its execution handler also refuses. No request can supply a key, URL or installation path. There is no separate noncanonical `update.status` verb; the canonical status reply is `update.check`.

`Ping(ctx, socket, deadlineMs)` uses the real Unix RPC client against the configured socket and deadline. Scratch socket tests prove a real RPC reply and a stalled-response deadline. This seam is not wired into a privileged helper, since helper launch outside the daemon cgroup, store lifetime transfer, durable interrupted-job reconciliation, trusted installation ownership and versioned service packaging remain blockers. The existing offline apply callback remains available for isolated tests only.

Dispatcher tests cover authorization, exact registry metadata, unset-key no-network/no-job refusal and audits. `update_signed_test.go` additionally runs the actual handler with injected HTTP transport in a separately linked test binary. Its deterministic seed is test-only. Generate its public key from `ed25519.NewKeyFromSeed(bytes.Repeat([]byte{42},32))`, padded-base64 encode it, then run:

```
NOVAPANEL_UPDATE_TEST_ANCHOR=1 go test -race -ldflags "-X novapanel/internal/update.releasePublicKeyBase64=$TEST_PUBLIC_KEY" ./internal/verbs -run '^TestUpdateSignedDispatch$' -count=1
```

This verifies signed success, bad signature, network-error no-leak behavior, configured deadline and continued apply refusal. Never use this test anchor in a release build. No production key or release download was used.

## Source and fixes

Based on canonical main `2ff29a1a70f0ad678969d48006a4c21c183dd29a`. Only `internal/update` was selected from the clean reviewed source `8f5cd185a53bdbcfbcbdff7f1c39d367e25af543`. No old branch ancestry, module files, agent/store wiring or packaging changes were merged.

Read PR80 comments and reviews, PR83 and PR84 rulings, and canonical phase-6 plus shared 00/01/02/03/06. PR84 supersedes PR83's committed-key wording. The trust key remains unset and all production package entry points fail closed with `unsupported`. Release builds must inject the owner's canonical padded-base64 Ed25519 public key through `-X novapanel/internal/update.releasePublicKeyBase64=...`. No production key was generated.

Two reproduced defects were fixed test-first:

- Signed `security_fix:null` and `notes:null` silently decoded to zero values despite their bool/string schema. The scanner now rejects explicit null without changing optional-field omission.
- A ping callback returning nil after its context expired could finalize the update and remove recovery evidence. Apply now checks context state before committing success and performs local rollback under the existing independent bounded recovery context.

New archive probes cover valid gzip, corrupt checksum, truncated gzip, PAX metadata, trailing payload and cleanup. Recovery probes preserve both the unexpected current link and its recovery record without restarting services.

## Verification

On the new canonical-base worktree:

```
go test -race ./internal/update -count=1
go vet ./internal/update
go test ./cmd/... ./internal/... -count=1
git diff --check
```

All passed. Tests use ephemeral signing keys, scratch roots, injected HTTP transports, fake service runners and injected health callbacks only. No release download or real service/firewall command ran.

Both new regression tests fail against the exact reviewed original `manifest.go` and `apply.go` using the scratch map `/root/.hermes/cache/scratch/phase6-original-overlay.json`:

```
go test -overlay=/root/.hermes/cache/scratch/phase6-original-overlay.json ./internal/update -run 'TestVerifyRejectsNullOptionalMetadata|TestApplyCannotCommitAfterHealthDeadline' -count=1 -v
```

The original returns nil for both null cases and for a cancelled successful health callback. The fixed package passes.

## Integration requirements

- Wire owner/MFA effectful `update.apply` and read-only `update.check` through the canonical registry and audit/queue contracts. Gate before queueing or I/O. Add persistent notification UI separately.
- Launch `panel-agent update-apply` outside the daemon's cgroup. Directory flock is only helper exclusion, not a proof of daemon/store lifetime ownership or job reconciliation.
- Provide a trusted, stable root-owned installation parent that unprivileged panel-web can traverse. Never accept request-supplied installation paths or public keys. The package does not secure attacker-writable parent directories.
- Change packaging to execute through the versioned `current` symlink before enabling updates. Existing service units still use fixed `/usr/sbin/novapanel-agent` and `/usr/bin/novapanel-web`, as PR83 review identified.
- Supply a real context-aware agent ping using configured `deadline_default_ms`, durable interrupted-job reconciliation, helper audit delivery and owner signer provenance. The existing offline cap is 120 seconds or a shorter caller deadline.
- Prove process/cgroup survival, real rollback health, root ownership, crash/power-loss behavior and disposable Ubuntu acceptance. Unit tests do not prove these.

No firewall or backup work was mixed into this updater defect slice. Phase 6 and PR80 remain incomplete. The separate interrupted Phase4 review report remains at `/root/.hermes/cache/scratch/phase4-independent-review-report.md`; its PHP repeated-apply reload finding is not an updater finding and was not modified here.
