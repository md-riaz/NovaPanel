# 00 — Foundations (shared contracts)

Every phase builds on these. They are versioned here once; phases reference a section instead of
restating it. If a phase needs to change a contract, the head edits it **here** and bumps the note in
the affected phases. Module path is `novapanel`; all imports read `novapanel/internal/...`.

---

## F0a. Target platform (v1)

**Ubuntu 24.04 LTS first** (matches the acceptance criteria). Concrete, code-level facts the
implementer must use (do not hardcode a different distro's values):

| Thing | Value on Ubuntu 24.04 |
|---|---|
| Nginx user / group | `www-data` |
| Nginx layout | `/etc/nginx/sites-available/` + `/etc/nginx/sites-enabled/` (symlinks); main conf includes `sites-enabled/*` |
| Nginx version | **1.24.0** — does **not** accept `http2 on;` (added 1.25.1). The renderer emits `listen 443 ssl; http2 on;` only when nginx ≥1.25.1, else the legacy `listen 443 ssl http2;` form. Detect the version once at startup. |
| PHP | 8.3; FPM unit `php8.3-fpm.service`; pool dir `/etc/php/8.3/fpm/pool.d/`; binary `/usr/sbin/php-fpm8.3`; CLI `/usr/bin/php8.3` |
| MariaDB | 10.11 LTS; socket `/run/mysqld/mysqld.sock`; `unix_socket` auth plugin available for `root@localhost` |
| systemctl / journalctl | `/usr/bin/systemctl`, `/usr/bin/journalctl` |
| chattr | `/usr/bin/chattr` |

RHEL/Rocky/Alma differ (user `nginx`, `conf.d/` layout, `php-fpm` names, socket path) — a **later
port**, not v1. The agent resolves these from a small platform table, not scattered literals.

## F0b. Decisions glossary (D1–D9)

The specs cite these labels; they are defined in `vpspanel-plan.md` §4/§9 and summarized here so the
corpus stands alone.

| ID | Decision |
|---|---|
| D1 | Webhooks: HMAC-SHA256 + replay protection, **verified in the agent**; uniform refusal. |
| D2 | Updates: notify + one-click apply; ed25519-signed manifest, **verify-before-parse**, anti-rollback. |
| D3 | Per-site isolation: per-site Unix user + FPM pool over a Unix socket + `pm=ondemand` + Nginx-direct static. |
| D4 | Modules: first-party, **compiled-in only**; `module.install` never fetches/executes code. |
| D5 | Caller trust: identity = agent-minted **session token**; socket `0660` group + **SO_PEERCRED uid-pin**; `actor` = audit label only. |
| D6 | Job model: durable queue + **single in-process worker**, per-object serialized. |
| D7 | Crash safety: idempotent **ensure-state** verbs + **roll-forward** (no compensation for local resources) + one `reconcile` verb. |
| D8 | The config generator is an **attacker-controlled sink** (validate names, no empty/unquoted interpolation, atomic + validate + revert). |
| D9 | The **agent owns all persistent state** (root-only SQLite); `panel-web` is stateless (no DB). |

---

## F1. Process & socket

Two binaries, one module:
- `cmd/panel-agent` — root; the only root process; owns all state; serves the RPC socket.
- `cmd/panel-web` — unprivileged user `novapanel`; stateless; UI + public/REST API; holds **no DB**.
- `cmd/novapanelctl` — thin local client over the same socket (ops/debug/self-backup). No privileges
  of its own.

Socket: `/run/novapanel/agent.sock`, created `0660 root:novapanel`. `/run/novapanel` is created by
`systemd-tmpfiles` (`0755 root root`), **not** a unit `RuntimeDirectory=` (a unit stop would delete
it and drop every live FPM socket — BHPanel's measured 503 bug).

**Peer gate (D5):** on every accepted connection the agent reads `SO_PEERCRED`
(`golang.org/x/sys/unix`, `unix.GetsockoptUcred`) and accepts **only two peer uids**: the `novapanel`
**web uid** (normal sessions, must `session.login`) and **uid 0** (root — local `novapanelctl`/ops,
granted an **implicit `owner` session** with `mfa_at=now`, since root already owns the box). Every
other uid is closed. This is what lets `novapanelctl` run privileged local verbs while a compromised
`panel-web` (web uid) still only ever holds the sessions it logged into — the two are distinguished by
uid, not by a forgeable marker. (Group membership alone is necessary but not sufficient.)

---

## F2. RPC envelope, framing, codec

Newline-delimited JSON, one request per line, one reply per line.

```jsonc
// request
{ "id": "<ulid>", "verb": "site.create", "args": { ... },
  "actor": "<audit label, NOT authz>", "session": "<nps_<id>_<secret> | npat_<id>_<secret> | absent>",
  "deadline_ms": 120000, "idempotency_key": "<optional; from the API Idempotency-Key header>" }
// reply
{ "id": "<echoes the request id>", "ok": true,  "data": { ... } }
{ "id": "<echoes the request id>", "ok": false, "error": "human message", "code": "stable_string", "data": { ... optional ... } }
```

The reply **echoes the request `id`** (clients match replies to requests). `data` **may appear on an
error reply** too, carrying structured hints the UI branches on — e.g.
`{"ok":false,"code":"forbidden","error":"…","data":{"reauth_required":true}}` (F4). On success `code`
is omitted; on failure `error` is the human string and `code` the stable branch key.

Rules:
- **Framing:** read with a bounded scanner; **hard cap a reply at 4 MiB** and a request at 1 MiB.
  On a malformed/oversized frame the agent **closes the connection** (a half-read tail must never
  answer the next request — BHPanel's documented desync). `panel-web` caches one connection per HTTP
  request and closes on any decode error.
- **`args` is always a JSON object** (`{}` when empty, never `[]`).
- **`code`** is a stable, lowercase_snake string the UI/clients branch on (catalog F3). Never
  localize or reword `code`; `error` is the human string and may change.
- **`deadline_ms`** is advisory from the caller; the agent clamps it to a per-verb max and enforces
  it on the handler's `context.Context`. For a **slow** verb this bounds only the synchronous dispatch
  leg (validate → enqueue → return); its queued execution is bounded **per attempt** by
  `Verb.MaxDeadlineMs`, **not** the caller deadline (which is gone after enqueue) — see F6.

Codec lives in `internal/rpc/codec.go`: the server reads a line with a `bufio.Scanner` (buffer cap
**1 MiB**, the request cap) and calls `DecodeLine([]byte) (Request, error)` on it (so Decode composes
with the Scanner, not its own reader); `Encode(io.Writer, Reply)` enforces the **4 MiB reply cap** —
a handler reply over cap is **not** sent; the agent logs it and returns `agent_error` (never a silent
truncation or desync). It is the **only** encode/decode path; no verb hand-rolls JSON. **Browser
session tokens are `nps_<id>_<secret>`** (parallel to API `npat_<id>_<secret>`): the agent looks up the
row by `<id>` and compares `<secret>` (sessions: sha256; tokens: argon2id). `panel-web` passes the
whole string as `session`.

---

## F3. Error-code catalog (stable strings)

Agents return one of these in `code`. Extend only by adding to this list (never reuse a retired
string).

| `code` | Meaning |
|---|---|
| `ok` | success (reply uses `ok:true`; `code` omitted on success) |
| `unknown_verb` | verb not in the registry |
| `bad_request` | args failed decode/shape |
| `invalid_argument` | a value failed sink validation (name/path/url) |
| `unauthenticated` | no/invalid session where one is required |
| `forbidden` | session's role may not call this verb |
| `not_found` | target object does not exist |
| `conflict` | object exists / concurrent mutation on the same object |
| `precondition_failed` | required prior state missing (e.g. no cert before force-HTTPS) |
| `locked` | another job holds this object |
| `deadline_exceeded` | handler/child exceeded the clamped deadline |
| `agent_error` | internal failure (logged + audited; never leaks internals to web) |
| `interrupted` | an effectful job was left running by a crash/restart and was not blindly re-run (F6) |
| `unsupported` | feature is a deferred module / not enabled |
| `rate_limited` | a throttle or quota was hit (an **agent-side** limiter, e.g. the ACME per-domain issuance cap); retry later |

**Public-API superset.** Agent handlers return only the codes above. The HTTP/API layer (`panel-web`)
may additionally surface codes for conditions where **no agent reply exists**: `agent_unavailable`
(agent socket down → `503`) and the transport token-bucket `429` (which carries `rate_limited`). These
are **web-tier-synthesised**, never emitted by an agent handler — the one documented exception to
"agents return one of these."

---

## F4. Session & authorization

**The session is the single identity primitive.** Browser logins, API tokens, and the pre-login page
all present a `session` string; the agent resolves it to `{identity, role, scopes, mfa_at}` from its
own `sessions`/`api_tokens` tables. `actor` is a free string used **only** for the audit label, never
for an authz decision.

**Session row** (`sessions`; full DDL in `02-data-model.md`):
`{id, token_hash, kind, user_id, role, csrf_token, created, last_seen, idle_deadline, abs_deadline,
mfa_at}` where `kind ∈ {anon, user, token}`.

- **Pre-login anonymous session.** The first page visit mints an `anon` session (no user, role below
  `read_only`, carries a `csrf_token`). It exists so the login form has a CSRF token and so login can
  **rotate** to a fresh session id (fixation defense). The stateless web tier mints it through the
  **public `session.start` verb** (01 / 03), which returns `{session, csrf_token}`; it is rate-limited
  per peer and short-lived. Only `session.start`, `session.login`, and read of public assets are
  allowed under an `anon` session.
  **`session.start` is idempotent get-or-create.** When the F2 envelope presents a **live `anon`
  session**, `session.start` returns that **same** `{session, csrf_token}` unchanged — no new row, no
  rotation, and not counted against the per-peer mint rate limit — so the stateless web tier can
  **re-fetch the anon session's expected `csrf_token` on the login POST** to do its constant-time
  header compare (whoami is non-public, so the anon flow needs this symmetric read path; see CSRF-token
  storage below). When no valid `anon` session is presented (absent / expired / malformed) it **mints**
  a fresh anon row (rate-limited per peer). An **authenticated** (`kind=user`/`token`) session presented
  to `session.start` returns `code:"precondition_failed"` (F3) — that session's CSRF is read via `whoami`,
  not here. The idempotent return does **not** extend the anon session's absolute lifetime; once it
  expires the next `session.start` mints anew.
- **Anon-mint budget is a global backstop, not per-visitor.** The agent's `session.start` rate limit is
  keyed by the connecting **peer UID** (F1); the web peer is a **single UID fronting every browser**, so
  that budget is effectively **global** and must be sized as a high ceiling (config `anon_mint_rps`/
  `anon_mint_burst`) — a defense-in-depth cap against a flooded or compromised web tier, **never** a
  per-visitor throttle (keying it low starves all new logins once a handful of visitors arrive).
  **Per-client** anon-bootstrap throttling is the **web tier's** responsibility (only it sees the client
  IP), and its token bucket **must cover cookie-less bootstrap** (`GET /login` → `session.start`);
  skipping the bucket when no cookie is present is a defect. Exhaustion of the agent ceiling returns
  `rate_limited` (F3); the agent never trusts a client IP/identity the web tier forwards.
- **Expired-session GC.** Each fresh anonymous mint, never anonymous reuse, performs one indexed
  sweep deleting at most **256** session rows of any kind. Each deadline branch selects at most 256
  candidates in `(deadline, id)` order using its covering index; deduplicate their union, sort by id,
  and limit to 256 before deleting. The exact single DELETE and indexes are pinned in 02.
  Acquire the store connection and SQLite write lock before sampling the injected clock; bind that
  same `now` to all four DELETE parameters. Cleanup and the fresh insert share one short transaction;
  cleanup failure refuses the mint. Do not loop until empty or add a scheduler, config key or verb.
  GC uses strict `now > idle_deadline OR now > abs_deadline`: live and exact-boundary rows survive.
  Authentication separately rejects `now >= deadline`; retention must not weaken authentication.
  Do not log credentials or deleted session contents. Revocation remains a row delete.
  **Release dependency:** PR85's `0006_session_expiry_indexes.sql` must not merge or release before
  Phase 3 PR81's reserved `0005_sites.sql`. Do not edit shipped 0001–0004 or add a placeholder 0005;
  merge released Phase-3 main into PR85 normally and rerun gates before release.
- **Login** (`session.login{user, password, totp?}`): agent verifies (Argon2id; TOTP if enrolled),
  **mints a new session id** (the anon one is deleted — this is the "rotate on login"), stores
  `kind=user`, sets `mfa_at = now` iff a TOTP/backup code was verified, returns the token. `panel-web`
  stores it in a `__Host-` cookie and presents it as `session`.
- **Roles:** `owner` > `admin` > `read_only`. Each verb declares a **minimum role** (F13);
  `read_only` may call only verbs flagged `ReadOnly`. **No tenants, packages, or resellers.**
- **Self-service verbs (decided).** `session.reauth`, `session.logout`, `mfa.enroll`, `mfa.confirm`,
  `mfa.disable` act only on the **caller's own** session/user and are authorized for any authenticated
  `kind=user` session at or above `read_only` — an explicit, narrow exception to the "read_only may
  call only `ReadOnly` verbs" rule. They never permit acting on another user (no target user_id; they
  resolve the subject from the session), and they stay **not token-reachable** (scope —). The arch
  test exercises a `read_only` user performing each on its own account and being refused any cross-user
  effect. This resolves the dispatcher's over-broad rejection of read_only self-lifecycle calls.
- **Token sessions (API).** An API token is `npat_<id>_<secret>`; the agent looks the row up by `<id>`
  (O(1)), Argon2id-verifies `<secret>`, and treats it as a `kind=token` session carrying **scopes**
  (not a role beyond what its scopes imply). `panel-web` passes the bare `npat_…` as `session`; the
  agent resolves it. **Scope enforcement is agent-side** (a verb declares a `Scope`; the dispatcher
  checks the token's scopes), so a web-tier RCE cannot widen scope.
- **"Privilege change" = rotate the session id.** Rotation happens on: login, re-auth step-up, and
  any role change. The old id is invalidated.
- **MFA gate.** A verb flagged `MFA` (F13) requires `kind=user` **and** `now − mfa_at ≤ reauth_window`
  (default **5 min**). Otherwise the agent returns
  `{ok:false, code:"forbidden", error:"…", data:{"reauth_required":true}}`. The client then calls
  **`session.reauth{totp}`** (or a backup code), which re-verifies TOTP and sets `mfa_at = now`
  (rotating the session id). A `kind=token` session can reach an MFA-gated verb **only** if the token
  carries an **automation grant** (`api_tokens.automation = true`), which `tokens.create` issues only
  from a stepped-up (`mfa_at` fresh) `user` session; such tokens are exempt from the TOTP-recency
  check but still scope-checked.
- **CSRF token storage.** The synchronizer CSRF token lives in the **session row** (`csrf_token`); it
  is **not** a cookie. `panel-web` injects it into pages and validates the `X-CSRF-Token` header against
  the session's value (constant-time) on every mutating request. It rotates with the session id.
  Because panel-web holds no session state, it **re-fetches the expected value per request** from the
  agent: a `kind=user` (browser) session reads it via **`session.whoami`**; an **`anon`** session (the
  pre-login POST, where whoami is non-public) reads it via the **idempotent `session.start`** above. An
  API **`kind=token`** request carries no ambient cookie and bears its token directly, so it is **not**
  CSRF-protected and re-fetches no CSRF token (`whoami` is not token-reachable anyway — 01). The agent
  **never** receives or compares a CSRF header — there is no CSRF field in the F2 envelope; the web tier
  owns the compare, the agent owns the token's value and storage.
- **Timeouts:** idle **10 min**, absolute **12 h** (high-value panel), both enforced agent-side from
  `last_seen`/`created`; revocation = row delete. *(Supersedes any earlier "30 min" default.)*
- **Idempotency** for the public API lives in an agent `idempotency` table keyed by
  `sha256(token_id,key) → job_id|result` with a payload hash and a TTL — never in the stateless web
  tier (B-D5). An `Idempotency-Key` HTTP header becomes the optional `idempotency_key` request field
  (F2); the agent stores the job (async) or the result (sync).
- **Local root (`novapanelctl`) caller** is **not** a `sessions` row (root has no `users` row): the
  peer gate (F1) grants a uid-0 peer an **in-memory synthetic `Caller{Role:"owner", Actor:"root@local",
  MFAat:now, Scopes:all}`**; nothing is persisted. Internal verbs (F13) are likewise authorized by the
  **peer uid** (web uid), carried in `Caller.Peer`, not by any payload marker.
- **Job ownership:** `jobs.created_by` records the caller's session/token id. `job.status`/`job.claim`
  on a job are permitted to **that owner regardless of scope** (so a `sites:write` token can poll and
  claim the job its own `POST` created); otherwise the normal scope/role applies. This is what makes
  the show-once claim reachable by the API caller that created it.

---

## F5. Data model conventions (agent-owned SQLite)

One DB, `/var/lib/novapanel/panel.db`, `0700 root` (D9). `/var/lib/novapanel` is created by tmpfiles
`0700 root root`; **no `StateDirectory=`** in the unit (it lays a second mount over the dir and traps
nested paths — BHPanel incident).

```
PRAGMA journal_mode = WAL;  PRAGMA foreign_keys = ON;  PRAGMA busy_timeout = 5000;
```

- **Metadata only.** Never store website files, MariaDB table data, backup archives, or unbounded
  logs. Stores: users, sessions, mfa, api_tokens, sites, domains, databases, db_users, jobs, events
  (audit), backups catalog, integrations, secrets (sealed), settings, module_state.
- **Migrations:** numbered, forward-only SQL files in `internal/store/migrations/NNNN_name.sql`,
  applied in a transaction, recorded in a `schema_migrations` table. A migration never edits a prior
  migration. `store.Open()` runs pending migrations at agent start.
- **Timestamps** are UTC unix-millis integers. **IDs** are ULIDs (string) unless a natural key exists.
- Short transactions only; long work goes through the job queue (F6), not an open transaction.

---

## F6. Job & worker contract (D6/D7)

Any verb whose p95 can exceed ~2 s is marked **slow** and goes through the queue.

```
jobs(id, verb, args_json, object_key, state, step, attempts, progress, result_json, error, code,
     created, updated, finished)
job_events(id, job_id, ts, msg)            -- the streamed progress lines the API exposes
state ∈ { queued, running, done, failed }
object_key = the resource a job serializes on (see the site rule below)
```
- **Site-scoped lock key (reconciles F6↔01).** `site.create` keys on **`site:<name>`** (ensure-absent
  on the new name — the `site_id` does not exist yet); **every other** site-scoped verb keys on
  **`site:<site_id>`** resolved from persisted state **before enqueue** (a `domain.*`/`redirect.*`/
  `ssl.*` verb resolves its parent `site_id` first; `domain.remove`/`redirect.remove` look the parent up
  by the child id). The `site:<name>` entries in 01 are shorthand for this rule. Other domains key on
  their stable id likewise (`db:<name>`, `zone:<zone>`).
- `result_json` holds the **non-secret** result (the created resource / a link). A **show-once
  secret** (a generated DB or admin password) is **never** written to `result_json`, the row, or
  audit — it is delivered once via a sealed one-shot: the enqueue reply carries a `claim` token and
  the secret is readable exactly once through `job.claim{job_id, claim}` within a short TTL, then
  destroyed. `code` mirrors the F3 catalog on failure.
- **Secret job *input* (decided).** A slow verb that takes a **secret input** (e.g. `ssl.upload`'s
  `key_pem`) **seals it (F12) before the job row is written** — `args_json` holds only the sealed
  ciphertext (or a sealed-blob reference), **never plaintext**. `Exec` opens it to use it, and the
  sealed input is destroyed on the job's terminal state (and **retained** while the job is `interrupted`,
  for the retry). Crash recovery depends on the verb's class: an **effectful** secret-input verb recovers
  as `interrupted` (not blindly re-run); an **idempotent** one may roll forward (re-run) safely **provided
  an intent journal prevents a half-applied state** — `ssl.upload` takes this path (installing a
  user-supplied cert is idempotent, and a TLS intent journal guards the publish step). Either way,
  plaintext never reaches `args_json`, `result_json`, `job_events`, audit, or web state.

- A slow verb's handler **enqueues a row and returns** `202`-style `{ ok:true, data:{ job } }`
  immediately. The web tier polls `job.status{id}` or `job.list`.
- **Enqueue admission & uncertain-reply (head ruling — resolves the enqueue-audit finding, F6/F11).**
  The enqueue is **one atomic SQLite transaction** — the same shape as the scheduler's
  `EnqueueScheduledJob` (F18): it commits the `queued` **job row**, the **sealed input** (F12), the
  **`job_claims`** reservation (when the verb mints a show-once secret), **and** the **`idempotency`**
  row (`idempotency_key → job_id`, when a key is present) **all-or-nothing**. This commit **is** the
  admission boundary: the operation is admitted iff the tx commits, so a failure *before* it leaves
  **nothing queued** and the caller's failure is truthful.
  - **The job row is the sole execution authority.** The worker and `PrepareOperations` execute **only**
    a committed `queued`/`running` job row — **never** an audit line. So a mutation runs **iff** its
    admission tx committed; there is no path by which an un-admitted operation executes.
  - **Audit ordering is unchanged (F11): job-tx first, then the enqueue event (file append first, then
    the `events` row), reconciled at startup.** A committed job is admitted and **runs regardless of the
    enqueue-audit write** — the enqueue event *records* an admitted op, it is **not** a second gate that
    can un-admit a durable job. No effect is ever unaudited: the **terminal** event (F11, mandatory)
    records the effect, and a torn enqueue event is backfilled by the F11 file-only reconcile; an audit
    **sink failure** trips F11's sticky-`unhealthy` gate, stopping the *next* mutation. (This is why the
    finding's "enqueue-audit `Record` failed, yet `PrepareOperations` runs the job" is **safe**: the job
    ran because it was durably admitted, and its effect is terminal-audited.)
  - **Uncertain/lost reply is bounded and recoverable, never prevented** — losing the reply after the job
    is durably queued is unavoidable for any durable async job. **At-most-once effect:** the single
    admission tx + the `idempotency` mapping — a retry with the same `idempotency_key` returns the
    **existing** `job_id` (and re-reads the F19 claim within TTL), never a second operation; without a
    key the agent cannot dedup, so sending an `Idempotency-Key` for mutations is the web tier's job
    (F2/B-D5). **Undeliverable show-once secret:** if the reply is lost and the claim's TTL lapses (or no
    key), the minted secret is recovered by `rotate_password` (fresh F12 seal + F19 claim) — never
    silently wrong, never a second uncommitted operation.
