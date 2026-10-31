# Phase 7 sealed integration preparation

This slice integrates `5dfe663d249df51bed1e0ab1dcee88a9d1f15cc8` with fixed PR88 head `b50984e`. It does not complete Phase 7.

## Canonical schema and lifetime

Canonical `02-data-model.md` now documents the minimal ownership companion. Its exact parent is `integrations(id)` with `ON DELETE CASCADE`; `user_id` references `users(id)` with `ON DELETE RESTRICT`. Exactly one user or local-root ownership mode is permitted. The generated integration ULID is the immutable credential lifetime. Credential reads require the exact lifetime and zone. Removing and re-adding the same zone never authorizes an old job against the replacement token.

Canonical zones are not globally unique. This slice preserves multiple provider entries and all existing `config_json`; it removes the preparation branch's extra unique-zone restriction. Future zone-only consumers must return `conflict` on ambiguous selection rather than pick the first integration. They must bind the chosen lifetime durably and serialize removal with provider execution.

Scratch fixtures execute canonical target DDL explicitly. There is no production migration `0008`, no modification to prior migrations or hashes, and no startup installation of Phase 7 tables. Phase 4 reserves `0006` and Phase 6 reserves `0007`.

## Admission contract

`integration.set` is registered with canonical 01 attributes: admin, synchronous, mutating, MFA, no PAT scope. Its public arguments and result match canonical 03 exactly. Unknown fields are rejected. Tokens, lowercase zone names and duplicates are validated before a trusted injected scope probe. The probe must attest Zone:Read, DNS:Edit and Cache Purge for exactly the requested zones without writes. The handler rejects wrong/extra bindings, sanitizes probe failures, seals execution-only token bytes and calls the atomic typed store publication API. Non-root authority must still be a current enabled admin/owner at publication.

Production leaves `Agent.IntegrationProbe` nil and returns `unsupported`. The experimental fixed-origin Cloudflare permission parser is not wired into production. Its read-only introspection endpoints require extra token-management permissions beyond the canonical minimum, so synthetic acceptance does not establish a usable minimum-scope admission path. See `phase-7-cloudflare-attestation-evidence.md`. Do not request broader credentials to bypass this blocker.

`integration.test` and `integration.delete` do not exist in canonical 01/03. Dispatcher tests explicitly verify they remain unreachable; deletion coverage uses the typed store API. Adding public forms requires head-approved attributes and shapes first. This is an API gap, not a completed deletion RPC.

## Verification and remaining gates

Tests exercise production dispatch, scratch SQLite, file audit and its existing mirror, canonical argument rejection, role/MFA/PAT denial, unavailable production admission, fake-probe success/refusal, wrong/extra zone binding, deadline errors, transactional publication failure, token destruction, sealed round trips and removal. Store tests retain reopen, lifetime rebinding, owner FK, demotion/disable refusal, rollback and shutdown checks.

Resolved-session acceptance also exercises a stepped-up admin through the production dispatcher with a synthetic trusted probe, including demotion during the probe, all-table rollback on publication failure, sealed round trip, removal and lifetime refusal. Both ordinary and automation PATs remain refused. These are scratch acceptance tests, not live provider evidence.

Still required: an approved minimum-permission attestation path, approved migration sequencing, authority-bearing queue integration, removal/rotation locking, durable recovery and publication, DNS consumers, resolver configuration and DNS-01. No DNS create/effectful consumer is enabled. Tests perform no real provider calls, token prompts, service changes or public CA calls.
