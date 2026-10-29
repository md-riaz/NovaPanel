# Phase 8 — Integrations & public API

## Goal
A versioned, token-scoped **public REST API** (`/v1`) that is a thin authorized façade over agent
verbs, plus the **inbound deploy webhook** (D1). Scripts/CI can drive the panel safely; a compromised
token inherits only its scopes, and underneath only the fixed agent verbs.

## Depends on
Phases 1–6 (jobs, auth/MFA, sites, DB, deploy, backups/firewall). Reads: `vpspanel-api.md` (the full
design — this phase implements it), `00-foundations.md` F2–F4, F6, F11, F13.

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
   root-only); **uniform refusal** (`202 "not accepted"`) for bad sig / unknown hook / stale
   timestamp (no enumeration); replay window; POST-only; routed **before** CSRF and the sign-in gate.
8. **Errors + limits** (§8): typed `{error:{code,message,details}}`, the status-code table, per-token
   token-bucket rate limiting with `Retry-After`, `503 agent_unavailable` when the agent is down.
9. **`docs/api/openapi.yaml`** (OpenAPI 3.1): the single source of truth; the server validates
   requests against it; each scope/error-code/verb documented.

## Blueprint
- **`internal/api/router.go`** (in `panel-web`) — route table with a required `auth` field per route
  (`token(scope…) | session(role, mfa?) | public`); a registration test fails the build if any route
  omits it (mirrors the no-unregistered-verb arch test, on the HTTP side).
- **`internal/api/auth.go`** — bearer-token parse (`npat_`), hash-compare against the agent
  (`tokens.verify` verb returns `{token_id, scopes, automation}`); session-cookie path for the UI;
  scope→verb authorization map; the excluded-scope gate.
- **`internal/api/jobs.go`** — translate agent `{job}` replies to the `202` body; `GET /jobs/{id}`
  reads `job.status`; idempotency store keyed by `(token, Idempotency-Key)` → job id.
- **`internal/api/webhooks.go`** — size-capped raw-body read; forward `{hook_id, body, signature,
  timestamp}` to `deploy.hook`; always return the uniform response regardless of outcome.
- **`internal/verbs/tokens.go`** (agent) — `tokens.create/list/revoke/verify`; hash with argon2id;
  `tokens.create` for an automation grant requires a stepped-up session.
- **`internal/verbs/deploy.go`** — extend with `deploy.hook{hook_id, body, signature, timestamp}`:
  look up the hook's sealed secret (F12), `hmac.Equal` over the raw body, check the replay window,
  then enqueue a `deploy.run` job; uniform refusal on any failure.
- **`docs/api/openapi.yaml`** — authored alongside; CI validates the server's routes against it.

> **Naming note (head to reconcile in the registry):** `vpspanel-api.md` uses `database.*` and
> `dns.record.*`; the v1 verb set in the plan abbreviates some as `db.*`. The **registry
> (`messages.go`) is the source of truth** — pick one spelling there and make the OpenAPI match; do
> not ship two names for one verb.

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
