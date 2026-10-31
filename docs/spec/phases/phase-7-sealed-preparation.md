# Phase 7 sealed integration preparation

This slice is not a live integration endpoint. It adds no production migration, registry handler, provider call, credential prompt or queue mutation.

## Internal contracts

- `dns.IntegrationSpec.Validate` accepts only Cloudflare, 1..100 lowercase hostname zones, strict 32-character lowercase hexadecimal provider IDs and unique names/IDs. It rejects input rather than repairing identifiers.
- `dns.NewIntegrationToken` owns an execution-only copy of a bounded printable token. Formatting and JSON return `[REDACTED]`. `ExecutionBytes` returns a caller-owned copy that must be cleared; `Destroy` clears the original.
- `store.CreateDNSIntegration(ctx, authority, spec, sealed)` atomically creates a generated integration lifetime, generated nonsecret name, owner membership, zones and `secrets` entry named `integration:<id>:token`. There is no arbitrary config JSON or caller-supplied integration ID. The store cannot authenticate opaque ciphertext or prove provider scope.
- `store.DNSIntegrationToken(ctx, authority, zone, integrationID)` requires exact zone membership and the exact original integration lifetime. Removing and re-adding the same zone creates a different lifetime, so an old job cannot resolve the replacement token. It returns only bounded ciphertext, with formatting and JSON redaction.
- `store.DeleteDNSIntegration` atomically removes the credential and integration; zone and owner rows cascade. Deletion failures roll back secret cleanup.
- Trusted authority is either local root or a current enabled admin/owner user. Non-root access must match the persisted owner. Local root can repair/remove any integration. These types are internal dependencies, not RPC arguments. MFA, session/PAT expiry, scopes and durable job authorization are not replaced by this check.
- All SQL uses tracked transactions. Writes acquire the SQLite write lock before current authority checks and publication time. Closed-store admission precedes input validation; cancellation/deadline errors survive error sanitization. Other failures never expose driver strings.

## Scratch schema proposal, not approved DDL

Tests load canonical `02-data-model.md` integrations/dns_zones DDL. A test-only `integration_owners` companion has an integration FK with cascade, a user FK with restrict and an exclusive local-root/user ownership check. A test-only unique zone-name index makes one zone belong to one integration. Store writes independently check duplicates under the write lock.

Canonical 02 does not yet define ownership or unique zone membership. These additions need head approval and canonical reconciliation before production use. Direct SQL deletion of an integration does not clean the polymorphic `secrets` table; use the typed delete API. The current API intentionally has no replace/rotate operation.

Phase 4 reserves `0006`, Phase 6 reserves `0007`; Phase 7 may propose `0008` only after predecessors and canonical schema are approved. This PR must not ship a migration that skips missing predecessors. Existing migration files/hashes are unchanged.

## Future scope-probe and worker boundary

Before `integration.set` can be registered, the agent must validate the token shape, use an injected probe to discover exact requested zone bindings and prove Zone:Read, DNS:Edit and Cache Purge scope without provider writes, then seal and atomically publish. Fake-provider tests must cover invalid/insufficient tokens, deadline expiry and zero publication on probe/seal failure. No live probe is implemented here; accepting stored bindings is not evidence of provider permissions.

The proposed execution plan contains only the integration lifetime and zone membership, not token plaintext. Queue admission must persist resolved authority and bind that lifetime. Execution must reauthorize session/PAT/user state, acquire the zone lock, re-read membership and sealed token, open only at point of use and clear plaintext. Concurrent removal/rotation must use that same lock; a standalone snapshot lookup does not protect a subsequent provider write. Durable retries, ambiguous provider outcomes and terminal audit remain unimplemented.

Do not add `integration.test` as an RPC. Canonical 01/03 define `integration.set`, not that verb. Future rejection tests should use a fake injected probe and the canonical endpoint, without real tokens or network. These credentials are supplied by the caller, not generated show-once output; F19 does not authorize returning them in claims, results or audit.

## Verified and outstanding

Tests exercise real scratch SQLite, AES-GCM round trips across close/reopen, ownership FKs, demotion/disable refusal, exact membership, stale lifetime refusal after removal/re-add, duplicate publication races, expired contexts, blocked admission cancellation on close, rollback on publication/deletion failure and formatting/JSON redaction. No tests use real provider credentials.

Still required: canonical schema approval, full authority-bearing queue integration, fake-probe admission acceptance, token rotation and revocation, file/SQLite audit acceptance, all DNS handlers and job locks, resolver configuration, DNS-01 and privileged acceptance. PR88 stays draft; the Phase 7 completion gate is not met.