- **One worker goroutine** drains `queued` oldest-first. Concurrency is bounded by a **keyed mutex on
  `object_key`** (`internal/jobs/keyedlock.go`): two jobs on the same object never interleave; jobs on
  different objects may run on a small worker pool (default 1; configurable, still per-object serial).
- **Oldest-first is a tie-break among the *eligible* set — never a licence to hot-spin one job (head
  ruling, 2026-11-05).** "Oldest-first" orders **admission fairness**; it does **not** mean re-select the
  same job the instant it asks to retry. A job is **eligible this pass** only if it is `queued`, its
  `object_key` lock (and every key it needs, in the F20.7 fixed order) is acquirable now, and it did **not**
  just yield on this pass. A job that returns `ErrRetry` or a transient `deadline_exceeded`/`interrupted`
  **yields to a later pass** (F6 "retried on a later pass"): the worker advances to the next eligible
  `queued` job rather than re-running the yielder, so one persistently-indeterminate create, claim
  publication, or lock-contended job **can never starve** later jobs — **including the periodic
  `reconcile`/F18 jobs, which must always stay schedulable** (the F20.9 convergence guarantee depends on
  reconcile running even while another job is wedged). A **bounded in-memory inter-pass delay** (a small
  liveness ceiling so a pass holding only yielded jobs does not busy-loop the CPU) is **permitted** and is
  a liveness clamp of the same family as the per-attempt `MaxDeadlineMs` — it is **not** the "backoff
  machinery" F6 excludes. What F6 excludes is **persisted** retry-policy state: no `attempts` column, no
  durable next-retry timestamp, no dead-letter queue, no max-attempts cap. All eligibility/yield/delay
  state is **in-memory only**: a restart (roll-forward resets `running`→`queued`) rebuilds it from scratch,
  and the **sealed input is untouched** by any yield or delay (F20.1 — a delayed retry reuses the same
  sealed args/password unchanged). This closes the oldest-first starvation window without adding durable
  backoff state or reopening the #90 freeze.
