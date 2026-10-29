# Phase 8 — Integrations & public API

## Goal
A versioned, token-scoped **public REST API** (`/v1`) that is a thin authorized façade over agent
verbs, plus the **inbound deploy webhook** (D1). Scripts/CI can drive the panel safely; a compromised
token inherits only its scopes, and underneath only the fixed agent verbs.

## Depends on
Phases 1–6, **and Phase 7** for the `dns:*` scopes/resources (gate those routes on a configured DNS
provider, else `unsupported`). **Required reading (not optional background): `../vpspanel-api.md`** —
it is the authoritative route table (§5 paths/methods/bodies), scope list (§4) and status-code table
(§8); every route maps 1:1 to a verb in `01-rpc-and-verbs.md` with the args/reply in `03-verb-shapes.md`.
Also `00-foundations.md` F2–F4, F6, F11, F13.

## Deliverables
1. **`/v1` router** in `panel-web`: default-deny (every route carries an explicit auth decision or a
   `public` marker; a build test fails otherwise), JSON-only, OpenAPI-validated requests.
2. **API tokens** (`npat_` prefix): create (UI, shown once), stored **hashed** (argon2id per F4),
   named, scoped, optional expiry, instantly revocable. `tokens.*` agent verbs; token metadata in the
   agent DB (D9).
3. **Scope model** (`vpspanel-api.md` §4) enforced in the agent: `system/sites/databases/dns/backups`
   read+write, `deploy`. **Excluded from ordinary tokens:** `backups:restore`, `firewall:write`,
   `ssh:write`, `tokens:write`, `system:power` — these need a user+MFA session or an explicit
   step-up **automation grant**. **No scope maps to a shell or a raw verb.**
4. **Async job surface:** every mutation returns `202 { job, links }`; `GET /v1/jobs/{id}` polls
   status/progress/events/result (maps to F6 `job.status`). `POST /v1/jobs/{id}/cancel`.
5. **Idempotency:** `Idempotency-Key` on every `POST`/`DELETE`; stored with the job; a replay returns
   the **same** job (409 on key/payload mismatch).
6. **REST resources** (`vpspanel-api.md` §5): system, jobs, sites, deployments, databases, domains/dns,
   backups, firewall, ssh — each mapping to an existing agent verb. **No `/exec`, `/run`,
   `/command`, raw-config endpoint — ever.**
7. **Inbound deploy webhook** (D1, §7): `POST /v1/webhooks/deploy/{hook_id}` — thin route forwards
   **raw body + signature** to agent `deploy.hook`; HMAC-SHA256 verified **in the agent** (secret
   root-only); **one uniform `202 "accepted"` for success AND every failure** (bad sig / unknown hook /
   stale timestamp), byte-identical, so a prober learns nothing; replay window; POST-only; routed
   **before** CSRF and the sign-in gate. (See the folded-in webhook block for the exact HMAC input.)
8. **Errors + limits** (§8): typed `{error:{code,message,details}}`, the status-code table, per-token
   token-bucket rate limiting with `Retry-After`, `503 agent_unavailable` when the agent is down.
9. **`docs/api/openapi.yaml`** (OpenAPI 3.1): the single source of truth; the server validates
   requests against it; each scope/error-code/verb documented.

## Blueprint
- **`internal/api/router.go`** (in `panel-web`) — route table with a required `auth` field per route
  (`token(scope…) | session(role, mfa?) | public`); a registration test fails the build if any route
  omits it (mirrors the no-unregistered-verb arch test, on the HTTP side).
- **`internal/api/auth.go`** — bearer-token parse (`npat_`) and the session-cookie path for the UI; it
  **passes the token through as `session`** and holds **no scope→verb map** — the agent resolves the
  token and the dispatcher enforces `Verb.Scope` (authoritative; see the folded-in block). `panel-web`
  makes no authz decision.
- **`internal/api/jobs.go`** — translate agent `{job}` replies to the `202` body; `GET /jobs/{id}`
  reads `job.status`. **Idempotency is stored in the agent** (`idempotency` table, F4/02): `panel-web`
  forwards the `Idempotency-Key` as the `idempotency_key` request field; it keeps **no** store itself
  (it holds no DB). The agent returns the same job/result on a replay, `409` on a payload mismatch.
- **`internal/api/webhooks.go`** — size-capped raw-body read; forward `{hook_id, body, signature,
  timestamp}` to `deploy.hook`; always return the uniform response regardless of outcome.
- **`internal/verbs/tokens.go`** (agent) — `tokens.create/list/revoke` (+ internal `tokens.verify`);
  hash with argon2id;
  `tokens.create` for an automation grant requires a stepped-up session.
- **`internal/verbs/deploy.go`** — extend with `deploy.hook{hook_id, body, signature, timestamp}`:
  look up the hook's sealed secret (F12), `hmac.Equal` over the raw body, check the replay window,
  then enqueue a `deploy.run` job; uniform refusal on any failure.
- **`docs/api/openapi.yaml`** — authored alongside; CI validates the server's routes against it.

> **Naming (resolved in `01-rpc-and-verbs.md`):** the canonical verbs are `database.*`, `dbuser.*`,
> `dns.record.*`, `deploy.run`. The OpenAPI must match the `01` verb table exactly.

