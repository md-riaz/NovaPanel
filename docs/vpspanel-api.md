# NovaPanel — API Design

How NovaPanel's API should work. Companion to `vpspanel-plan.md` (§2 architecture, §4 decisions, §8
Phase-8 API). Covers the **two planes** and keeps the single-VPS scope — there are no
tenant/account/package/reseller endpoints.

```
Plane 1 — Public HTTP/REST API   → what scripts, CI and the UI call (OpenAPI 3.1, token-scoped)
Plane 2 — Agent RPC              → typed verbs over the Unix socket; the ONLY path to the system
```

The public API is a **thin, authorized façade** over agent verbs. It never exposes a raw verb, never
takes a shell string, and every mutation becomes a **job + audit event**. A public-API compromise
inherits only the scopes a token carries — and, underneath, only the fixed set of agent verbs.

---

## 1. Principles

- **Versioned, stable:** everything under `/v1`. Breaking changes → `/v2`; additive changes don't bump.
- **JSON only.** `Content-Type: application/json`; UTF-8; snake_case fields.
- **Async by default for real work.** Anything that touches the system returns **`202 Accepted` +
  a `job` reference**; the caller polls the job. Pure reads are synchronous.
- **Idempotent mutations.** Every `POST`/`DELETE` honors an `Idempotency-Key` header; a repeat with
  the same key returns the original job, never a second one.
- **Least privilege.** A token carries explicit scopes. **No scope maps to a shell or a raw agent
  verb.** Sensitive actions (restore, firewall) are excluded from tokens unless an automation grant
  is attached (§4).
- **Every mutation is audited** (actor = token name or user), and leaves a job record.
- **Errors are typed.** A stable `code` string the caller branches on, plus a human message.
- **Single server.** Resources are this VPS's sites, databases, domains, backups — no `/accounts`,
  `/packages`, `/resellers`.

---

## 2. Authentication

Two credential types, same endpoints:

| Caller | Credential | How |
|---|---|---|
| Scripts / CI / external | **API token** | `Authorization: Bearer npat_<token>` |
| The NovaPanel web UI itself | **session cookie** | `__Host-` cookie, CSRF-protected (browser only) |

**API tokens**
- Created in the UI (Security → API tokens). **Shown once**, stored **hashed** (argon2id/sha256) at
  rest, never logged, never returned again.
