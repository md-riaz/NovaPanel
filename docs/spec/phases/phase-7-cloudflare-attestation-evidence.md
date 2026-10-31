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

## Bounds and tests

The probe sends GET requests only to `https://api.cloudflare.com/client/v4`. It reuses the adapter's bounded 1 MiB response reader and secret-free transport errors. It refuses redirects, malformed envelopes, nonempty errors, incomplete pagination and mismatched identities. A single 30-second context covers all requests, with caller cancellation preserved. At most 105 requests cover 100 zones, three metadata lookups, verification and details. It does not retry or paginate and never returns partial bindings.

Fake RoundTripper tests check exact origin, GET-only behavior, Authorization delivery and deadlines. They cover known-policy acceptance, missing/unknown/spoofed permissions, absent policies, deny and broad-resource refusal, extra zones, identity mismatches, pagination, provider errors, redirects, transport errors, oversized/malformed bodies, cancellation, deadlines and invalid inputs. Race tests and vet exercise `./internal/dns/...`. No real token, live API call, provider write, public CA, or host service was used.

These tests do not complete Phase 7, DNS consumers, migration approval, durable worker/recovery acceptance, removal serialization or DNS-01.
