# Cloudflare permission attestation evidence

## Outcome

`internal/dns/cloudflare/probe.go` implements an experimental `dns.IntegrationProbe`. Production still leaves `Agent.IntegrationProbe` nil. No startup wiring, public verb, schema, store, migration or provider mutation changed.

The probe accepts a synthetic fully-known policy only after token verification, exact zone lookup, provider permission-group metadata resolution and token-policy inspection. Successful token verification or zone/DNS reads alone never establish DNS edit or cache purge. Every failure returns an empty integration spec. Unsupported or incomplete evidence returns `*cloudflare.AttestationError`, detectable with `errors.Is(err, cloudflare.ErrAttestationUnsupported)`. The existing handler sanitizes non-context probe errors into its existing forbidden response. Production nil configuration retains unsupported.

## Official sources and mapping

Retrieved from Cloudflare's official documentation during implementation, without credentials or live provider API requests:

- [Verify Token](https://developers.cloudflare.com/api/resources/user/subresources/tokens/methods/verify/) documents `GET /user/tokens/verify` with ID, status and optional validity times. It does not attest policy permissions or exact zone scope.
- [Token Details](https://developers.cloudflare.com/api/resources/user/subresources/tokens/methods/get/) documents `GET /user/tokens/{token_id}`, returning policy effects, resources and permission-group IDs. It requires **API Tokens Read or API Tokens Write**, beyond the canonical minimum zone permissions.
- [List Token Permission Groups](https://developers.cloudflare.com/api/resources/user/subresources/tokens/subresources/permission_groups/methods/list/) describes the catalog of available groups, not a token's grants. It also requires **API Tokens Read or API Tokens Write**. The probe uses exact name and zone-scope filters and requires a complete singleton result for each group before comparing IDs in the token's policies. It never trusts policy display names as identifiers.
- [API token permissions](https://developers.cloudflare.com/fundamentals/api/reference/permissions/) names Zone Read, DNS Write and Cache Purge. [Create API token](https://developers.cloudflare.com/fundamentals/api/get-started/create-token/) describes dashboard Edit as CRUDL and distinguishes Read.
- [Create DNS Record](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/create/) requires DNS Write. This is the provider mapping for canonical DNS:Edit, not DNS Read.
- [List Zones](https://developers.cloudflare.com/api/resources/zones/methods/list/) documents name filtering and zone access. Its accepted-permission label currently renders as `Zone Zone Read`, while the permission catalog calls the group `Zone Read`. The probe does not guess aliases.
- [Purge Cached Content](https://developers.cloudflare.com/api/resources/cache/methods/purge/) documents an effectful POST, not a read-only capability check. The probe never calls it.
- [Create tokens via API](https://developers.cloudflare.com/fundamentals/api/how-to/create-via-api/) documents exact-zone resource keys, zone wildcards and nested account-wide resources. Only explicit exact-zone keys are accepted here.

## Why production remains disabled

The retrieved read-only introspection endpoints require extra token-management permissions. No official minimum-scope self-attestation endpoint was established. The probe deliberately does not request those extra privileges, accept global credentials, add another credential or infer grants from read success. Its exact-policy allowlist rejects extra permission groups, including token-management groups. Thus the synthetic successful response is a parser contract test, not evidence that this response is obtainable with a real least-privilege token.

Production enablement requires an approved provider evidence path that works with canonical Zone:Read + DNS:Edit + Cache Purge on exactly the selected zones. Account-owned token introspection, alternate permission names, wildcard/account policies and deny-policy evaluation remain unsupported, not silently approximated. Permission metadata may change, which must fail closed rather than substitute guessed IDs. The parser does not claim a provider policy cannot change between reads or after admission.

## Schema review and minimal owner decision

A second documentation-only review found that List Zones requires `per_page` in 5..50. The experimental request now uses 5 rather than 2, with a regression assertion on the exact query. It still demands exactly one matching zone and complete singleton pagination. The permission-group endpoint documents only `name` and `scope` filters, with optional `result_info` containing `count`, `page`, `per_page`, and `total_count`. Unlike List Zones, it does not document `total_pages`. The probe does not invent pagination query parameters or require that field for permission groups. Missing singleton counts remain unsupported even though the provider schema allows their omission. This is a deliberately narrower evidence contract, not a claim that every valid provider response is accepted.

Verify and token details return an object in `result`; zones and permission groups return arrays. Token details' `policies`, and permission groups' `id`, `name` and `scopes`, are optional in the provider schemas. The probe requires the evidence it needs rather than treating omission as a grant. Permission policy display names do not establish group identity. The envelope schemas define structured `errors` and `messages`; their generated 200 examples even contain placeholder nonempty errors. The probe refuses nonempty errors regardless of HTTP success, ignores messages as authorization evidence, and sanitizes HTTP denial bodies without relying on an undocumented provider error code. No live denial status or error payload was observed.

The List Zones schema also exposes a deprecated `permissions` array described as legacy user-membership permissions, replaced by Account memberships. Its example `#worker:read` does not document an exhaustive token-grant vocabulary or bind all token resources. It is not evidence of DNS Write, Cache Purge, or absence of access to unselected zones. Reading selected zones cannot prove that no other zones are authorized.

The smallest owner decision is to retain the canonical minimum permissions and leave production admission disabled while accepting this change only as experimental parser coverage. This note requests that decision; it does not record owner approval. No credential collection or live probe is needed to make it. If production admission is required, the owner must first approve an evidence design and amend canonical contracts before implementation. That design must bind the executing token to the exact selected zones, establish all required grants and exclusion of broader grants, define evidence freshness and revalidation, and specify failure behavior. An external administrative attestation would introduce a new trust/custody boundary and is only an unapproved design option, not an implemented fallback. Adding API Tokens Read/Write to the submitted production token, accepting dashboard assertions as proof, or trying DNS writes/cache purge is not this decision.

Until an approved path exists, keep `Agent.IntegrationProbe` nil and preserve unsupported behavior. Do not add public test/delete verbs, a second administrative credential, a production migration, or startup wiring to bypass this blocker. Account-owned token APIs remain unevaluated and unsupported. The independent token-format redaction fix must be combined and reviewed separately before publication; this note does not attest its safety.

## Bounds and tests

The probe sends GET requests only to `https://api.cloudflare.com/client/v4`. It reuses the adapter's bounded 1 MiB response reader and secret-free transport errors. It refuses redirects, malformed envelopes, nonempty errors, incomplete pagination and mismatched identities. A single 30-second context covers all requests, with caller cancellation preserved. At most 105 requests cover 100 zones, three metadata lookups, verification and details. It does not retry or paginate and never returns partial bindings.

Fake RoundTripper tests check exact origin, GET-only behavior, Authorization delivery and deadlines. They cover known-policy acceptance, missing/unknown/spoofed permissions, absent policies, deny and broad-resource refusal, extra zones, identity mismatches, pagination, provider errors, redirects, transport errors, oversized/malformed bodies, cancellation, deadlines and invalid inputs. Race tests and vet exercise `./internal/dns/...`. No real token, live API call, provider write, public CA, or host service was used.

These tests do not complete Phase 7, DNS consumers, migration approval, durable worker/recovery acceptance, removal serialization or DNS-01.