- Carry a **name**, a **scope set**, and an **optional expiry**. Revocable instantly.
- Prefix `npat_` so leaked tokens are greppable/scannable (like GitHub's `ghp_`).
- Rate-limited per token (§8).

**Default-deny:** every route requires an auth decision. A route with no explicit `public` marker is
rejected by the router before any handler runs (architecture-test enforced). The only unauthenticated
route is the inbound deploy webhook (§7), which authenticates by HMAC instead.

---

## 3. Conventions

**Base:** `https://<panel-host>:<port>/v1` (the panel's own TLS endpoint).

**Async job pattern** — the heart of the API:
```http
POST /v1/sites
→ 202 Accepted
{ "job": { "id": "job_8sK2", "status": "queued", "verb": "site.create" },
  "links": { "job": "/v1/jobs/job_8sK2" } }
```
Poll:
```http
GET /v1/jobs/job_8sK2
→ 200 { "id":"job_8sK2","status":"running","progress":0.6,
        "events":[{"t":"...","msg":"nginx -t passed"}], "result":null }
```
`status` ∈ `queued | running | succeeded | failed | cancelled`. On success, `result` holds the created
resource (or a link to it); on failure, a typed `error`.

**Pagination** (list endpoints): `?page=1&per_page=20` (max 100) →
```json
{ "data":[ ... ], "page":1, "per_page":20, "total":4 }
```

**Idempotency:** `Idempotency-Key: <uuid>` on every mutation. Stored with the job; a replay returns the
same job. Required for CI safety (a retried webhook/deploy must not double-apply).

**Field shapes:** timestamps RFC 3339 UTC; IDs are opaque prefixed strings (`site_`, `db_`, `job_`,
`bkp_`, `dom_`, `dep_`). IDs are not guessable sequence numbers.

---

## 4. Scopes

A token's power is the union of its scopes. Reads and writes are separate; destructive/host-risk
actions are gated harder.

| Scope | Grants |
|---|---|
| `system:read` | server status, services, metrics, job status |
| `sites:read` | list/read sites, domains, PHP, logs metadata |
| `sites:write` | create/update/delete sites, domains, SSL issue/renew |
| `deploy` | trigger deployments, read deployment history |
| `databases:read` / `databases:write` | DB + user list / create, rotate, delete |
| `dns:write` | create/update/delete DNS records (via provider) |
| `backups:read` / `backups:write` | list/create snapshots |

**Excluded from ordinary tokens** (need a separately provisioned **automation grant**, because they
can lock out or destroy and a token can't present MFA):
```
backups:restore      firewall:write      ssh:write      tokens:write      system:power (reboot)
```
These are available to a signed-in user with MFA, or to a token explicitly marked
`automation=true` whose creation required a step-up. No scope ever grants "run a command."

---

## 5. Resources (REST surface)

Representative endpoints — all under `/v1`, all mapping to agent verbs (§6). `(async)` = returns a job.

**System & jobs**
```
GET    /system                      server: cpu, memory, disk, uptime, services[]
GET    /jobs?status=running         list jobs
GET    /jobs/{id}                   one job (status, progress, events, result)
POST   /jobs/{id}/cancel     (async) request cancel
```

**Sites**
```
GET    /sites                       list sites
POST   /sites                (async) create site  → site.create
GET    /sites/{id}                   one site (runtime, root, domains, status)
PATCH  /sites/{id}           (async) update (php version, redirects, maintenance) → site.update
DELETE /sites/{id}           (async) delete site → site.delete
GET    /sites/{id}/logs?stream=access&tail=200   bounded log tail
POST   /sites/{id}/ssl       (async) issue/renew cert → ssl.issue / ssl.renew
POST   /sites/{id}/php       (async) set version/limits → php.set
```

**Deployments** (Git)
```
GET    /sites/{id}/deployments           history (commit sha, status, when)
POST   /sites/{id}/deployments   (async) deploy a ref → deploy.run  (body: {ref|commit})
GET    /deployments/{id}                 one deployment (job + logs + pinned sha)
```

**Databases**
```
GET    /databases                        list
POST   /databases               (async)  create db + least-priv user → database.create
POST   /databases/{id}/rotate   (async)  rotate user password → database.rotate_password
DELETE /databases/{id}          (async)  delete → database.delete
```

**Domains & DNS** (external provider, Cloudflare first)
```
GET    /domains
GET    /domains/{id}/dns                  records
POST   /domains/{id}/dns         (async)  create record → dns.record.create
PATCH  /domains/{id}/dns/{rid}   (async)  update → dns.record.update
DELETE /domains/{id}/dns/{rid}   (async)  delete → dns.record.delete
```
MX/SPF/DKIM/DMARC are never proxied through Cloudflare (enforced server-side, not a client choice).

**Backups**
```
GET    /backups                           snapshots
POST   /backups                 (async)   create snapshot → backup.create
POST   /backups/{id}/restore    (async)   restore (scope backups:restore + confirm token) → backup.restore
```
Restore requires a typed confirmation body (`{ "confirm": "<site-or-db-name>" }`) and takes a
pre-restore safety snapshot — the API mirrors the UI's safe-by-default rule.

**Security** (host-risk — automation grant only)
```
GET    /firewall                          current UFW rules
POST   /firewall                (async)   apply preset → firewall.apply  (dry-run + auto-revert; SSH kept)
GET    /ssh/keys                          authorized public keys
POST   /ssh/keys                (async)   add public key → sshkey.install   (public keys only)
```

There is intentionally **no** `/exec`, `/run`, `/command`, or raw-config endpoint.

---

## 6. Plane 2 — Agent RPC (internal)

The public API handlers talk to the root agent over the Unix socket; this is the only path to the
system. Verified against BHPanel's real `Daemon.php` pattern.

**Transport:** newline-delimited JSON over `unix:///run/novapanel/agent.sock` (`0660 root:novapanel`).

**Request / response**
```json
// request
{ "id":"req_…", "verb":"site.create", "args":{…}, "actor":"token:ci-deploy",
  "session":"<hex>", "deadline":"2026-…Z" }
// response
{ "ok":true, "data":{…}, "error":"", "code":"" }      // code is a STABLE string on failure
```

**Rules**
- `args` is JSON, **never interpolated**; the agent executes **argv arrays, never `sh -c`**.
- Authorization is **in the agent** (session → role check), not the web tier.
- `internal/rpc/messages.go` is the **single canonical registry**; an architecture test fails the
  build if any agent handler is reachable without a registered verb.

**Verb namespaces (single-VPS set):**
```
site.*  ssl.*  php.*  database.*  dns.*  backup.*  cron.*  files.*
firewall.*  sshkey.*  service.*  system.*  deploy.*  module.*  job.*  audit.*  session.*
```
**Forbidden permanently:** `shell.execute`, `run-command`, exec-arbitrary-string,
write-arbitrary-root-path. (Dropped from BHPanel's set: all tenancy verbs — `account.*`, `package.*`,
`reseller.*`, `collab.*`, `dnscluster.*`, `import.*`, billing.)

---

## 7. Webhooks

**Inbound deploy webhook** (Decision D1 — HMAC-SHA256, verified in the agent):
```
POST /v1/webhooks/deploy/{hook_id}     (unauthenticated route — authenticated by signature)
Headers: X-Hub-Signature-256: sha256=<hmac of raw body>,  X-Delivery-Timestamp: <unix>
```
- The route is **thin**: it forwards the **raw body + signature** to the agent's `deploy.hook` verb.
  The secret lives root-only behind the agent; the internet-facing route holds nothing.
- **Uniform refusal:** any failure (bad signature, unknown hook, stale timestamp) → one response
  (`202 "not accepted"`) so a prober can't enumerate which hook IDs exist.
- Body size-capped; timestamp+nonce replay window; POST-only; routed **before** CSRF and the sign-in
  gate (no session to spend).

**Outbound event webhooks** (later): the panel POSTs signed events (`job.succeeded`, `cert.renewed`,
`backup.failed`) to a user URL, with the same HMAC scheme — so NovaPanel is a good webhook citizen in
both directions.

---

## 8. Errors, status codes, limits

**Error body** (every non-2xx):
```json
{ "error": { "code": "ssl_dns_unverified",
             "message": "DNS for store.acme.io does not point here yet.",
             "details": { "domain": "store.acme.io" } } }
```
`code` is stable and documented; `message` is human and safe (no secrets, no paths that leak layout).

| Status | Meaning |
|---|---|
| 200 | read ok |
| 202 | mutation accepted → poll the job |
| 400 | invalid input (typed `code`) |
| 401 / 403 | missing/invalid token · valid token lacking the scope |
| 404 | no such resource (uniform — never reveals existence across auth boundaries) |
| 409 | conflict (idempotency replay mismatch, name taken) |
| 422 | semantically rejected (e.g. firewall change would drop SSH) |
| 429 | rate limited — `Retry-After` header |
| 5xx | server/agent error (agent down → 503 with `code: agent_unavailable`) |

**Rate limiting:** per token, token-bucket; `X-RateLimit-Remaining` / `-Reset` headers; `429` +
`Retry-After` on exhaustion. The login and webhook routes have their own stricter limits.

---

## 9. OpenAPI & docs

- The surface is defined by a hand-maintained **`docs/api/openapi.yaml`** (OpenAPI 3.1); the server
  validates requests against it and it generates the reference docs and client stubs.
- Each scope, error `code`, and job `verb` is documented there as the single source of truth.
- Example (create a WordPress site from CI):
```bash
curl -sS -X POST https://panel.example:8443/v1/sites \
  -H "Authorization: Bearer npat_********" \
  -H "Idempotency-Key: 7c1a-…" \
  -H "Content-Type: application/json" \
  -d '{ "type":"wordpress", "domain":"store.acme.io",
        "php":"8.3", "database":true, "ssl":true }'
# → 202 { "job": { "id":"job_8sK2", "verb":"site.create" } }
curl -sS https://panel.example:8443/v1/jobs/job_8sK2 -H "Authorization: Bearer npat_********"
# → 200 { "status":"succeeded", "result":{ "site": { "id":"site_Q4", "domain":"store.acme.io" } } }
```

---

## 10. What stays out of the API (by design)

- No raw command / config endpoint, ever.
- No tenant/account/package/reseller/billing endpoints (single-VPS product).
- No scope that grants shell or an unlisted agent verb.
- No endpoint returns a secret twice (tokens, DB/user passwords, private keys are show-once).
- Restore, firewall, SSH-key, reboot, and token-minting are **not** reachable by an ordinary token —
  they need a user+MFA or an explicit automation grant.
