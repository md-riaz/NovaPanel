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
  it on the handler's `context.Context`.

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
  **rotate** to a fresh session id (fixation defense). Only `session.login` and read of public assets
  are allowed under an `anon` session.
- **Login** (`session.login{user, password, totp?}`): agent verifies (Argon2id; TOTP if enrolled),
  **mints a new session id** (the anon one is deleted — this is the "rotate on login"), stores
  `kind=user`, sets `mfa_at = now` iff a TOTP/backup code was verified, returns the token. `panel-web`
  stores it in a `__Host-` cookie and presents it as `session`.
- **Roles:** `owner` > `admin` > `read_only`. Each verb declares a **minimum role** (F13);
  `read_only` may call only verbs flagged `ReadOnly`. **No tenants, packages, or resellers.**
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
- **CSRF token storage.** The synchronizer CSRF token lives in the **session row** (`csrf_token`,
  returned by `session.whoami`); it is **not** a cookie. `panel-web` injects it into pages and
  validates the `X-CSRF-Token` header against the session's value (constant-time). It rotates with the
  session id.
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
object_key = the resource a job serializes on (e.g. "site:example.com")
```
- `result_json` holds the **non-secret** result (the created resource / a link). A **show-once
  secret** (a generated DB or admin password) is **never** written to `result_json`, the row, or
  audit — it is delivered once via a sealed one-shot: the enqueue reply carries a `claim` token and
  the secret is readable exactly once through `job.claim{job_id, claim}` within a short TTL, then
  destroyed. `code` mirrors the F3 catalog on failure.

- A slow verb's handler **enqueues a row and returns** `202`-style `{ ok:true, data:{ job } }`
  immediately. The web tier polls `job.status{id}` or `job.list`.
- **One worker goroutine** drains `queued` oldest-first. Concurrency is bounded by a **keyed mutex on
  `object_key`** (`internal/jobs/keyedlock.go`): two jobs on the same object never interleave; jobs on
  different objects may run on a small worker pool (default 1; configurable, still per-object serial).
- **Crash recovery = roll-forward.** On agent start, every job left `running` is reset to `queued` and
  **re-run from the top**. This is safe because every step is idempotent (F7). **No compensation for
  local resources.** Compensation is written **only** for external/billed side effects (ACME issuance,
  provider DNS) and is itself idempotent.
  > Supersedes `vpspanel-plan.md` §7 Phase-1's older "compensation/rollback" wording — local
  > multi-resource jobs roll forward, they do not compensate.
- **Lock = liveness-checked**, never a bare lockfile. The single-worker invariant is held in-process;
  a second agent instance is prevented by an advisory lock on the DB + a pidfile checked for liveness
  (ISPConfig's stale `.ispconfig_lock` wedge is the anti-pattern).
- `reconcile{scope?}` re-renders derived config from the DB and reloads; heals drift and
  half-created state. Config files are **derived artifacts**, never parsed back into the DB.

**Executor binding (how a slow verb runs).** A slow verb's `Verb.Run` (F13) **validates args,
generates any new id, persists the row (e.g. a `sites` row in `state='creating'`), writes that id into
the job's `args_json`, and enqueues** (returning `{job}`); the **worker** calls the same verb's
`Verb.Exec(ctx, a, *Job)`. So `Exec` re-runs idempotently from `args_json` alone after a crash (the id
is already there). `Verb.ObjectKey(args) string` derives the lock key **from ids in args** — the
convention is `site:<site_id>` / `db:<db_id>` / `zone:<zone>` / `system` / `firewall` (an **id**, which
`Run` has already ensured is present; it never needs a store lookup). `step` names the last completed
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

- **Check-before-create:** `useradd` only if absent; `CREATE DATABASE IF NOT EXISTS`; write a vhost
  only if the rendered bytes differ from disk.
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
  Where it matters, the **stage** goes in `detail_json` as `detail.stage ∈ {authz, enqueue, exec,
  claim}`. So an authorization refusal is `outcome=refused, detail.stage=authz`; a worker failure is
  `outcome=error, detail.stage=exec`.
- **Required events:** a pre-authorization **refusal** event for any denied attempt (so denials are on
  the record, not just successes — aaPanel/BHPanel log only post-auth actions; we log the refusal too),
  and a **final outcome** event. A slow verb records **enqueue** (`ok`, stage=enqueue) at `Run` and the
  worker's **terminal** event (`ok`/`error`, stage=exec) at the end, correlated by `job_id` in detail.
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
- File-backed append-only log (`/var/log/novapanel/audit.log`, `chattr +a`, root-owned) **in addition
  to** the `events` table, so a DB compromise cannot rewrite history. `/var/log/novapanel` is made by
  the installer and re-made by the agent if missing; **no `LogsDirectory=`** in the unit (systemd
  re-chowns an ExecDirectory on every start and fails against `chattr +a` — BHPanel incident).
- Secrets are redacted before an event is written (F12 redaction helper).

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
For the public API, the `202` body includes the `claim` so a token caller can retrieve its secret; an
idempotent replay returns the same (still-unclaimed) `claim` or `410`-style `not_found` once claimed.

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