### Folded-in API surface (so this phase stands alone — resolves C2, B22–B26, C3)
- **Token identity flow (authoritative):** `panel-web` passes the bare `npat_<id>_<secret>` as the
  RPC `session`; the **agent** resolves it (O(1) by `<id>`, Argon2id-verify `<secret>`) to
  `{scopes, automation}` and **the dispatcher enforces `Verb.Scope` agent-side**. `panel-web` does
  **not** hold a scope→verb map and makes no authz decision (removes the review's contradiction). A
  separate `tokens.verify` is unnecessary — the normal session resolution covers it.
- **Scopes:** `system:read/write`, `sites:read/write`, `databases:read/write`, `dns:read/write`,
  `backups:read/write`, `deploy`. **Excluded** (need user+MFA or an automation-grant token):
  `backups:restore`, `firewall:write`, `ssh:write`, `tokens:write`, `system:power`. The per-verb scope
  is the `Scope` column in `01-rpc-and-verbs.md`.
- **Error → HTTP status:** 200 read ok · 202 mutation accepted (+job) · 400 `bad_request`/
  `invalid_argument` · 401 `unauthenticated` · 403 `forbidden` (scope/role/MFA) · 404 `not_found` ·
  409 `conflict`/idempotency-mismatch · 412 `precondition_failed` · 423 `locked` · 429 rate-limited
  (+`Retry-After`) · 504 `deadline_exceeded` · 503 `agent_error`/agent down (`agent_unavailable`).
- **Async body:** `202 { "job": {id,status,verb}, "links": {"self","job","events"} }`; `GET /v1/jobs/{id}`
  returns `{status, progress, events[], result}` from `jobs` + `job_events` (02-data-model). A
  show-once secret is fetched via the job `claim`, not the job body.
- **Idempotency** is stored in the **agent `idempotency` table** keyed by `(token_id, Idempotency-Key)`
  with a payload hash + 24h TTL (never in the stateless web tier). Same key+payload → same job; same
  key+different payload → 409. Required on `POST`/`DELETE` (400 if missing).
- **Webhook HMAC (D1):** header `X-Hub-Signature-256: sha256=<hex>` over **`<timestamp> "." <raw body>`**
  (so the timestamp is authenticated), plus `X-Delivery-Timestamp`; replay window **5 min** + the
  `(hook_id, signature)` seen-set (`webhook_deliveries` table, 02) rejects replays; **raw body capped at
  `webhook_body_max_kb` = 512 KiB** so its base64 fits the 1 MiB RPC frame (never an over-cap close);
  the agent does a dummy
  HMAC for an unknown `hook_id` (constant-ish work, no enumeration). The uniform response —
  **`202 "accepted"` for success AND every failure, byte-identical** — is the only thing the route
  ever returns. `deploy.hook` enqueues `deploy.run` with the pushed SHA from the (validated) payload.
  Hook lifecycle: `deploy.hook_create/list/delete` (admin), secret sealed, shown once.

## External refs (gathered by the head)
- `npat_` greppable-prefix pattern (GitHub `ghp_` secret-scanning precedent).
- HMAC-SHA256 webhook verification in the agent + uniform refusal: BHPanel `web/routes/deploy_hook.php`
  (verified in this session) — ambiguous 404/202 so a prober can't tell "bad signature" from "no such
  hook".
- Token hashing = argon2id (same params as login, F4 / phase-2 refs).
- Idempotency-Key semantics (Stripe's model): same key + same payload → same result; same key +
  different payload → 409.
- OpenAPI 3.1 request validation (`kin-openapi` or similar Go validator) — head to pick in the spec.

## Verification (the gate)
```
1.  Default-deny: a new route without an auth decision FAILS the build test. Every /v1 route has one.
2.  Token: create → shown once, stored hashed (plaintext never in DB/logs); revoke → immediate 401;
      expiry honored; `npat_` prefix present.
3.  Scopes: a `sites:read` token calling POST /sites → 403; `backups:restore` via an ordinary token →
      403 (needs MFA/automation grant); no scope reaches a shell/raw verb (static check of the map).
4.  Async + idempotency: POST /sites → 202 + job; GET /jobs/{id} tracks it; replaying the same
      Idempotency-Key → the SAME job id; a changed payload with the same key → 409.
5.  Webhook: valid HMAC → deploy job enqueued; bad signature, unknown hook_id, and stale timestamp
      ALL return the identical uniform response (byte-for-byte); secret never leaves the agent; a
      replayed delivery (same timestamp/nonce) is rejected.
6.  Errors/limits: typed code bodies; 429 + Retry-After on token-bucket exhaustion; agent down → 503
      agent_unavailable (not a 500 leaking internals).
7.  OpenAPI: the server's live routes match openapi.yaml (CI diff); a request violating the schema is
      rejected with a typed 400.
```

## Definition of Done
- [ ] `/v1` default-deny router; build test rejects any auth-less route.
- [ ] `npat_` tokens: create/shown-once/hashed/scoped/expiry/revoke; agent-side verify.
- [ ] Scope enforcement incl. the excluded set needing MFA/automation grant; no shell/raw scope.
- [ ] Every mutation → 202 + job; idempotency replay returns the same job; 409 on mismatch.
- [ ] Deploy webhook: agent-side HMAC, uniform refusal, replay-protected, secret root-only.
- [ ] Typed errors + rate limits + 503-on-agent-down; OpenAPI 3.1 is the validated source of truth.

## Guardrails (this phase)
- **No `/exec`, `/run`, `/command`, or raw-config/raw-verb endpoint — ever.** The API never exposes a
  verb by name or takes a shell string.
- **No tenant/account/package/reseller/billing endpoints** (single-VPS product).
- **No secret returned twice** (tokens, DB/user passwords, private keys are show-once).
- **Restore/firewall/ssh/reboot/token-mint are not reachable by an ordinary token** — user+MFA or an
  explicit automation grant only.
- The webhook route holds **no secret** and makes **no authz decision** — it forwards to the agent,
  which owns both.