- **Crash recovery = roll-forward.** On agent start, every job left `running` is reset to `queued` and
  **re-run from the top**. This is safe because every step is idempotent (F7). **No compensation for
  local resources.** Compensation is written **only** for external/billed side effects (ACME issuance,
  provider DNS) and is itself idempotent.
  > Supersedes `vpspanel-plan.md` §7 Phase-1's older "compensation/rollback" wording — local
  > multi-resource jobs roll forward, they do not compensate.
- **Per-attempt worker deadline (head ruling, 2026-11-05).** The caller's F2 `deadline_ms` bounds only
  the **synchronous dispatch leg** (validate → enqueue → the `202`-style return) and is **consumed at
  enqueue**; a slow verb's real work runs later on the worker, where there is **no live caller** and the
  original request deadline is gone. So a queued/replayed attempt is **not** bound by the caller deadline,
  and `jobs` stores **no** deadline column (the bound is a property of the **verb**, not the row).
  Instead **each execution attempt** — first run and every replay — runs under a fresh `Exec` context
  bounded by **`Verb.MaxDeadlineMs`** (`0` → `deadline_default_ms`), recomputed per attempt. This is a
  **liveness clamp, not the caller's SLA**: it exists so a stuck `db:`/`dbuser:` lock-wait or a hung
  external call fails **`deadline_exceeded`** rather than pinning the worker; a normal attempt finishes
  well under it. Binding worker execution to the original request deadline would be **wrong** — a durable
  job must stay completable long after the RPC returned, which is the whole point of the queue.
- **Attempt `deadline_exceeded` is transient, not terminal.** It aborts the attempt and releases its
  locks; the job returns to the roll-forward set (`running`→`queued`) and is retried on a later pass — it
  is **not** marked `failed`. Idempotent ensure-state (F7) + provenance-phase branching (F20.4) make the
  retry safe (the next attempt re-reads recorded state and resumes). **This transient re-queue is for an
  idempotent attempt only.** For an **effectful** verb (external/billed side-effect: ACME issuance,
  provider DNS, an object-store/`restic` run — the same class F6 recovers as `interrupted`, not blind
  re-run), an attempt `deadline_exceeded` **mid-effect** takes that **same `interrupted` + reconcile
  path** — the verb's effect-specific idempotence check / intent journal decides whether the external
  call landed — and is **not** blindly re-queued, because a naive replay could double-issue, burn a
  rate-limit, or duplicate a provider record. The **sealed input is
  deadline-invariant**: the write-ahead sealed args/password (F12/F20.1) are reused **unchanged** on every
  replay, including one after a timeout — a timeout never re-mints, re-seals, or invalidates them, and the
  F19 claim publishes from that same sealed value when an attempt finally commits. (A genuine business
  precondition failure stays terminal `failed` with its F3 code; a resource that is persistently
  unreachable fails fast at the verb's own pre-check — e.g. MariaDB down → `precondition_failed`, not a
  deadline spin — and a job that never reaches `done` stays visible to the operator; no max-attempts or
  backoff machinery is added here.)
- **Lock = liveness-checked**, never a bare lockfile. The single-worker invariant is held in-process;
  a second agent instance is prevented by an advisory lock on the DB + a pidfile checked for liveness
  (ISPConfig's stale `.ispconfig_lock` wedge is the anti-pattern).
- `reconcile{scope?}` re-renders derived config from the DB and reloads; heals drift and
  half-created state. Config files are **derived artifacts**, never parsed back into the DB.

**Durable MFA admission.** Dispatch snapshots a successful MFA gate in the existing job authority
columns as specified in 02's worker admission clarification. User/local-root admission is distinct
from a token automation grant; the worker never requires an expired or rotated session to remain
live. This changes no schedule automation policy and adds no migration.

**Executor binding (how a slow verb runs).** A slow verb's `Verb.Run` (F13) **validates args,
generates any new id, persists the row (e.g. a `sites` row in `state='creating'`), writes that id into
the job's `args_json`, and enqueues** (returning `{job}`); the **worker** calls the same verb's
`Verb.Exec(ctx, a, *Job)`. So `Exec` re-runs idempotently from `args_json` alone after a crash (the id
is already there). `Verb.ObjectKey(args) string` derives the lock key **from ids in args** — the
convention is `site:<site_id>` / `db:<db_id>` / `zone:<zone>` / `system` / `firewall` (an **id**, which
`Run` has already ensured is present; it never needs a store lookup). A resource with an identity in a
store-global namespace distinct from its parent takes its **own** lock as well — a MariaDB user is
global (`user@host`), so user verbs lock `dbuser:<username>` **and** the parent `db:<db_id>` in a fixed
order (F20.7); a verb may hold more than one keyed lock when it spans namespaces. `step` names the last completed
step; the worker updates `progress` and appends `job_events`. A panic inside `Exec` is recovered →
`state=failed`, `code="agent_error"`, audited with the job's stored `actor`/`role` (F11). **Recovery**
reads `jobs.effectful` (a column, 02) so `internal/jobs` needs no Registry: on start, an idempotent
job left `running` → `queued` (re-run); an effectful one → `failed`,`code="interrupted"`.

**Two verb classes (crash semantics).**
- **`idempotent` (default, D7):** ensure-state; a re-run from the top makes **zero** changes
  (`service.*` config, `site.*`, `database.*` creation, `files.*`, config renders). The F7 "run twice
  = no change" test applies.
- **`effectful` (explicit `Verb.Effectful = true`):** each run has a real external effect that a naive
  re-run would duplicate or that has safety timing — `backup.create` (new snapshot), `firewall.apply`
  (armed deadman), `service.restart`. These **do not** use blind roll-forward: on worker restart an
  effectful job left `running` is marked `failed` with `code="interrupted"` (not silently re-run); the
  caller/UI decides whether to retry, and the verb's own design handles the in-flight artifact (e.g.
  `firewall.apply` leaves its armed revert to fire; `backup.create` leaves a possibly-partial snapshot
  that the next verify catches). The F7 idempotency test is replaced by an **effect-specific** test
  named in each such phase.

---

## F7. Idempotent "ensure-state" verb shape

Every mutating verb is written as *"make reality match this row,"* not *"do this action."*

- **Check-before-create:** `useradd` only if absent; write a vhost only if the rendered bytes differ
  from disk. **Exception — F20-governed two-store resources:** for a site resource whose ownership
  cannot be proven by existence (a site MariaDB database/user, a **per-site OS user** — ownership by its
  GECOS marker, see §Panel-owned OS-user provenance — a DNS record), idempotency comes from the
  **F20 provenance marker**, not from `IF NOT EXISTS` or a reusable name. There a plain `CREATE` gated by a pre-flight
  absent-check **plus** the provenance row gives the same "run-twice = no change" property, while
  `CREATE … IF NOT EXISTS` is **forbidden** (it would adopt an unmanaged collision — F20.3). This does
  **not** apply to the agent's own control schema the way it applies to managed objects: the provenance
  store is self-owned, so `CREATE … IF NOT EXISTS` is correct **when the schema is absent, or present but
  table-less with root-only grants** (a fresh box, or the agent's own interrupted two-statement bootstrap
  → **complete it** — an empty/table-less schema holds no markers, so nothing is adopted and the bootstrap
  rolls forward without an operator). But the store is a **trust root** (the executor trusts its markers
  to authorize a `DROP`), so a **pre-existing** control schema that already carries a table is **verified
  before any marker in it is trusted, never adopted on sight**: structure (a **malformed** table fails
  closed — the agent's own fixed DDL never emits one) + **no site-reachable grant** (the root/agent
  exemption is by exact `user@host`, never by username, so `root@<non-local>` is non-exempt). A
  verification failure fails closed and the operator reconciles, never auto-repair; lock/MariaDB waits are
  bounded (`deadline_exceeded`). See the Phase-4 bootstrap ruling. `DROP … IF EXISTS` stays correct for
  delete.
  The **OS user / home / docroot** are two-store too (`/etc/passwd` + disk vs SQLite), but take a
  **lighter** ownership proof: the single-VPS agent is root and owns the box, and `sites.name` is a
  validated-unique 1:1 of the Unix username, so the **SQLite `sites` row is the provenance** (no on-disk
  marker). The never-adopt rule still binds: on **first** create (a fresh `sites` row, not a replay) a
  pre-existing user/home or non-empty docroot of that name is an **unmanaged collision → `conflict`**,
  never a silent `useradd`-if-absent adopt; a **replay** (the `sites` row already `creating`, job
  re-running) owns and resumes its own. `site.delete` removes the user/home as **its own `sites`-row
  resource** (ensure-absent), never a name-only `userdel` of state it cannot show is ours.
- **Writes are temp + atomic rename** under a known, non-symlinked parent, `O_NOFOLLOW` under site
  homes. Never `chown -R` a site-influenced path.
