# 00 — Foundations (shared contracts)

Every phase builds on these. They are versioned here once; phases reference a section instead of
restating it. If a phase needs to change a contract, the head edits it **here** and bumps the note in
the affected phases. Module path is `novapanel`; all imports read `novapanel/internal/...`.

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
(`golang.org/x/sys/unix`, `unix.GetsockoptUcred`) and **closes any connection whose `uid` ≠ the
`novapanel` web user's uid**. Group membership on the socket is necessary but not sufficient.

---

## F2. RPC envelope, framing, codec

Newline-delimited JSON, one request per line, one reply per line.

```jsonc
// request
{ "id": "<uuid>", "verb": "site.create", "args": { ... },
  "actor": "<audit label, NOT authz>", "session": "<64-hex or absent>", "deadline_ms": 120000 }
// reply
{ "ok": true,  "data": { ... } }
{ "ok": false, "error": "human message", "code": "stable_string" }
```

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

Codec lives in `internal/rpc/codec.go`. It is the **only** encode/decode path; no verb hand-rolls
JSON.

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
| `unsupported` | feature is a deferred module / not enabled |

---

## F4. Session & authorization

- **Login is an agent verb.** `session.login{user, password, totp?}` → agent verifies (Argon2id;
  TOTP if enabled), mints a **64-hex opaque token**, stores `token → {user_id, role, created,
  last_seen, idle_deadline, abs_deadline}` in the agent DB, returns the token. `panel-web` stores it
  in a secure, httpOnly cookie and presents it as `session` on every call.
- **Roles:** `owner` > `admin` > `read_only`. Each registered verb declares a **minimum role**.
  `read_only` may call only verbs marked read-only. There are **no tenants, packages, or resellers.**
- **`actor`** is a free string used only for the audit label; it is **never** read for an authz
  decision. Authz is `role(session)`, resolved in the agent.
- **MFA-gated verbs** (firewall write, backup restore, ssh write, security settings, token issue,
  reboot): require that the session was minted with TOTP **and** is within a short re-auth window;
  otherwise `forbidden` with `code:"forbidden"` and a `reauth_required` hint in `data`.
- Sessions expire on idle (default 30 min) and absolute (default 12 h); revocation is a row delete.

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
jobs(id, verb, args_json, object_key, state, step, attempts, error, created, updated, finished)
state ∈ { queued, running, done, failed }
object_key = the resource a job serializes on (e.g. "site:example.com")
```

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
write temp       → <target>.tmp   // same dir, 0644 root (or per-file owner), O_NOFOLLOW parent
validate         → `nginx -t` / `php-fpm -t` on the temp set   // argv, never shell
atomic rename    → <target>
reload           → `systemctl reload nginx` (skip if rendered hash unchanged)
health-probe     → active HTTPS/socket probe of the affected target
revert           → restore prior good file + reload if validate or probe fails; return code:"agent_error"
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
outcome ∈ { ok, refused, error }
```

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
    Name     string              // "namespace.action"
    MinRole  Role
    ReadOnly bool                // callable by read_only
    Slow     bool                // → job queue (F6)
    MFA      bool                // requires TOTP re-auth (F4)
    NewArgs  func() any          // typed arg struct; decoded + validated (F9) before Run
    Run      func(ctx context.Context, a *Agent, args any) (any, error)
}
var Registry = map[string]Verb{ ... }
```

- **Architecture test:** a test enumerates every handler in `internal/verbs` (by convention/reflection
  or a generated list) and **fails the build** if any is absent from `Registry`, or if any `Registry`
  entry lacks a handler. This is invariant 3's enforcement.
- Namespacing follows the v1 verb set in `vpspanel-plan.md` (single-tenant filtered). Adding a verb =
  one `Registry` entry + one typed arg struct + one handler + its tests; nothing else reaches root.
