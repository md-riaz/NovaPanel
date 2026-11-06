# Phase 7 — DNS (external provider first)

## Goal
Manage DNS through an external provider behind a clean interface — **Cloudflare first** — with record
CRUD, a proxy toggle that is safe by construction, DNS verification, and cache purge. This also
unblocks **wildcard certs via DNS-01** (gated out of Phase 3).

## Depends on
Phases 1–3 (jobs, auth, sites/TLS). Reads: `00-foundations.md` F6, F9, F10, F11, F12.

## Deliverables
1. **`dns.Provider` interface** — one interface, provider implementations behind it; Cloudflare is the
   first and only v1 implementation.
2. **Record CRUD** (`dns.record.create/update/delete/list`): A, AAAA, CNAME, TXT, MX — validated
   (F9), as jobs, audited.
3. **Safe proxy toggle:** Cloudflare's orange-cloud proxy may be toggled **only for HTTP record types
   (A/AAAA/CNAME serving web)** — **never for MX/SPF/DKIM/DMARC** (enforced server-side, not a client
   choice; proxying mail/auth records breaks mail).
4. **DNS verification** (`dns.verify`): confirm a domain's records point here before dependent actions
   (e.g. issuance, mail later).
5. **Cache purge** (`dns.purge` / provider cache) where the provider supports it.
6. **DNS-01 for wildcards:** wire the provider into ACME DNS-01 so `ssl.issue` can obtain a `*.domain`
   cert (the Phase-3 `unsupported` path now works for wildcards).
7. **Provider credential sealed** (F12): the Cloudflare API token is stored sealed, opened only in the
   agent at point of use, scoped to the zone.

## Blueprint
- **`internal/dns/provider.go`** uses context-aware `List(ctx, zone) ([]Record,error)`, `Create(ctx, zone, Record) (Record,error)`, `Update(ctx, zone, Record) error`, `Delete(ctx, zone, recordID) error`, `Verify(ctx, domain, Resolver, expectedIPs) (Verification,error)` and `Purge(ctx, zone, urls) error`. `Verification` has only `points_here` and `method`, per canonical 03. Create's normalized record includes the provider ID and is projected to `{record_id}` by the future worker. Resolver and expected public IPs are trusted injected agent dependencies, not RPC fields; until F16 pins their production configuration, no live detection is assumed. DNS-01 PresentTXT/CleanupTXT remain the separate canonical lego solver integration, not additional methods on the record adapter.
- **`internal/dns/cloudflare/`** — implement `Provider` against the Cloudflare API (token auth; zone
  lookup; record CRUD; proxied flag only for web record types). Reuse lego's Cloudflare DNS provider
  for the DNS-01 solver, or drive `PresentTXT`/`CleanupTXT` directly — head to pick; keep the token
  path identical (sealed, agent-only).
- **`internal/verbs/dns.go`** — the `dns.*` verbs; the **proxy-toggle guard** rejects a proxied flag
  on any non-web record type with `code:"invalid_argument"`.
- **ACME DNS-01 glue** — extend `ssl.issue` (Phase 3) so a wildcard domain routes through the
  provider's `PresentTXT`/`CleanupTXT`; HTTP-01 stays the default for non-wildcard.