- **The crash-safety test (mandatory per slow verb):** run the verb twice against the same desired
  state; the second run makes **zero** changes and reloads nothing (Ansible's idempotency test).

---

## F8. Config-render contract (D8 / invariant 6)

One pipeline for every system config (nginx vhost, FPM pool, …), in `internal/render`:

```
render(desired)  → bytes          // text/template; context-correct escaping; NO empty/unquoted interpolation
keep LKG         → copy the current good file to <target>.lkg (none on first create)
write temp       → <target>.tmp   // same dir, 0644 root (or per-file owner), O_NOFOLLOW parent
atomic rename    → <target>       // into sites-enabled; NOT served until a reload, so this is safe
validate         → `nginx -t` / `php-fpm -t`   // tests the full live set; argv, never shell
                 on fail: rename <target>.lkg back (or remove on first create); DO NOT reload; code:"agent_error"
reload           → `systemctl reload nginx` (skip if rendered hash unchanged; a cert-only swap forces it)
health-probe     → state-aware (HTTP-only when no cert); 5xx/connect-fail = worse; an intended 503
                   (maintenance) is NOT "worse"
revert           → on probe failure restore <target>.lkg + reload; return code:"agent_error"
```

- **Names are attacker-controlled.** Validate every `server_name`/pool name/path against F9 **before**
  it enters a template. A value that is empty where the template assumes non-empty is a **render
  error**, not an empty directive (BHServe `root $root;` outage).
- The renderer is pure (desired → bytes) and unit-tested with hostile inputs; the apply/revert side is
  integration-tested against a real nginx.

---

## F9. Validation contract (sink allowlists)

`internal/validate` — the single place names/paths/urls are checked; verbs call it, never re-implement.

- **Hostname/domain:** RFC-1123 labels, total ≤ 253, no leading/trailing dot, IDN pre-encoded to
  punycode, reject `..`, whitespace, and `_` in hostnames; a wildcard only where the verb allows it.
- **Site/unix-user name:** `^[a-z][a-z0-9-]{1,30}$`; never a reserved name; maps 1:1 to a system user.
- **Panel-owned OS-user provenance (F20 instantiation, no control table):** an OS user lives in a store
  not transactional with SQLite but has no `managed_objects`-style table, so its ownership token is the
  account's **GECOS comment**, written atomically at `useradd -c "novapanel-site=<site_ULID>"`. GECOS
  forbids `:` (passwd field sep) and `,` (GECOS subfield sep), so the exact marker is
  **`novapanel-site=<ULID>`** (ULID is Crockford base32 — no separators); parse by the `novapanel-site=`
  prefix. The name proves nothing (a slug is reusable across a delete+recreate); the marker is the
  **obj_id-analogue**. **Never `userdel`/`usermod`-adopt on a name match.**
  - **Authority to destroy/re-drive** = the `novapanel-site=` sentinel **plus** a matching site ULID. The
    **owning `site.delete` job** (row legitimately `deleting`, live job) drives teardown by that marker
    match — "no live row/job" is **not** required of it. The **"no live `sites` row and no live job"**
    guard applies only to **reconcile adopting an orphan**, so reconcile never races the owning op.
  - A reserved-scheme name whose account lacks the sentinel, or carries a **different** site's ULID than
    the op, is **not proven this op's** → fail closed (`conflict`, operator/reconcile), never adopt or
    delete a foreign account.
  - **Pre-existing unmarked (Phase-3) accounts:** never silently adopted. A **one-time, bounded startup
    migration** stamps the marker (`usermod -c`) onto an unmarked account **only** when it matches a live
    `sites` row by the frozen 1:1 name mapping — re-asserting provenance the `sites` row already proves
    (Phase 3 created it), not adopting an unknown. Thereafter the marker is authoritative; an unmarked
    account with **no** matching `sites` row stays foreign and is never touched.
  - **Retain vs purge (phase-3 `site.delete`):** `userdel` + home removal happen **only** when
    `purge_files` is set. On **retain**, the files and OS user are kept **dormant** and the account's GECOS
    is rewritten to the retained sentinel **`novapanel-retained=<ULID>`**; reconcile sweeps only
    `novapanel-site=` accounts and **never** a `novapanel-retained=` one, so retained files never land on an
    orphaned, reusable UID.
  - **Locking:** verbs that create/mutate/delete the per-site OS user hold the site key then
    `osuser:<username>` in that fixed order (F20.7). The site key is **`site:<name>`** for `site.create`
    (ensure-absent, the `site_id` not yet existing — F6 §Site-scoped lock key) and **`site:<site_id>`** for
    every existing-site op. Reconcile takes the same `site:`/`osuser:` (and `db:`/`dbuser:`) keys **per
    candidate at execution** in that order, so it never races a lifecycle job; its registry ObjectKey stays
    coarse `system` (admission) while the per-candidate keys are acquired dynamically.
  This is the generic rule for any two-store resource without a native control table (reuse F20/F6/F7;
  the in-band marker replaces the control row). It **supersedes** the older "SQLite row alone is the
  proof" shortcut for OS users (F7 check-before-create), because the slug is reusable.
- **DB / DB-user name:** `^[a-z][a-z0-9_]{1,46}$`; generated, never raw from the user.
- **Filesystem path:** must resolve (after `filepath.Clean` + symlink eval) **inside** the declared
  project root; reject absolute escapes, `..`, and symlinks that cross the boundary.
- **Git/remote URL:** scheme in `{https, ssh}`; host allowlist optional; no `file://`, no `ext::`.
- Validation failure → `code:"invalid_argument"`, never a partial side effect.

---

## F10. Exec runner contract

`internal/exec` — the only way the agent runs an external program.

- **argv only.** `Run(ctx, name, args...)` with a fixed program path and a `[]string` argv. There is
  **no** API that takes a command string. No `sh -c`, no interpolation, no `$()`.
- **Deadline + kill:** the child runs in its own process group; on `ctx` deadline/cancel the whole
  group is killed (no orphans). Stdout/stderr captured with a size cap.
- **Env is minimal and explicit;** secrets passed via env or stdin (never argv — argv is world-visible
  in `/proc`), mirroring BHPanel keeping the license key out of the process list.

---

## F11. Audit contract (invariant 7)

`internal/audit` — the agent appends one event per privileged action (success **and** refusal **and**
panic, via a deferred `recover()` on the one dispatch path).

```
events(id, ts, actor, role, verb, object_key, outcome, code, detail_json)
outcome ∈ { ok, refused, error }                       -- fixed set; never extended
Record(Event) error                                    -- fail-closed: see sinks below
```

- **Signature is `Record(Event) error`** (decided — the earlier error-less prose is superseded).
  Audit is invariant 7: a privileged **mutation** must not be reported complete if its record could
  not be durably written. Reads are not gated.
- **Outcome stays `{ok, refused, error}`** — never add new outcome values for auth/queue phases.
  Where it matters, the **stage** goes in `detail_json` as `detail.stage ∈ {authz, resolve, enqueue, exec,
  claim}`. So an authorization refusal is `outcome=refused, detail.stage=authz`; a worker failure is
  `outcome=error, detail.stage=exec`.
- **Required events:** a pre-authorization **refusal** event for any denied attempt (so denials are on
  the record, not just successes — aaPanel/BHPanel log only post-auth actions; we log the refusal too),
  and a **final outcome** event. A slow verb records **enqueue** (`ok`, stage=enqueue) at `Run` and the
  worker's **terminal** event (`ok`/`error`, stage=exec) at the end, correlated by `job_id` in detail.
  The **enqueue** event records an **already-admitted** operation (admission is the atomic job tx, F6) —
  it never gates whether a committed `queued` job runs; the **terminal** event is the mandatory
  "no effect reported complete without a durable record". So an enqueue-audit `Record` failure does not
  un-admit the job (it trips the sticky-`unhealthy` gate for the *next* mutation and is backfilled by the
  startup reconcile); the job's effect is still terminal-audited.
- **Dual-sink ordering (decided): file append **first**, then the `events` row.** The `chattr +a` file
  is the tamper-proof source of truth; if the two ever diverge, the file wins. The two are **not
  atomic** and we do not promise they are: a crash between them can leave a **file-only** event (never
  a DB-only one). On agent start, `internal/audit` **reconciles** unmatched file lines into `events`
  (roll-forward, consistent with F6). This is the ISPConfig lesson inverted — its DB-only audit could
  be rewritten by a DB compromise; ours cannot.
- **Sticky fail-closed health.** If **either** durable sink fails, the audit subsystem enters a sticky
  `unhealthy` state that **gates further mutating verbs** (they return `code="agent_error"`,
  `detail.stage=authz`) until an operator clears it; reads continue. A refusal/error response that
  arrives **after** an effect already happened does **not** undo the effect — audit records the effect
  and the failing stage; effect reversal is the verb's concern under the effectful rules (F6), not
  audit's.
- **Show-once reply exception (bounded, decided).** **Any synchronous verb that returns a newly-minted,
  irrecoverable show-once secret** in its reply is covered — currently `mfa.confirm → {backup_codes}`,
  `tokens.create → {token}`, and `deploy.hook_create → {secret}`, plus any later verb of the same shape
  (the secret is stored **sealed or hash-only**, with no second read). **Excluded** is a show-once value
  the caller can cheaply **re-obtain** — e.g. `mfa.enroll`'s `otpauth_uri` (re-enroll regenerates it) or
  a non-secret `deploy_pubkey` exposed through a read verb (`deploy.status`) — losing it costs only a
  re-request, so such a verb **fails closed** on an audit failure like any other mutation. When a covered
  verb's **effect has
  already durably committed** and only the **completion (final-outcome) audit write** then fails, the
  agent **returns the committed show-once reply** instead of overwriting it with `agent_error`, **and**
  still enters the sticky `unhealthy` state that gates all subsequent mutating verbs. Suppressing the
  reply cannot un-commit the effect (effects are never reversed here, per the bullet above); it would
  only orphan an **unrecoverable, unusable** credential — and the recovery actions (`mfa.disable`,
  `tokens.revoke`/recreate) are themselves mutations blocked by the same sticky gate, so suppression is
  strictly worse. This mirrors the bounded `job.claim` precedent: an already-consumed successful reply
  is preserved while sticky gating is retained. The exception is **narrow** — it covers **only** a
  completion-audit failure **after** an irreversible secret-bearing effect has committed. A
  **pre-authorization / attempt** audit failure, or any audit failure **before** the effect commits,
  **fails closed as normal** (no secret minted, `agent_error`, retry safe). It applies whether the
  **file append** or the **events row** failed: file-append failure loses the completion record (the
  pre-auth attempt event and the committed DB state remain; reconcile cannot recover an unwritten line),
  events-row failure keeps the tamper-proof file record (reconciled on restart) — either way the reply
  is returned and the gate engages. The show-once reply **shape is unchanged**; the degraded state is
  surfaced out-of-band (the next mutation returns `agent_error, detail.stage=authz`, plus the audit
  log), never inside the secret reply. Regression: for both verbs, inject a completion-audit failure on
  each sink → the secret reply is returned, the committed effect is present, an attempt event exists,
  the agent is sticky-`unhealthy`, and the next mutating verb is gated.
- File-backed append-only log (`/var/log/novapanel/audit.log`, `chattr +a`, root-owned) **in addition
  to** the `events` table, so a DB compromise cannot rewrite history. `/var/log/novapanel` is made by
  the installer and re-made by the agent if missing; **no `LogsDirectory=`** in the unit (systemd
  re-chowns an ExecDirectory on every start and fails against `chattr +a` — BHPanel incident).
- Secrets are redacted before an event is written (F12 redaction helper).
- **Resolved audit identity projection (ADR-0009).** New dispatch records carry
  `detail.principal`, independent of the untrusted `actor` label. Its `kind` is `user`, `anon`,
  `token`, `local-root`, or `unresolved`. Successful session resolution supplies `session_id`
  and, for authenticated users, `user_id`; PAT resolution supplies only `token_id` because the
  resolver does not provide a resolved user identity for PATs. These are agent-resolved row IDs, never the presented credential, token
  secret, hash, sealed input or private bytes. Local root uses `{kind:"local-root"}`. Missing or
  failed resolution uses `{kind:"unresolved"}` without deriving identity from actor or input IDs.
  Capture occurs after session resolution and before argument decoding, authorization refusals
  and execution; malformed arguments retain a successfully resolved principal. Unknown or
  unavailable verbs rejected before resolution explicitly remain unresolved.
  Admission and dispatch completion preserve the incoming principal, not a newly minted identity.
  Queue enqueue and terminal execution records project existing durable `jobs.created_by` and
  `role`: local-root sentinel, token ID for stored token authority, otherwise user session ID.
  The existing job snapshot has no user ID column, so queued records omit `user_id` rather than
  inventing one or consulting an expired/revoked live session. Correlate their `job_id` to the
  dispatch records for the full admission identity. This projection uses existing `detail_json`;
  no migration or DDL change is authorized or needed. Old replayed records remain unchanged.
  Typed trusted principal fields bypass only key-name redaction for `token_id`; their string
  values still undergo secret-shape redaction, and arbitrary detail retains normal redaction.
  File-first mirroring/replay, sticky failure gates and actor-label semantics are unchanged.
- **Phase-3 object attribution clarification.** Authorization precedes resource lookups, including
  lookups used for audit attribution. Before authorization, `detail.stage='authz'` identifies a
  refusal; `object_key` may identify only a validated resource ID supplied in the request
  (`site:<site_id>`, `domain:<domain_id>`, `redirect:<redirect_id>`) or a validated create name.
  Otherwise it is empty, never a fabricated parent or the incomplete key `site:`. The request key
  does not assert existence or parent resolution. After registry role/scope/MFA authorization and
  argument validation, a shared dispatcher resolution step may read persisted parent bindings.
  Child-removal admission and completion then use `site:<persisted_site_id>`, as do their queued
  events; resolution failures retain the safe request key with `detail.stage='resolve'`.
  Synchronous `ssl.auto_set` admission and completion use `site:<site_id>`. Durable admission must
  still precede every mutation; resolution is read-only and cannot bypass registry authorization.
  Existing sticky fail-closed audit behavior, actor-label semantics and authenticated roles remain
  unchanged. Test seams are the production dispatcher with file/SQLite audit mirrors and the real
  queue worker in the confined Pebble/nginx lab.

---

## F12. Secret-sealing contract (invariant 8 / D9)

`internal/secret` — provider tokens, DB admin creds, webhook secrets.

- Sealed with a **root-held key** at `/var/lib/novapanel/seal.key` (`0600 root`, generated at install,
  never leaves the agent). AEAD (XChaCha20-Poly1305 or AES-GCM); store ciphertext + nonce in the
  `secrets` table.
- `panel-web` never sees plaintext or the key; a verb that needs a secret opens it **inside the agent**
  at point of use. A web compromise leaks ciphertext only.
- A redaction helper masks known secret shapes before audit/log writes.

---

## F13. Verb registry format (invariant 3)

`internal/rpc/messages.go` is the single canonical registry.

```go
type Role uint8 // ReadOnly < Admin < Owner
type Verb struct {
    Name      string             // "namespace.action"
    Public    bool               // callable with an anon/no session (e.g. ping); overrides MinRole
    MinRole   Role               // minimum role for a user session
    ReadOnly  bool               // callable by read_only
    Scope     string             // required API-token scope (empty = not reachable by a token)
    Slow      bool               // → job queue (F6); pairs with Exec
    Effectful bool               // F6 effectful class (no blind roll-forward)
    MFA       bool               // requires fresh TOTP re-auth window (F4)
    Module    string             // owning module name ("" = core); a disabled module's verb → "unsupported"
    Internal  bool               // callable only by the web-uid peer (tokens.verify, deploy.hook); not in the public scope map
    MaxDeadlineMs int            // clamp ceiling (0 = deadline_default_ms)
    NewArgs   func() any         // typed arg struct; decoded + validated (F9) before Run
    ObjectKey func(args any) string                              // per-object lock key for a slow verb
    Run       func(ctx context.Context, a *Agent, args any) (any, error) // sync, or validate+enqueue for Slow
    Exec      func(ctx context.Context, a *Agent, j *Job) error // worker step for a Slow verb
}
var Registry = map[string]Verb{} // populated by verb packages' init() (see F14)
```
Every reachable verb is in `Registry`; the dispatcher reads the attributes before `Run`. The per-verb
table is `01-rpc-and-verbs.md` (one **`Min` role** column = `anon`|`read_only`|`admin`|`owner`;
`Public` ⇔ `Min=anon`). Resolving the review's contract gaps:
- **Role semantics:** `Min` is the floor. `ReadOnly=true` additionally lets a `read_only` user call a
  verb whose `Min` is higher (reads only). A `kind=token` session is gated by `Scope`, **not** by
  `Min`. `anon` sessions may call only `Public` verbs.
- **The caller reaches a handler via context**, not a parameter: the dispatcher puts a
  `Caller{Session, Actor, Role, Scopes, UserID, MFAat}` in `ctx`; handlers read `agent.CallerFrom(ctx)`
  (needed by `tokens.create`, `job.claim`, enqueue, audit). `Run`/`Exec` keep the `(ctx, *Agent, …)`
  shape.
- **Errors carry a code:** a handler returns `rpc.Errf(code, msg, …)` (a `*CodedError`) for any F3
  code; the dispatcher maps a `*CodedError`→that code and any other error→`agent_error` (logged +
  audited, never leaked). `validate.*` returns `invalid_argument` this way.
- **Deadline:** `Verb.MaxDeadlineMs` (0 = use `deadline_default_ms`) caps the clamp (F2).
- **One worker-step type:** the slow-verb worker step is `Verb.Exec` only; there is no separate `Step`
  type (F15 corrected).

- **Architecture test:** a test enumerates every handler in `internal/verbs` (by convention/reflection
  or a generated list) and **fails the build** if any is absent from `Registry`, or if any `Registry`
  entry lacks a handler. This is invariant 3's enforcement.
- Namespacing follows the v1 verb set in `vpspanel-plan.md` (single-tenant filtered). Adding a verb =
  one `Registry` entry + one typed arg struct + one handler + its tests; nothing else reaches root.

---

## F14. Package layout & the `Agent` type

```
cmd/panel-agent/main.go     serve (default) + root subcommands: `fw-revert <snapshot-dir>` (B-D7),
                            `update-apply` helper — run by transient units, must work with the daemon down
cmd/panel-web/main.go        HTTP server; uses internal/rpc/client; holds NO state
cmd/novapanelctl/main.go     thin local client (ops/debug/self-backup); unprivileged, over the socket
internal/
  rpc/        codec.go  server.go  client.go  messages.go(Registry, Verb, Dispatch)  session.go
  agent/      agent.go  — defines the Agent struct (the injected dependency bundle)
  store/      store.go  migrations/*.sql
  jobs/       queue.go  worker.go  keyedlock.go
  audit/  secret/  exec/  validate/  render/  dns/  config/  platform/
  verbs/      service.go site.go ssl.go db.go files.go deploy.go cron.go firewall.go backup.go
              dns.go tokens.go mfa.go session.go ...   (one file per namespace)
```

**`Agent` (in `internal/agent`)** is the dependency bundle every handler receives — the only thing a
verb needs:
```go
type Agent struct {
    Store    *store.Store
    Jobs     *jobs.Queue
    Audit    *audit.Log
    Secret   *secret.Sealer
    Exec     *exec.Runner
    Validate *validate.V
    Render   *render.R
    Cfg      config.Config
    Plat     platform.Platform
}
```
**Import layering (no cycle — resolves the review's compile blocker).** Dependencies point one way:
```
internal/jobs   → (store only)         Job is plain data here; the Queue holds an injected
                                       run func(ctx, *Job) error — it does NOT import rpc/agent.
internal/agent  → jobs, store, audit, secret, exec, validate, render, config, platform
internal/rpc    → agent, jobs          Verb.Exec is func(ctx,*agent.Agent,*jobs.Job) error; defines
                                       Verb, Registry, Dispatch, Caller, CodedError, the client/codec.
internal/verbs  → rpc, agent, jobs     each file registers in init(): rpc.Register(Verb{...}).
cmd/panel-agent → blank-imports internal/verbs so the init()s run, then wires:
                   a := agent.New(...); jobs.Queue.SetRunner(func(ctx,j){ rpc.Registry[j.Verb].Exec(ctx,a,j) })
```
So `jobs` imports nothing high-level (the worker runs the injected closure); `rpc` imports `agent`+`jobs`
(never `verbs`); the agent wires the Registry into the Queue at startup. The no-unregistered-verb arch
test checks that every exported handler in `internal/verbs` is referenced by exactly one `Register`
call. `internal/id` (ULID), `internal/platform` (F0a table), and `internal/render` are additional leaf
packages.

## F15. Internal package signatures (code-level contract)

Exact enough to implement against; each phase uses these, never a re-invented variant.
```go
// store
func Open(path string) (*Store, error)                 // WAL+FK+busy_timeout; runs migrations
func (*Store) Tx(ctx, func(*sql.Tx) error) error       // short transactions only

// jobs (F6) — Job is plain data defined here; no agent/rpc import
func (*Queue) Enqueue(ctx, verb string, args any, objectKey string) (EnqueueResult, error) // {Job, ClaimToken}
func (*Queue) Status(ctx, id string) (Job, error)
func (*Queue) Claim(ctx, id, claim string) (map[string]any, error)   // show-once secret, one-shot, TTL
func (*Queue) SetRunner(run func(ctx context.Context, j *Job) error) // injected by the agent at wiring
// the worker step itself is Verb.Exec (F13) — there is no separate Step type

// rpc dispatch plumbing
type Caller struct{ Session, Actor, Role string; Scopes []string; UserID string; MFAat int64; Peer uint32 } // Peer = SO_PEERCRED uid
func CallerFrom(ctx context.Context) Caller
type CodedError struct{ Code, Msg string }       // func Errf(code, msg string, a ...any) *CodedError

// audit (F11) — called in a defer on the one dispatch path
func (*Log) Record(ev Event) error                      // file append first, then events row (F11); err = fail-closed
type Event struct{ Actor, Role, Verb, ObjectKey, Outcome, Code string; Detail map[string]any }

// secret (F12)
func (*Sealer) Seal(plain []byte) (Sealed, error)
func (*Sealer) Open(s Sealed) ([]byte, error)          // XChaCha20-Poly1305; key /var/lib/novapanel/seal.key 0600

// exec (F10) — the ONLY external-program path; argv only, never a shell. Runner is an INTERFACE
// (tests inject a fake that records argv, per 06); exec.Real is the production impl.
type RunOpts struct { Dir string; Env []string; Stdin io.Reader; Uid, Gid uint32; Groups []uint32; MaxOutput int }
type Runner interface {
    Run(ctx context.Context, prog string, args []string, o RunOpts) (stdout, stderr []byte, exit int, err error)
}
// Clock is likewise an interface (06 time-window tests): Now() int64 ; agent holds one.
// prog is an absolute path from the platform table (F0a); Uid/Gid≠0 drops the child to a site user.

// validate (F9) — returns code:"invalid_argument" on failure
func (*V) Hostname(s string) error;  func (*V) SiteName(s string) error;  func (*V) DBName(s string) error
func (*V) UnixUser(s string) error;  func (*V) UnitName(s string) error;  func (*V) PathIn(root, p string) (abs string, err error)
func (*V) GitURL(s string) error;    func (*V) ArchiveEntry(top, name string) error
func (*V) Port(n int) error;         func (*V) CIDR(s string) error            // firewall rule fields (F9)
func (*V) RedirectTarget(s string) error  // http(s) URL or site-relative path; no CRLF/space (nginx sink)
```

## F16. Configuration

One source, read once at agent start: **a TOML file at `/etc/novapanel/agent.toml`**, each key
overridable by an env var `NOVAPANEL_<KEY>`; flags override env (for tests). Keys + defaults:
```
socket_path   = "/run/novapanel/agent.sock"
db_path       = "/var/lib/novapanel/panel.db"
seal_key_path = "/var/lib/novapanel/seal.key"
log_dir       = "/var/log/novapanel"
web_user      = "novapanel"          # agent resolves its uid/gid for the SO_PEERCRED gate (F1)
acme_webroot  = "/var/www/acme"       # world-traversable (B-D8); per-site subdir under it
sites_root    = "/srv/sites"
tls_dir       = "/etc/novapanel/tls"
acme_directory_prod    = "https://acme-v02.api.letsencrypt.org/directory"
acme_directory_staging = "https://acme-staging-v02.api.letsencrypt.org/directory"
acme_email    = ""                    # required before prod issuance; ssl.issue refuses prod if empty
acme_use_staging = false              # dev/CI set true
ssh_port      = 22                    # the never-removable firewall rule (B-D7) pins this
firewall_window_min = 15              # deadman revert timer default
update_manifest_url = "https://releases.example/novapanel/stable/manifest.json"  # D2; pubkey is compiled in
rate_limit_rps = 5   rate_limit_burst = 20     # per-token/per-session token bucket (web tier)
anon_mint_rps = 20   anon_mint_burst = 100     # agent GLOBAL ceiling on session.start mints (web peer = all browsers); backstop, not per-visitor
login_max_attempts = 5   login_window_sec = 900   totp_max_attempts = 5
webhook_body_max_kb = 512             # deploy.hook raw body cap (fits base64 in the 1 MiB frame)
reauth_window_sec = 300   idle_sec = 600   abs_sec = 43200   deadline_default_ms = 120000
```
`panel-web` has its own minimal config (listen addr, socket_path, web key for nothing secret).

## F17. RPC client

`internal/rpc/client.go` — the one socket client used by `panel-web`, `novapanelctl`, and the API:
```go
type Client struct{ /* socket path; optional cached conn */ }
func Dial(socket string) (*Client, error)
func (*Client) Call(ctx context.Context, verb string, args any, session string) (Reply, error)
```
`panel-web` caches one `*Client` connection per inbound HTTP request and closes it on any decode
error (F2). A non-`ok` reply is returned as a typed error carrying `Reply.Code` so callers branch on
the stable code. The client never interprets `actor`; `panel-web` sets `actor` to the audit label
(user email or `token:<name>`) and `session` to the presented token.

## F18. Scheduler (recurring work)

The job queue (F6) only runs **enqueued** work; recurring work is driven by an agent-internal
**scheduler goroutine** over the `schedules` table (02-data-model): each tick it finds due rows
(`next_run ≤ now`), **enqueues** the row's verb as a normal job (so it inherits per-object locking,
audit, and the effectful rules), advances `next_run` from `cron`, and persists any rolling `state_json`
(e.g. the backup verify's `n` in `--read-data-subset=n/7`). Default schedules seeded at install:
`ssl.renew` (daily; renews certs within 30 days of expiry and **forces a reload** despite an unchanged
config hash), `backup.create`/`backup.verify` (per the repo's retention), a **backup-freshness check**
(alerts if the newest snapshot per repo is older than its interval), and `update.check`. The scheduler
uses the injectable `Clock` (F15/06) so cadence is unit-testable. It does **not** fire while a prior
job for the same `object_key` is still running (the queue's keyed lock covers overlap).

**Scheduler authority (security).** A scheduled firing is **not** a privilege bypass: the enqueued job
carries the **stored owner's** `actor`/`role` and runs through the same dispatcher checks. **MFA-gated
verbs may not be scheduled** (no live TOTP) unless the schedule's owner holds an **automation grant**,
and even then only verbs that grant permits. `schedule.set` validates `verb` against an **allowlist of
schedulable verbs** (`ssl.renew`, `backup.create`, `backup.verify`, `update.check`, `reconcile`,
site-`cron`) — never `backup.restore`/`firewall.apply`/`site.delete`/etc. The scheduler goroutine and
`schedule.*` verbs are a **Phase-1 deliverable** (core infra), consumed by later phases.

**Calendar grammar (Phase-1, decided).** The agent-internal scheduler accepts a **restricted UTC
subset**, not full systemd `OnCalendar`: the aliases `minutely`/`hourly`/`daily`/`weekly`/`monthly`/
`yearly`, or `[weekday ]*-*-* HH:MM:SS UTC` where weekday ∈ `Mon Tue Wed Thu Fri Sat Sun`, each of
`HH`/`MM`/`SS` is two digits in range **or** `*`. `weekly` = Monday 00:00:00 UTC; `monthly`/`yearly` =
first day at 00:00:00 UTC. Lists, ranges, steps, concrete dates, other time zones and cron expressions
are **rejected with the `invalid_argument` code** (F3) — the parser must not silently accept and mis-fire
(BHPanel's cron strings are passed to the OS and under-validated; we validate at the sink, invariant 4).
This governs the **internal `schedules` table only**. The later **site-level `cron.*`** verbs (Phase 5)
promise real systemd `OnCalendar` timers and are a **separate surface** — they are translated to
systemd timer units, never back-fitted onto this subset. Do not claim full `OnCalendar` for Phase 1.

**Atomic firing (decided).** A tick that decides a row is due performs **one typed store transaction**,
`EnqueueScheduledJob`, that: (a) **compare-and-swaps** `next_run` guarded by the value just read (a
concurrent or duplicated tick that lost the race is a no-op — no double-fire), (b) inserts the `queued`
job row with the owner's persisted authority (below), (c) reserves the `job_claims` row if the verb
mints a secret, and (d) updates `last_run`/`state_json` — all or nothing. This closes the
cadence-advanced-but-not-enqueued crash window. The **audit record stays outside this scheduled-job
transaction**: the job transaction commits first, then the audit record is written in F11 order —
**file append first, then the `events` row** (the job tx and the audit write are two separate writes;
do not fold the `events` row into the job tx, and never let it precede its file record). **Busy object:**
if the row's `object_key` has a running job,
the tick **skips** this occurrence and still advances `next_run` (no pile-up, no retry of the skipped
occurrence — the next occurrence is the retry); this matches the ISPConfig anti-pattern we avoid, where
overlapping cron runs stack and wedge.

**Scheduler authority is persisted** (not re-derived from a live session that may be gone at fire time):
`schedules` stores the owner's `actor`, `role`, `scopes_json`, and `automation` (migration `0002`,
02-data-model), and `EnqueueScheduledJob` copies them onto the job row so the firing authorizes and
audits from stored authority — never from a session lookup that a logout/expiry could have removed.

## F19. Show-once secret claim (F6 detail)

A slow verb that mints a secret (DB/admin password) writes it **sealed** into `job_claims`
(`claim_hash`, sealed ciphertext, short `expires`) from its `Exec`, and the **enqueue reply** carries
the one-time `claim` token (`EnqueueResult.ClaimToken`, F15). The caller later calls
`job.claim{job_id, claim}`; the agent checks the claim hash, opens the secret, **deletes the row**, and
returns it exactly once. The secret is never in `jobs.result_json`, `job_events`, audit, or any log.
For the public API, the `202` body includes the `claim` so a token caller can retrieve its secret.

**Two different surfaces return two different "gone"s — do not conflate them (head ruling, 2026-11-05).**
- **`job.claim{job_id, claim}` is the secret-retrieval verb.** It returns the sealed secret exactly once;
  once the `job_claims` row is **consumed or expired** it returns `410`-style **`not_found`** — the
  *secret* is not retrievable again. Unchanged (F19 single-use).
- **The idempotent enqueue-replay is a *different* operation** (a retry carrying the same
  `idempotency_key`, F6). It returns the **existing job**, and must **not** return `not_found` merely
  because the claim row is gone — the `idempotency_key→job_id` mapping is still live and the job reached a
  real outcome. Its response shape:
  - **job still `queued`/`running`** → existing `job_id` + that status, and the **live `claim`** re-read
    if still within TTL and unconsumed.
  - **job `done`, claim live** → existing `job_id` + `done` + the same still-unclaimed `claim`.
  - **job `done`, claim consumed or expired or otherwise removed** → existing `job_id` + `done`, **no
    `claim`**, `claim_state=none` (secret **not retrievable** → recover with **`rotate_password`**).
    **Never `not_found`** — the operation succeeded; only the secret is unrecoverable, and rotate is the
    recovery (fresh F12 seal + fresh F19 claim). **Consumed vs expired is *not* durably distinguished**
    (head ruling, 2026-11-05, simplifying the earlier wording): once the `job_claims` row is deleted the
    canonical state is identical and the caller's action is identical (rotate), so **no durable non-secret
    per-claim history is authorized or needed** to tell a historical consumed claim from an expired one.
    `none` is the single determinate "secret gone" answer; distinguishing the two would be exactly the
    kind of per-feature durable state the scope freeze forbids.
  - **job terminal `failed`** → existing `job_id` + `failed` + its F3 `code`, **no `claim`**. Not
    `not_found` (a real terminal outcome, reported at-most-once — the caller sees the failure, never a
    phantom success).
  - **no such `idempotency_key` at all** → **`not_found`** (the *only* `not_found` from this surface).

  This keeps F19 **single-use / no secret re-disclosure / at-most-once** intact: a consumed claim is never
  re-handed, recovery is always a *new* `rotate_password` (never a re-read), and the replay performs **no
  second effect**. `03`'s `EnqueueResult`/`job.status` shape carries `claim` as **optional** with a
  `claim_state` reason enum of just **`live` | `none`** (retrievable, or gone → rotate); updated in the
  same change.

**Lifecycle (decided).**
- **TTL is anchored at secret publication** — the moment `Exec` commits the `job_claims` row — not at
  enqueue (the secret does not exist until `Exec` runs). **Expiry is evaluated at actual DB
  publication/read**, inside the SQL critical section, so a blocked connection cannot publish or hand
  back a claim that is already expired; a connection wait before the write re-evaluates against the
  DB's clock.
- **Clock injection stays internal.** Test the clock through the store's constructed `Clock` (F15);
  **do not export** a `PublishClaimAt(…, now)` as a public store contract — the public surface is
  `PublishClaim`/`job.claim` only.
- **Owner-bound, one-shot.** `job.claim` requires the caller to be the job's `created_by` owner (F4
  job-ownership exception) **and** within TTL; the row is deleted on first successful read or on
  expiry. Nothing about the secret survives in `jobs.result_json`, `job_events`, `events`, or any log.
- **Browser conveyance (decided).** The stateless web tier holds no store, so for a browser-initiated
  slow verb the enqueue reply's `claim` is returned to the browser in the HTMX response; the page holds
  it **in memory only** (never a cookie, never web-tier state) and calls `job.claim` once `job.status`
  reports the job done. If the page is lost before claiming, the secret is recovered only by re-running
  the verb (rotate/recreate) — never re-read. This is the single browser-side claim path for **every**
  `+ claim(…)` verb.
- **An applied secret is never lost before its claim is published (head ruling, 2026-11-05; corrected
  2026-11-06).** A rotate (`database.rotate_password`) mints a new password sealed (F12), applies it with
  `ALTER USER`, and publishes the F19 claim. The `ALTER` can land and then lose its reply; the bug to
  forbid is **terminalizing-and-erasing the sealed input on a retry error while the `ALTER` may have
  landed** — that strands the applied password (the user has the new credential, but it was never
  published as a claim and the sealed value is gone, so no one can recover it). The fix is **one
  retention invariant**, not a new recovery mode:
  1. **Rotation is NOT effectful — do not over-model it.** Canonical `01` marks `rotate_password` **Slow
     but not `Eff`**, and that is correct: re-applying the **same sealed password** via `ALTER USER` is
     **idempotent** (the identical value is a safe no-op), **unlike** the genuinely effectful class
     (`ssl.issue`/ACME, provider DNS, `restic`) where a blind replay double-issues or burns a rate limit.
     So rotation **auto-replays through ordinary F6 roll-forward** (F7 ensure-state + F20.1 same-secret
     replay) — it does **not** take the effectful `interrupted`+reconcile path, and **`01` is not
     changed**. (This supersedes the earlier "rotation is effectful" wording, which over-classified it.)
  2. **The one real invariant — retention.** The write-ahead **sealed new password (F12/F20.1) is retained
     until the F19 claim is published from it** (then the applied password is owner-recoverable) **or**
     until a terminal in which the secret **provably never landed and no target user holds it**. The
     sealed input is **never** erased on an ambiguous/transient retry while the target user may still hold
     the applied password. Order: seal → **`jobs.step` pre-`ALTER` checkpoint** → `ALTER` → **publish
     claim** → commit `done` → only then is the sealed input consumable. The `jobs.step` checkpoint is the
     canonical "last completed step," **not** new durability; on replay "`ALTER` may have landed" → re-run
     the same idempotent `ALTER` + publish the claim → the credential is **recovered, never lost**.
  3. **Which "interrupted" yields vs reconciles — `01`'s `Eff` flag decides (answers the overload).** A
     **non-effectful** verb (rotation, the MariaDB create/`ALTER` DDL) that hits a transient
     lock/deadline/`interrupted` **yields and auto-replays** (F6 roll-forward, idempotent). An
     **effectful** verb (`ssl.issue` and the TLS publication path) returns a **coded `interrupted`** that
     deliberately routes to **startup/`reconcile`**, never a blind in-pass retry — because its effect
     can't be blindly replayed. The `Eff` column is the single discriminator; a verb is never both.
  4. **Owner/provenance conflict on retry (the reported failure), and a lost admission token.** `never
     `ALTER` an uncertain/foreign owner` stands. When ownership can't be confirmed **and the `ALTER` may
     have landed**, the job does **not** terminalize-erase: it reaches a terminal that **preserves
     claim-recoverability** — publish the claim from the sealed value (the applied password is retrievable
     **exactly once by the owner who still holds the admission token**) + record
     `conflict`/`precondition_failed` for the operator. If the **admission reply (the claim token) was
     lost**, that one-shot claim cannot be re-handed (F19 single-use/owner-bound) — recovery is a **fresh
     `rotate_password`**, exactly as for any lost reply (no new mechanism, no second durable token path);
     the conflict-published claim is a **best-effort** convenience for a caller who still has the token,
     not a guarantee the token survives. If the target user is provably **gone**, there is no stranded
     credential and the sealed input may be released. Codes (`agent_error`, `conflict`,
     `precondition_failed`) F3-catalog; machinery is `jobs.step` + F12 seal + F19 claim + F20.1
     invariance — all existing, no `01`/`02`/`03` change, no reopening of the #90
     freeze.

---

## F20. External-resource provenance & two-store roll-forward (F6 detail)

Some slow verbs mutate a store that is **not transactional with the agent's SQLite** — MariaDB DDL
(Phase 4), a DNS provider (Phase 7), the filesystem. F6 requires roll-forward recovery, and invariant 4
/ the phase guardrails forbid **adopting a pre-existing unmanaged object** (no blind `IF NOT EXISTS`,
no inferring ownership from a matching name). Those two collide in one window: if the agent creates the
external object and dies **before** recording success in SQLite, a restart cannot tell *its own
half-finished resource* from *an unmanaged collision* by existence alone. A reservation held only in
SQLite proves the intended **name**, not **ownership** of a separately-created external object.

This is a **contract**, not per-feature recovery machinery (it does not reopen the #90 scope freeze):
it is the minimum needed for any two-store create to roll forward safely without adoption. The
mechanism is a **provenance marker written in the external store itself, keyed to the operation**, so
ownership is provable on the far side of the transaction boundary.

**Rules (every two-store mutating verb).**
1. **Write-ahead intent + sealed inputs in SQLite first.** Before touching the external store, commit
   the operation row with its generated **identities** (names) and any minted **secret sealed** per
   F12. Replay reuses these exact values (so a re-applied step is idempotent and an F19 claim
   re-publishes the *same* secret — never a second, unseen one). TTL/claim semantics are unchanged (F19).
   **This "operation" is the verb's existing Phase-1 job — 02 `jobs` — not a new table or state:** the
   generated identities (names **and** the durable **resource id** — `databases.id` / `db_users.id`, a
   ULID) live in `args_json`, the sealed secret in `secrets` (F12), both written by `Verb.Run` before
   enqueue (F6). "An op with no success record" = **a job not yet `done`** (the F6 `running`→`queued`
   roll-forward set) — this is the **replay trigger**. **Ownership, however, is keyed to the durable
   resource id, not the job id** (rule 2): the create job, its replay, and the later *delete* job are
   three different `jobs.id` but share the one resource id, so that id — never the name, never a job id —
   is what proves far-side ownership across the whole lifecycle. No `prepared` state or operation table
   is added to 02.
2. **Provenance-before-object, under the per-name lock.** Record a durable provenance row **in the
   external store** — `(kind, name, obj_id, phase)`, where **`obj_id` is the owning SQLite resource's
   stable id** (not the job id) — **before** creating the object, and create the object only after a
   pre-flight check shows it absent. The object is therefore never created without a matching provenance
   row that names *this resource*. The name is unique in the store (a PK on `(kind, name)`); **ownership
   is the `obj_id`**.
3. **Never adopt.** A pre-flight collision (object present, no provenance row carrying **this resource's
   `obj_id`**) **fails closed** with `conflict` — never `IF NOT EXISTS`-adopt, never auto-delete someone
   else's object. A provenance row carrying a *different* `obj_id` is likewise `conflict`.
4. **Replay decision (roll-forward), branching on the provenance `phase`.** For a job not yet `done`
   (the object may or may not exist, because the provenance row is written one DDL statement *before*
   the object — the reserve→create window):
   - provenance row for the name carrying **this resource's** `obj_id`:
     - `phase='created'` → the object is proven **ours** (it is only ever created *after* its row is
       advanced past `reserved`) → re-run the remaining idempotent steps with the sealed
       identities/secret (`GRANT`/`ALTER`), publish the claim, finish. If the object is unexpectedly
       **absent** here (dropped out-of-band) → `conflict` for **operator reconciliation** — never
       silently re-create.
     - `phase='reserved'` → creation was **not** confirmed; pre-flight the object:
       - **absent** → resume at `CREATE` (we hold the reservation and the per-name lock — a clean
         resume, not adoption), then the remaining idempotent steps, advance to `created`, publish.
       - **present** → we **cannot** prove we created it — an out-of-band create landing in the
         reserve→create window (which the per-name lock bounds but cannot forbid), **or our own `CREATE`
         that the client reported with a nonzero/indeterminate exit** so success was never confirmed and
         `created` never written; the two are **indistinguishable** from the external state → `conflict`
         for operator reconciliation. **Never adopt** (ownership unprovable) and **never `DROP`** (it may
         be a foreign object) on the strength of a `reserved` row alone. An **indeterminate external exit**
         (a nonzero client exit where the server-side outcome is unknown) is classified **`agent_error`**,
         the op is **not** `done`, and it rolls forward into exactly this branch — the ambiguity is *why*
         the window is unresolvable. Stamping the object with its `obj_id` at creation (so an
         ambiguous-exit replay can prove ownership and advance to `created`) is the **owed** optimization
         to shrink the window — out of current scope; the `conflict`-to-operator path is the contract.
     - `phase='deleting'` → complete the delete idempotently (F20.5), then remove the row. For a
       **delete verb** this finalizes `done`; for a **create** that finds its *own* `obj_id` here (an
       F20.8 abort) it finalizes **`failed`** (never re-creates, even if the collision cleared) — and the
       terminal `failed` is committed **before** the row is removed (F20.8 step 3).
   - **no** provenance row carrying this resource's `obj_id` (none for the name, or one under a
     *different* `obj_id`), object **present** in the external store → not ours (an **unmanaged
     collision**, or another resource's object) → `conflict`, surfaced for **operator reconciliation**
     (rename/remove the external object, or a future explicit adopt verb — never automatic).
   - no provenance row for this resource, object **absent** → we died before reserving → restart the op
     cleanly with the same sealed identities.
5. **Delete is provenance-last, and verifies ownership by `obj_id` — never by name alone.** The delete
   job knows the resource id (from its args) and resolves the name from the SQLite row. It DROPs **only**
   when a provenance row matches **both** `(kind, name)` **and** this resource's `obj_id`: set
   `phase=deleting`, `DROP`, then remove the row (a crash mid-delete leaves `phase=deleting` + object-
   maybe-gone → replay completes the `DROP` idempotently). A provenance row for the name carrying a
   *different* `obj_id` → **not ours**: never `DROP`; the SQLite row is a failed-create / stale artifact
   → remove it (surface the mismatch), leaving the other owner's object untouched. No provenance row and
   the SQLite row present → the object was never created under us → remove the SQLite row, no `DROP`.
   Already-deleted replay (no owned row, object absent) is a no-op. Name reuse is safe: a new resource
   reserves the freed name under *its own* `obj_id`, which a stale delete can never match.
   **Delete ordering & roll-forward (head ruling, 2026-11-06).** The full order is: mark `phase=deleting`
   → `DROP` by `obj_id` → remove the provenance row → **remove the SQLite owning row** → commit the job
   `done`. A **transient** SQL/lock failure at any step is **not** terminal — the delete **rolls forward**
   (it is idempotent ensure-absent, F7), and a canonical `jobs.step` (`delete_started`/`deleting`) keeps
   it in the roll-forward set until **both** the external teardown **and** the SQLite owning-row removal
   have completed. So a delete is **never terminalized with teardown incomplete**, and the "terminalized
   `failed` job + surviving SQLite owner" state the WIP hit does **not** arise under this order. The
   `jobs.step` checkpoint is the canonical last-completed-step, **not** new durability (same shape as the
   create-side F20.8 ordering). A **persistent** conflict — the provenance `obj_id` no longer matches, or
   ownership cannot be proven — is **not** retried forever: it surfaces `conflict`/`precondition_failed`
   to the **operator**, never a blind `DROP` of an uncertain object (frozen F20). A **historical** residue
   (a job wrongly terminalized before this ordering, leaving `deleting`-but-object-gone + a surviving
   SQLite owner row) is converged by the F20.9 reconcile: the external side is finished under the
   proven-own `deleting` marker, and the now-marker-less SQLite owning row with its object **absent** is
   the rule-5 "no provenance row + SQLite row present → remove the SQLite row, **no** `DROP`" case.
6. **No weaker substitute.** A SQLite-only reservation does **not** satisfy this (it cannot prove
   far-side ownership). If a store genuinely cannot hold a provenance marker, that is a **blocker to
   raise**, not a license to adopt by name or to fail every retry.
7. **Cross-namespace locking & admission (serialize the right key; re-validate at execution).** The
   object-key lock (F6) serializes **execution**, not admission, so the ordering safety is at execution
   time under the lock, not in the queue. (a) **Lock the object's own namespace.** A resource with an
   identity in a store-global namespace distinct from its parent takes its *own* lock, not only the
   parent's — e.g. a MariaDB user is global (`user@host`), so user verbs lock `dbuser:<username>` **and**
   the parent `db:<db_id>` (fixed acquisition order to avoid deadlock); a create that makes both a schema
   and its owner user holds both. Two creates contending the same global name then serialize, and the
   loser hits the pre-flight/PK conflict. A resource parented to another in a **non-transactional parent
   store** — a site-associated MariaDB database/user whose SQLite `site` row governs it — also holds the
   **grandparent** lock (`site:<site_id>`) in the fixed order `site:` → `db:` → `dbuser:`, so a create
   serializes against a parent `site.delete` that locks only `site:<site_id>` (the `db:`/`dbuser:` locks
   do not overlap it). A detached resource (NULL `site_id`) takes no grandparent lock. (b) **Execution
   re-validates its preconditions under the lock** (ensure-state): a child create whose parent **or
   grandparent** (its `site`) was deleted while it sat queued finds it gone and fails
   `precondition_failed` — admission need not reject the ordering, because the lock + re-validation make a
   stale queued op fail closed rather than resurrect a deleted parent. A cascade delete removes the
   parent's children by their own `obj_id` provenance (rule 5), so a not-yet-created queued child leaves
   nothing to adopt.
8. **Two-store atomicity of the owning SQLite row (no orphan in either direction).** The external object
   and the SQLite resource row are all-or-nothing **for this `obj_id`**. The SQLite row is committed **at
   success** (rule 2: the `obj_id` is minted write-ahead, but the row is written only once the external
   create + provenance have committed), so a failed create leaves **no orphan row**. The converse is
   equally binding: an external object must **never outlive a create that cannot commit its owning SQLite
   row**. So (a) **pre-flight every parent the row's FK requires** — any SQLite parent (e.g. a
   `databases.site_id`, when non-null) is validated **before the first external mutation**; a
   missing/invalid parent fails `not_found`/`precondition_failed` with **zero external effect** — and so
   are the create's own names (schema **and** owner-username absent/unowned) so a name collision fails
   `conflict` before either object is made. And (b) if a **terminal** failure still strands an external
   object the create provably made (its own `obj_id`: a TOCTOU collision landing after `CREATE DATABASE`),
   the create **rolls its own `obj_id`-owned objects back to absent by the rule-5 delete path**. **The
   durable abort decision is the `phase='deleting'` marker under the create's own `obj_id`** — the
   existing provenance phase in the external store, never a separate SQLite "cleanup intent" flag (that
   would be prohibited per-feature durability). The ordering is what makes it survive both stores:
   1. advance the owned provenance row(s) → **`phase='deleting'`** (durable "this create is aborting");
   2. `DROP` the object(s) by `obj_id` (idempotent);
   3. **commit the job terminal `failed` with its `code` (SQLite `jobs.state`/`jobs.code` — canonical
      Phase-1 columns, not a new flag) *before* removing the last provenance row**;
   4. remove the provenance row(s).
   The linearization point is step 3: a create that reaches it is terminally `failed` and **never rolls
   forward**. So the one dangerous state — **no marker + no object** — is reachable **only after** step 3,
   where the job is already terminal and cannot re-create. (The #101 ordering, which removed the row
   *before* finalizing, left a window where a crash gave non-terminal job + no marker + no object → the
   re-create trap this closes.) **Replay before step 3** finds the create's **own `obj_id` in
   `phase='deleting'`** → this is an **abort, not a create**: complete the `DROP` (idempotent), then
   finalize the job `failed` (code from `jobs.code`, else re-derived by re-running the deterministic
   pre-flight), then remove the row — it **never** re-creates, **even if the obstacle has cleared**,
   because its own `deleting` marker is the durable proof it decided to abort. **After step 3**, a
   leftover `deleting`-row-with-object-absent is a completed-delete **tombstone**: harmless (names are
   generated-unique, so no new op collides), reclaimed by a startup/periodic reconcile or by the next op
   on that exact name (object absent under a `deleting` row = the delete finished → the row is removable,
   never a `DROP`). For **multiple owned objects** the same holds: mark all `deleting` → `DROP` all →
   **one** terminal `failed` commit → remove all rows; a crash between individual row removals leaves only
   tombstones. This removes **only** objects whose ownership is **proven** by this resource's `obj_id` —
   it is roll-forward of the create's *own* effect, and is distinct from (never a license to weaken) rules
   3–4's "never auto-delete an object of uncertain ownership."
9. **Startup/periodic provenance reconcile (the catch-all backstop).** Job roll-forward (F6) re-drives a
   non-`done` job, but it cannot clean up after a job that reached a **terminal** state (`done`/`failed`)
   or whose row is gone — e.g. the rule-8 tombstone, or an object owned by a create whose SQLite resource
   row never committed. A **reconcile pass** over the provenance store closes that remainder. It runs on
   agent start (**after** F6 job roll-forward) and periodically (F18), is itself idempotent and
   crash-safe, and is **generic over every F20 resource** (the F20 counterpart of F6's "reset `running`→
   `queued` at boot" — **not** per-feature machinery, and it uses only canonical state: `managed_objects`,
   `jobs`, and the owning SQLite resource rows). It acts **only on markers whose `obj_id` it can match to
   our own SQLite** (a marker whose `obj_id` is unknown/foreign is **never** touched → operator). For each
   of our markers it converges by the rule-4 table, keyed on (`phase`, object present?, owning resource
   row present?, owning job terminal?):
   - `phase='deleting'`, object **absent** → completed-delete **tombstone** → remove the row.
   - `phase='deleting'`, object **present** → an authorized teardown of *our own* `obj_id` that didn't
     finish → complete the `DROP`, remove the row.
   - `phase='created'`, **no** owning resource row, owning job **terminal (`failed`) or gone** → our own
     object (ownership **proven** by `created` — rule 4: `created` is written only *after* our own create
     confirmed) whose SQLite owner can never commit → drive the rule-8 abort (`deleting` → `DROP` by
     `obj_id` → remove). This is safe to `DROP` **because `created` proves ownership**. (It stays within
     the `obj_id`-provable guard above: if neither the job nor any resource row carries this `obj_id`, the
     marker is unmatchable → treated as foreign → operator, never dropped.)
   - `phase='reserved'`, object **absent**, no owning row, owning job **terminal or gone** → a create that
     died before a confirmed `CREATE` and can never commit its owner → there is **no object to drop** →
     remove the stranded `reserved` marker (frees the name; the marker is `obj_id`-proven ours and nothing
     external is touched).
   - `phase='reserved'`, object **present**, no owning row, owning job **terminal or gone** → **`conflict`
     → operator; never `DROP`, never adopt.** This is exactly rule 4's unprovable `reserved`+present: our
     own `CREATE` with an indeterminate exit (`created` never written) *or* a foreign object that landed in
     the reserve→create window — **indistinguishable** from external state. A terminal/gone job only
     removes the *replay* that would have owned it; it does **not** make the present object's ownership
     provable, so reconcile must fail closed to the operator, identically to rule 4 — it may **not** `DROP`
     on the strength of a `reserved` row. (Rule 4's owed `obj_id`-stamp optimization would let reconcile
     read the stamp, prove ownership and resolve this automatically; out of scope — the operator path is
     the contract.)
   - `phase='created'`, owning resource row **present** → live and healthy → leave.
   - `phase='reserved'`, object **present**, no owning row, a job still **live** → leave it to that job's
     replay (rules 3–4); reconcile never races a live job on the same `obj_id`.
   - **SQLite owning row present, its provenance marker gone, external object absent** (the historical
     failed-delete residue: a delete that tore down the external object + marker but was terminalized
     before removing its own SQLite row) → **remove the orphaned SQLite owning row** (rule 5 "no provenance
     row + SQLite row present → remove the row, **no** `DROP`"). This is the one case reconcile reaches by
     the **SQLite side**, not a marker; it touches nothing external (the object is already gone), so it
     cannot harm a foreign object.
   Because reconcile acts only on `obj_id`-proven-own markers and only ever `DROP`s an object under a
   `deleting` marker it owns (never a `reserved`-present or foreign one), it preserves rules 3–4 (never
   adopt, never delete an object of uncertain ownership) while guaranteeing **convergence**: whatever
   inter-store crash state a stop leaves, the next boot (plus the periodic pass) drives it to **a correct
   terminal state**. For every *provable* state that means **no orphan and no stranded marker** — the
   object and its marker are either both present (healthy), or both removed (tombstone/abort). The **one
   deliberate exception** is the unprovable `reserved`+present (an indeterminate-exit `CREATE`
   indistinguishable from a foreign object): there the correct terminal state is the **fail-closed
   operator-conflict that preserves *both* the object and its `reserved` marker** — the retained marker is
   **not** a "stranded marker" bug but the durable record of an unresolved-ownership conflict awaiting the
   operator, and the retained object is **not** an "orphan" to reclaim but a possibly-foreign object we are
   forbidden to `DROP`. Convergence therefore means "reaches its correct terminal state," which for that one
   case is preservation-for-operator, not removal (never-delete-uncertain, rules 3–4, wins over automatic
   cleanup).

**Completeness & freeze.** Rules 1–9 are the *closed* contract for a two-store create/rotate/delete: the
provenance `phase` is the external authority, the SQLite terminal state is ordered against it (rule 8),
and the reconcile (rule 9) converges every residue. Every inter-store crash point is therefore covered by
roll-forward (F6) **or** reconcile (rule 9); there is no window left that needs a *new* per-feature rule.
Implementations prove this by the **F7 crash-safety replay test across the enumerated inter-store points**
(before/after each of: reserve, CREATE, phase→created, SQLite commit; and for teardown: →deleting, DROP,
terminal commit, row removal), plus one reconcile pass from each stopped state. A genuinely new *contract*
gap is still raised; a sub-statement timing window that rules 1–9 + reconcile already converge is **not** a
fresh ruling — it is covered here, with power-loss/VM acceptance of the residue deferred to #90.

Phases instantiate this: Phase 4 with a MariaDB control schema (see `phases/phase-4.md`), Phase 7 with
the provider's own record keyed by the resource `obj_id`. Power-loss/VM acceptance of the window stays deferred (#90);
the contract itself is verified by the crash-safety replay test (F7) against a simulated mid-op stop.
