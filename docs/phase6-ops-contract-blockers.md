# Phase 6 early ops contract blockers

Status: blocked before implementation. This records questions for the architecture head, not new contracts or a claim that Phase 6 is complete.

## Verified starting point and scope

- This work branched from `main` at `cc9caa48769eceaf5e19dae45309fe8909c6a67c`, the merged Phase 2 PR #75. Phase 1 is already merged in that history.
- Issues #64 and #69 explicitly allow firewall, updates, and panel self-backup to start after Phase 1. Site and database backup targets still wait for Phases 3 and 4.
- The pre-flight read covered the spec README, all foundations, canonical files 01/02/03/06, Phase 6, plan sections 2 and 3, and ADR-0011.
- No shared Phase 3 implementation files, services, production paths, firewall rules, update endpoints, or privileged integration environments were changed or exercised.

## First bounded candidate: signed update validation

Phase 6 lines 138–146 require fetching `manifest.json` and `manifest.json.sig`, Ed25519 verification before parsing, rejection of versions less than or equal to installed, and artifact size and SHA-256 verification. Foundations F16 requires a compiled-in public key. ADR-0011 establishes the same trust boundary.

The repository has no Go update verifier, release manifest schema, signature decoder, or compiled-in update public key to reuse. `internal/version/version.go` defaults the installed version to `dev`; the update URL in F16 and `internal/config/config.go` is a placeholder.

The following decisions need authoritative contracts before a verifier can claim compatibility with a release producer:

1. **Signed bytes and detached signature format.** Specify the exact signed message and the encoding of `manifest.json.sig`. No raw/base64/hex format or canonicalization rule is pinned.
2. **Trust anchor.** Supply the approved compiled-in release public key and its source representation. A test-generated key must not become the production trust anchor. Clarify any supported trust-key replacement behavior rather than accepting keys from the downloaded content.
3. **Manifest schema.** Pin the required version and artifact fields, their types and naming, artifact selection for the supported platform, and the authenticated relationship between artifact location, digest, size, and archive top directory. The RPC reply in canonical 03 is not a release-manifest schema.
4. **Anti-rollback ordering.** Pin the version grammar and comparison rules, including development builds and prereleases. Phase 6 rejects versions `<= installed`; ADR-0011 and the plan mention only older versions. Confirm the equality behavior without changing it in implementation.
5. **Apply and recovery contract.** Before implementing apply, specify which shipped binaries the artifact replaces, the independent helper invocation, retained prior artifacts, post-restart health evidence and deadline, and interrupted-swap recovery. The approved healthy rollback exception must remain distinct from permitting an arbitrary older signed release.

These are release compatibility and security contracts, not buffer sizes or log wording. The README role split and pre-flight rule require raising them, not choosing them in an implementation PR. No speculative parser, fake production key, or network updater is included.

## Other eligible ops work cannot silently bypass the same check

### Firewall confirmation

Phase 6 lines 101–105 require a one-time confirmation nonce returned by apply and submitted on a fresh request. Canonical 03 specifies apply's job result as `{pending_id}` and confirm's arguments as `{pending_id}`; canonical 02's `fw_pending` has no nonce field. The nonce's conveyance, durable verification, and consumption contract are not pinned. Do not add an argument, result field, or schema column unilaterally. Canonical 01 also keeps confirm/revert synchronous despite Phase 6 verification's broad statement that all mutations are jobs.

This does not prevent a later separately scoped read-only firewall feature, but it blocks claiming the complete safe-apply deliverable without a ruling.

### Dedicated panel repository

Canonical 02 defines `repos.role` with exactly one panel repository and Phase 6 requires an out-of-band setup password. Canonical 03's `repo.create` arguments have no panel-role selection, and its list reply exposes no role. The current main has no repository management implementation to reuse. The head must pin how the dedicated repository is created/selected before a complete self-backup path can rely on it. A live WAL-mode `panel.db` also needs a consistent snapshot, not a raw main-file copy that silently omits WAL contents.

Do not pull site/DB targets, PHP, MariaDB, or ACME registration implementation into this early slice. The ACME account secret name `acme:account` is already canonical in 02; no alternative path or format is proposed here.

## Resume and verification boundary

After the head amends the canonical contracts, resume on this Phase 6 branch with one bounded implementation and test-first changes. Preserve the F3 error catalog, canonical RPC attributes/shapes and schema, and verify-before-parse ordering. Pure crypto and artifact tests need no host changes. Any later process execution must use the injected F10 Runner, and filesystem tests must use scratch paths.

This documentation-only draft does not satisfy the global code gate or Phase 6 verification, does not close #64, and does not request architecture review under the completed-phase checklist. No feature tests or privileged acceptance results are claimed.
