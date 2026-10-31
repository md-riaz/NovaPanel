# Phase 7 pure DNS slice

Base `2ff29a1a70f0ad678969d48006a4c21c183dd29a`. This package is not registered or wired into the agent, web, store, jobs or ACME. Only NEW `internal/dns/**` is owned by this slice.

## Implemented contract

```go
type Record struct {
    ID, Type, Name, Value string
    TTL, Priority int
    Proxied bool
}
func (Record) Validate(zone string) error
// package cloudflare
func New(token string, zones map[string]string, transport http.RoundTripper) (*Client, error)
func (*Client) Create(ctx context.Context, zone string, record dns.Record) (string, error)
```

`Record` matches phase-7 resolved fields. `Create` is a low-level request adapter returning the provider ID, not the canonical final `dns.Provider`, not a registered handler and not an idempotent worker. Its caller must eventually supply sealed token opening, durable zone bindings, registry checks, zone locking, ensure-state/recovery and audit. Do not automatically retry ambiguous external creation.

The HTTP test seam is the standard RoundTripper. All tests use synthetic responses and a conspicuously non-real credential. No provider call is made by these tests. A real caller can use the default transport by passing nil, but no production caller exists. Origin is fixed to `https://api.cloudflare.com/client/v4`, authorized zone bindings are copied, path IDs are 32 lowercase hex characters, redirects are refused, context propagates, response bytes are bounded to 1 MiB and raw provider/transport errors are not returned or logged. Token exists only in this execution-lifetime client. This is not proof of sealed persistent credential storage.

Reuse: `internal/validate.V.Hostname`, `internal/rpcerr`, `net/netip`, standard HTTP/JSON. No dependency or module-file edits. Cloudflare API field `content` maps from canonical `Record.Value`; priority is emitted only for MX. Ordinary hostname records are confined to the authorized zone. Proxy is allowed only for A/AAAA/CNAME, never TXT/MX.

Micro-details: omitted TTL must be converted by a future typed RPC parser to explicit 1, not silently accepted as zero at the sink. Standard Cloudflare zone TTL is 1 or 60..86400. Enterprise-only 30-second TTL is not enabled. MX priority is 0..65535 and forbidden for other types. IPv6 zone identifiers and IPv4-mapped AAAA values are refused. TXT rejects invalid UTF-8, NUL/CR/LF. No extra HTTP retries are introduced.

Provider request reference checked during preflight: https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/create/ . POST `/zones/{zone_id}/dns_records`, bearer token, `type/name/content/ttl/proxied`, MX priority. No Global API key auth is implemented.

## Canonical gaps requiring parent decision before broader consumers

1. F9 `00-foundations.md:378-381` requires the shared validator and explicitly rejects underscores in hostnames. Phase7 `16-18,65-68` requires DKIM/DMARC and `_acme-challenge` owners. A DNS owner name is not an RFC1123 hostname. This slice refuses underscore owners instead of bypassing F9. Parent should approve a narrowly typed DNS-owner validator and amend canonical F9 before service/auth TXT and DNS-01 consumers. Do not infer arbitrary underscore/wildcard acceptance.
2. Phase7 blueprint `28-30` says Create returns only error and Verify returns bool, while canonical 03 `166` requires record_id, and `170` requires method resolver/provider. No final Provider interface is exported here. Parent should pin context-aware method results, zone resolution and verification result before verb wiring, rather than treating this request adapter as the final interface.
3. Phase7 `59-62` needs this host's public IPs from config/detection, but F16 has no public-IP/detection contract. No live resolver or public-IP inference is implemented.
4. The proxy guard is type-based. Determining whether an A/AAAA/CNAME serves web rather than mail cannot be established from Record alone. Do not invent a name blacklist as a substitute for the canonical type guard.
5. `integration.set` shape has kind/token/zones while integration storage requires name. Future owner must agree generated naming and zone-scope probe policy. Do not add RPC fields or a migration silently.

The chosen lego DNS-01 provider is already canonical at phase7 `67-68`. This slice does not reimplement propagation or TXT cleanup. Future solver integration must use the same pinned lego/v4, same sealed agent token path and exact-value cleanup for shared apex/wildcard challenge owners.

## Exercised evidence

- RED `go test ./internal/dns/...` failed because the new package lacked implementation.
- GREEN record validation passed.
- RED Cloudflare tests failed because the adapter package lacked implementation.
- GREEN fake-HTTP request/error/context cases passed.
- RED caller-supplied create ID reached transport; GREEN now refuses before HTTP.
- `go test -race -count=1 ./internal/dns/...` passed both packages.
- `go vet ./internal/dns/...` passed.
- `go test ./...` passed all listed packages, including architecture and integration package tests on this snapshot. This is not real Cloudflare, DNS-01, live-VM or full Phase7 acceptance.
- `git diff --check` passed.

## Next owned slice

After parent agrees canonical DNS-owner and Provider contracts, add bounded fake-HTTP list/update/delete/purge and resolver/provider verification. Keep Cloudflare IDs distinct from panel ULIDs. Preserve one phase PR through the authorized integration owner. Store/migrations, seal-key custody, registry/worker, ACME and web stay unowned by this slice until explicit handoff. No actual provider writes, secret prompts, host services, public CA, push or PR write occurred.
