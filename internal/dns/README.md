# Phase 7 DNS adapter foundation

This is a tested execution adapter, not complete Phase 7. No DNS verbs, token persistence, jobs or ACME consumers are wired.

## Contracts

- `dns.Provider` has context-aware List/Create/Update/Delete/Verify/Purge. Create returns a normalized Record with provider ID. A future worker must project only `{record_id}`. Update is a complete replacement.
- `V.DNSOwner` is separate from unchanged web `Hostname`. ASCII underscores support service TXT owners. Wildcards, whitespace, controls, empty labels and raw punctuation are refused. Owners stay inside the authorized zone.
- Types are exactly A/AAAA/CNAME/TXT/MX. SPF/DKIM/DMARC use TXT. TTL is explicitly 1 or 60..86400, MX priority 0..65535. Only A/AAAA/CNAME can be proxied. The adapter cannot infer whether a type serves mail or web.
- `Verification` serializes exactly `points_here` and `method`. A matching proxied record uses `provider` per canonical Phase 7. Otherwise the injected resolver must return a nonempty set entirely within trusted expected addresses. The adapter does not discover public IPs.
- Cloudflare API origin is fixed. Authorized zone bindings are copied. IDs are validated before path interpolation. Redirects are refused. Every HTTP response is capped at 1 MiB, each request at 30 seconds plus caller context. No retries follow ambiguous mutations.
- List is capped at ten pages of 100 records. Inconsistent pages, duplicate IDs, invalid records, out-of-zone records, missing results and invalid envelopes return errors without partial records. Unknown provider fields are ignored but never projected.
- Purge accepts at most 30 URLs of at most 2048 bytes, HTTP/HTTPS only, inside the authorized zone, without userinfo, explicit ports, fragments, whitespace or controls. Empty means explicit purge_everything. URLs are sent only as JSON, never fetched.
- Raw provider/transport errors are not exposed or logged. Tokens exist only in the execution-lifetime client. All tests inject fake HTTP and synthetic credentials.

## Integration provenance and verification

Fresh base `2ff29a1a70f0ad678969d48006a4c21c183dd29a`. Baseline source `32061f7e2c9636e36936bd26c5f01ae6b991984b` applied as `ea41ac7`; stable patch IDs both `4f57ba53cc0a2a84d2287f32f4607a3b5cc090f8` before dependent edits. Issue #65 and all zero comments were read. Owner/response decisions were amended in canonical F9 and 03 before implementation, with phase prose reconciled.

RED/GREEN exercised missing DNSOwner, service TXT refusal, Create result contract, missing operations and unrelated Create response acceptance. Fake HTTP covers CRUD, verification, purge, redirects, pagination bounds, malformed envelopes, source-zone checks and secret-free errors. `internal/store/phase7_fixture_test.go` executes canonical Phase 7 DDL in scratch SQLite and checks FK/cascade only. It does not register production DDL or claim migration readiness.

## Remaining API and acceptance gaps

- No production Phase 7 migration number is assigned. integrations/dns_zones are absent from shipped migrations; the fixture is not a migration.
- No typed integration storage/zone lookup, generated integration name policy, token scope probe, sealed credential publication, token opening owner or rotation lifecycle.
- No registry handlers, durable zone-locked ensure-state/recovery, ambiguous-create reconciliation, worker result projections, audit integration or web UI. RPC verbs remain unavailable.
- F16 has no production expected-public-IP/resolver configuration contract. Verify accepts trusted injected dependencies only.
- No lego DNS-01 glue, shared apex/wildcard exact-value cleanup or wildcard issuance acceptance. Existing HTTP-01 behavior is unchanged.
- No real Cloudflare calls, provider writes, token prompts, public CA calls, host service changes or privileged VM acceptance.

Keep the single Phase 7 PR draft. Do not mark the phase complete or request completed-phase review from adapter tests.