### Resolved types & flows (B48–B56)
```go
type Record struct {
    ID       string // provider record id (empty on create)
    Type     string // A|AAAA|CNAME|TXT|MX
    Name     string // fqdn
    Value    string // IP / target / text
    TTL      int    // 1 = auto
    Priority int    // MX only
    Proxied  bool   // only valid for A/AAAA/CNAME (guard)
}
// zone is identified by name; the impl maps name→provider_zone_id via dns_zones (02-data-model).
// Every method takes ctx. dns.record.* are SLOW (run on the worker), so that ctx is the F6
// per-attempt worker deadline (Verb.MaxDeadlineMs), NOT the caller's F2 deadline_ms (gone at enqueue).
```
- **Provenance is OWED before build (F20, head marker).** A provider DNS record is a **two-store**
  resource (provider vs SQLite) and F20 names it explicitly (`00-foundations.md` F20 intro + "Phase 7
  with the provider's own record keyed by the resource `obj_id`"). Today there is **no persisted record
  identity** — `02` has only `dns_zones`, and `Record.ID` lives only in the provider. Before Phase 7 is
  built this must get the **full MariaDB-style F20 instantiation**: a durable SQLite record row carrying
  an `obj_id`, provenance keyed by that `obj_id` (not name/existence), **`obj_id`-matched delete** (never
  name-only), reserve→create roll-forward across the crash window, and admission-vs-execution handling
  when a `dns.record.create` is queued against a zone/integration being deleted (`dns_zones` is
  `ON DELETE CASCADE`). F6 already requires idempotent **compensation** for provider DNS; F20 is **how**.
  This marker is the ruling that it is owed; the mechanism is designed at Phase-7 build, not here.
  - **Owed with it (F6 per-attempt deadline): effectful-timeout replay.** A `dns.record.create/update/
    delete` is an external/billed provider call. Today it is **not** flagged Effectful (`01`), yet a
    per-attempt `deadline_exceeded` (or crash) mid-call leaves the provider's state uncertain — a naive
    idempotent re-run would re-hit the provider and could duplicate a record. So the Phase-7 build owes,
    together with the F20 `obj_id` provenance above, the **effectful-timeout contract**: either mark
    `dns.record.*` effectful (recover as `interrupted`, reconcile the provider record by `obj_id` before
    any re-issue), or prove replay is safe purely via the owed provenance (`obj_id`-keyed upsert that
    cannot duplicate). Decide at build; do not leave the timeout path to a blind re-queue.
- **Token storage:** `integration.set{kind:"cloudflare", token, zones[]}` (admin, MFA) seals the token
  (`secrets` name `integration:<id>:token`) and records zones in `dns_zones`. The Cloudflare token
  must carry **Zone:Read + DNS:Edit + Cache Purge** on exactly the listed zones (least privilege but
  enough for the listed ops — the review's "DNS:Edit only" was too narrow). The agent does not mint
  the token; it validates scope by a probe call and reports if insufficient.
- **`dns.verify(domain)`** = a public-resolver lookup that the domain resolves to this host's public
  IP(s) (from config/detection); for a **proxied** record it instead confirms via the provider API
  that the record exists and is proxied (a plain lookup would see Cloudflare IPs). Returns which check
  was used.
- **`Purge(zone, []string)`** takes URLs (empty = purge-everything for the zone).
- **DNS-01 adapter:** `ssl.issue` for a wildcard looks up the domain's configured provider+zone; with
  none → `code:"unsupported"`. `PresentTXT/CleanupTXT` set/remove `_acme-challenge.<domain>` TXT;
  wildcard + apex share that name with two values, so Cleanup removes only the value it added. Either
  drive lego's `providers/dns/cloudflare` directly or implement the two methods — **decided: use
  lego's Cloudflare DNS provider** (same pinned lego/v4) to avoid re-implementing propagation waits.

## External refs (gathered by the head)
- Cloudflare API: token auth (scoped to a zone, least privilege — `DNS:Edit` on the one zone, not a
  global key); record objects carry a `proxied` boolean valid only for A/AAAA/CNAME; MX/TXT are never
  proxied. (Implementer confirms the current API version + the Go client/`lego` Cloudflare provider
  signatures against docs when coding.)
- DNS-01: the challenge is a TXT record at `_acme-challenge.<domain>` with the key-authorization
  digest; required for wildcards (HTTP-01 cannot issue `*.`); lego's `challenge/dns01` +
  `providers/dns/cloudflare` is the reference path.
- Never proxy mail/auth records (MX/SPF/DKIM/DMARC) — orange-cloud rewrites/hides the origin and
  breaks mail delivery and SPF alignment.

## Verification (the gate)
```
1.  Interface: a fake Provider drives the dns.* verbs in tests (no network); Cloudflare impl tested
      against a sandbox/zone or a recorded fixture.
2.  CRUD: create/update/delete/list A/AAAA/CNAME/TXT/MX with validated inputs; a malformed record
      (bad name/value) → invalid_argument, no partial write.
3.  Proxy guard: setting `proxied` on an MX/TXT (or any non-web) record → invalid_argument; allowed
      only on A/AAAA/CNAME.
4.  Verify: dns.verify correctly reports whether a domain points here.
5.  DNS-01 wildcard: ssl.issue for `*.example.com` obtains a cert via a TXT challenge (staging first);
      the TXT record is created then cleaned up; the Phase-3 wildcard `unsupported` path now succeeds.
6.  Credential: the Cloudflare token is stored sealed (ciphertext), opened only in the agent, scoped
      to the zone; panel-web never sees it. Mutating dns.* verbs are jobs; `dns.record.list`/`dns.verify`
      are sync (per 01); all are audited.
```

## Definition of Done
- [ ] `dns.Provider` interface + Cloudflare implementation; record CRUD validated + audited.
- [ ] Proxy toggle allowed only for web record types; never MX/SPF/DKIM/DMARC (server-enforced).
- [ ] dns.verify + cache purge.
- [ ] DNS-01 wildcard issuance wired into ssl.issue (staging-tested).
- [ ] Provider token sealed + zone-scoped; never in panel-web.

## Guardrails (this phase)
- **Never proxy mail/auth records.** The proxy toggle is server-gated to web record types.
- **The provider token is least-privilege (one zone, DNS edit) and sealed** — never a global API key,
  never in the web tier.
- **Wildcards only via DNS-01;** HTTP-01 stays default for non-wildcard (Phase 3).
- One provider interface — do not special-case Cloudflare logic outside its implementation package.
