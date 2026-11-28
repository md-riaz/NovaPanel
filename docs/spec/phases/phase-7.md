# Phase 7 — DNS (external provider first)

## Goal
Manage DNS through an external provider behind a clean interface — **Cloudflare first** — with record
CRUD, a proxy toggle that is safe by construction, DNS verification, and cache purge. This also
unblocks **wildcard certs via DNS-01** (gated out of Phase 3).

## Depends on
Phases 1–3 (jobs, auth, sites/TLS). Reads: `00-foundations.md` F6, F9, F10, F11, F12.

## Deliverables
0. **`dns.record.*` is effectful, with a durable identity.** `dns.record.create/update/delete` are
   **effectful** provider calls (a per-attempt timeout or crash recovers as `interrupted`; the provider
   record is reconciled by `obj_id` before any re-issue — never a blind re-run). A new **`dns_records`**
   table (SQLite) carries one row per managed record with an **`obj_id`** (the row ULID — the F20 identity,
   never name/existence) as the provenance key; delete is `obj_id`-matched (never name-only), create is
   reserve→create roll-forward, and a `dns.record.*` queued against a zone/integration being deleted
   re-validates at execution (`dns_zones` is `ON DELETE CASCADE`). The registry flag in `01` (`Eff`) and the
   `dns_records` DDL in `02` land in the **same change** that builds the verbs (registry and code must
   agree). **DoD:** the `dns_records` row + `obj_id` provenance exists and `dns.record.*` is flagged
   effectful. **Verification:** the F7 crash-replay at the enumerated stops (create/update/delete) makes
   zero duplicate provider records and never deletes an object whose `obj_id` does not match.
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
- **`internal/dns/provider.go`** — `type Provider interface { List(zone) ([]Record, error);
  Create/Update/Delete(zone, Record) error; Verify(domain) (bool, error); Purge(zone, []string)
  error; PresentTXT/CleanupTXT(fqdn, value) error }` (the last two back ACME DNS-01).
- **`internal/dns/cloudflare/`** — implement `Provider` against the Cloudflare API (token auth; zone
  lookup; record CRUD; proxied flag only for web record types). Reuse lego's Cloudflare DNS provider
  for the DNS-01 solver (**decided: lego's Cloudflare provider**, see Resolved types & flows); keep the
  token path identical (sealed, agent-only).
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
- **DNS F20 provenance.** The provider record carries `novapanel:obj_id=<obj_id>` in its comment,
  stamped atomically with create. `dns_records` carries the durable `obj_id` and `phase`
  (`reserved`/`created`/`updating`/`deleting`). `reconciled=1` means the provider matches the row;
  updates write desired values with `phase=updating` and `reconciled=0` before the provider call.
  Reconcile lists records and matches the obj_id comment before
  re-issue; an unmatched record is a conflict, never adopted. Update and delete verify obj_id ownership.
  A record operation queued against a deleted zone or integration re-validates at execution.
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
  the token; it validates scope by a **functional probe within the granted scope** and reports if
  insufficient (attestation ruled on #65: the probe proves the token can do the listed ops, it does not
  read back token metadata).
- **`dns.verify(domain)`** = a public-resolver lookup that the domain resolves to this host's public
  IP(s) (from config/detection — **OWED:** a `public_ip` config key in F16 is not yet defined; add it there when
  `dns.verify` is built); for a **proxied** record it instead confirms via the provider API
  that the record exists and is proxied (a plain lookup would see Cloudflare IPs). Returns which check
  was used.
- **`Purge(zone, []string)`** takes URLs (empty = purge-everything for the zone).
- **DNS-01 adapter:** `ssl.issue` for a wildcard looks up the domain's configured provider+zone; with
  none → `code:"unsupported"`. `PresentTXT/CleanupTXT` set/remove `_acme-challenge.<domain>` TXT;
  wildcard + apex share that name with two values, so Cleanup removes only the value it added. Either
  drive lego's `providers/dns/cloudflare` directly or implement the two methods — **decided: use
  lego's Cloudflare DNS provider** (same pinned lego/v4) to avoid re-implementing propagation waits.

## External refs (gathered by the head)
- Cloudflare API: token auth (scoped to the listed zones, least privilege — **Zone:Read + DNS:Edit +
  Cache Purge** on exactly those zones, not a global key); record objects carry a `proxied` boolean valid only for A/AAAA/CNAME; MX/TXT are never
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
- **The provider token is least-privilege (Zone:Read + DNS:Edit + Cache Purge on the listed zones) and sealed** — never a global API key,
  never in the web tier.
- **Wildcards only via DNS-01;** HTTP-01 stays default for non-wildcard (Phase 3).
- One provider interface — do not special-case Cloudflare logic outside its implementation package.
