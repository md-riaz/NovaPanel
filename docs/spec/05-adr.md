# 05 — Architecture Decision Records

Each record states a decision that is **already made and binding** on the implementation. The plan
(`../vpspanel-plan.md`) and `00-foundations.md` carry the operative contracts; this file records *why*
each decision was taken, what it costs, and what was rejected — so a later contributor can tell a
deliberate constraint from an accident, and knows what would have to change to revisit one.

- **Status** values: `Accepted` (binding now), `Superseded by ADR-N`, `Proposed` (not yet binding).
- An ADR is not re-opened by an implementing agent. If one looks wrong or is blocking, **raise it to
  the architecture head** (see `README.md` role split); the head supersedes it with a new ADR.
- The short labels **D1–D9** used across the corpus map onto ADR-0004, 0006, 0007, 0008, 0009 (audit),
  0010 (secrets), 0011 (updates), 0012, 0013, 0014 as noted in each record.

| ADR | Decision | Status | Maps to |
|---|---|---|---|
| 0001 | Greenfield Go rewrite; freeze the PHP app at `php-legacy` | Accepted | — |
| 0002 | Two-process privilege split over a Unix socket | Accepted | spine |
| 0003 | Typed-verb RPC only — no shell, no run-command, ever | Accepted | inv. 2,3 |
| 0004 | Caller trust = session token + `SO_PEERCRED` uid-pin; `actor` = audit label | Accepted | D5 |
| 0005 | Agent owns all persistent state; `panel-web` is stateless | Accepted | D9 |
| 0006 | Durable job queue + single in-process worker, per-object serialized | Accepted | D6 |
| 0007 | Idempotent ensure-state + roll-forward + one `reconcile` | Accepted | D7 |
| 0008 | Config generator is an attacker-controlled sink (validate/atomic/test/revert) | Accepted | D8, inv. 6 |
| 0009 | Append-only audit, written by the agent | Accepted | inv. 7 |
| 0010 | Secrets sealed by the agent with a root-held key | Accepted | inv. 8 |
| 0011 | Signed, anti-rollback updates; verify-before-parse | Accepted | D2, inv. 9 |
| 0012 | Per-site isolation: Unix user + FPM pool over a socket | Accepted | D3 |
| 0013 | Modules compiled-in only; `module.install` never fetches code | Accepted | D4 |
| 0014 | Webhooks: HMAC-SHA256 + replay protection, verified in the agent | Accepted | D1 |
| 0015 | SSL: three cert sources (none / Let's Encrypt auto / manual); force-HTTPS gated on a valid cert | Accepted | Phase 3 |
| 0016 | Tech stack and target platform (Ubuntu 24.04 first) | Accepted | F0a |

---

## ADR-0001 — Greenfield Go rewrite; freeze the PHP app at `php-legacy`
**Status:** Accepted · 2026-10-30

**Context.** NovaPanel exists today as a PHP app in this repo. It must become a single-VPS control
panel whose central promise is a hard privilege boundary (see ADR-0002). Retrofitting that boundary
onto the PHP process model (the web tier is the privileged actor) would be a rewrite in all but name,
and would carry the old action surface forward unaudited.

**Decision.** Build v1 fresh in Go. Preserve the complete PHP history at branch/tag `php-legacy`; the
Go rewrite owns a clean repo root. No PHP code is deleted from history — it is frozen out of the way.

**Consequences.** (+) A clean action registry and privilege model from commit one; a single static
binary per process; no PHP-FPM in the control plane. (−) No incremental migration of the old UI;
feature parity must be rebuilt phase by phase. The freeze is a one-way door for v1.

**Alternatives rejected.** Incrementally hardening the PHP app (the boundary can't be bolted on
credibly); a Rust rewrite (Go's stdlib + ops ergonomics fit a systemd-native agent better, and the
reference panels we audited are Go/PHP).

---

## ADR-0002 — Two-process privilege split over a Unix socket
**Status:** Accepted · 2026-10-30 · *the architecture spine*

**Context.** A control panel must perform root actions (write `/etc/nginx`, manage systemd units,
create users). A single privileged web process means any web-tier RCE is instant root. Invariant 10:
*assume the web tier will eventually have an RCE and make it non-catastrophic.*

**Decision.** Two binaries, one module. `panel-agent` runs as root, is the only root process, owns all
state, and serves a typed RPC socket. `panel-web` runs as the unprivileged `novapanel` user, holds the
UI and public/REST API, and can do nothing privileged except by asking the agent over the socket
(`/run/novapanel/agent.sock`, `0660 root:novapanel`). `novapanelctl` is a thin local client on the
same socket.

**Consequences.** (+) A web-tier compromise is bounded by the verb surface and the agent's authz, not
by Unix permissions. (+) The security story is one boundary, testable in CI (ADR-0003). (−) Every
privileged capability costs a round-trip and a typed verb; nothing is "just a shell call." (−) Two
processes to package, supervise, and version together.

**Alternatives rejected.** One privileged process (defeats the whole point); polkit/helper-binary
sprawl (harder to audit than one typed surface); a container per action (too heavy for a single VPS).

---

## ADR-0003 — Typed-verb RPC only; no shell, no run-command, ever
**Status:** Accepted · 2026-10-30 · *invariants 2 & 3*

**Context.** The agent boundary (ADR-0002) is only as strong as its narrowest reachable action. A
single generic "run this command" verb, or any handler the registry doesn't know about, collapses the
boundary back to RCE-is-root.

**Decision.** The agent exposes a fixed registry of typed verbs (`internal/rpc/messages.go`). It
executes **argv arrays, never `sh -c`**; no command string is ever built from input. There is no
`shell.execute` / run-command / write-arbitrary-root-path verb — not now, not ever. Every reachable
handler must be a registered verb. Both rules are machine-enforced: an AST test fails the build if
anything outside `internal/exec` spawns a process or references a shell, and a registry test fails if
any handler is unregistered.

**Consequences.** (+) The entire privileged surface is enumerable and reviewable (`01-rpc-and-verbs.md`).
(+) Shell-injection as a class is designed out. (−) Every new capability is a new typed verb with
args, validation, and a spec row — deliberately high-friction.

**Alternatives rejected.** A guarded command allowlist (string-building is still a sink); a templated
shell (same); trusting the web tier to only send safe commands (violates invariant 10).

---

## ADR-0004 — Caller trust = session token + `SO_PEERCRED` uid-pin; `actor` is an audit label (D5)
**Status:** Accepted · 2026-10-30

**Context.** The agent must know *who* is calling and *whether they may*. The socket is group-readable
so the web uid can connect; a payload-supplied identity would be forgeable by a compromised web tier.

**Decision.** Identity is an **agent-minted session token**, resolved and authorized inside the agent.
On every accepted connection the agent reads `SO_PEERCRED` and accepts only the web uid or uid 0 (root
→ a synthetic owner caller for local ctl); every other uid is rejected before a line is read. The
`actor` field in a request is an **audit label only** — never an authorization input.

**Consequences.** (+) A web-tier RCE cannot escalate role by lying about `actor`; authz lives behind
the boundary. (+) Local root tooling works without a password dance. (−) Session/token machinery lives
in the agent (Phase 2), not the web tier.

**Alternatives rejected.** Trusting an `actor`/role field in the payload (forgeable); mTLS over the
local socket (peer-cred is simpler and sufficient for a local boundary); OS-group-only trust (can't
express per-user role or MFA).

---

## ADR-0005 — Agent owns all persistent state; `panel-web` is stateless (D9)
**Status:** Accepted · 2026-10-30

**Context.** If the web tier holds the database, a web-tier compromise reads and rewrites all panel
state (users, secrets, audit) directly, bypassing the agent boundary.

**Decision.** One SQLite database, `/var/lib/novapanel/panel.db`, `0700 root`, owned and accessed only
by the agent (WAL mode). `panel-web` holds **no** database; it obtains everything it renders through
read verbs over the socket.

**Consequences.** (+) The boundary also covers data at rest; audit and secrets can't be tampered with
from the web tier. (+) One writer removes a class of concurrency bugs. (−) Every read the UI needs is
a verb; no direct queries from the web tier. (−) SQLite single-writer ties into the single-worker job
model (ADR-0006).

**Alternatives rejected.** Shared DB readable by both uids (defeats the boundary for data); a client
DB in the web tier with sync (needless complexity on one VPS); Postgres (operational weight not
justified for single-node).

---

## ADR-0006 — Durable job queue + single in-process worker, per-object serialized (D6)
**Status:** Accepted · 2026-10-30

**Context.** Privileged mutations (issue a cert, restart a service, run a backup) are slow and must
survive a crash, not race each other on the same object (two edits to one site's nginx config).

**Decision.** Slow verbs enqueue a durable job (in the agent's DB) and return a job id immediately;
reads stay synchronous. A **single in-process worker goroutine** drains the queue, serialized per
object key (`site:<name>`, `firewall`, `system`, …). The result lands in `jobs.result_json`, read back
via `job.status`.

**Consequences.** (+) Crash-safe, inspectable, and free of same-object races by construction. (+) Fits
the single-writer DB (ADR-0005). (−) No parallel execution across unrelated objects in v1 (acceptable
on one VPS); a long job delays others on its key. (−) The UI must poll job status.

**Alternatives rejected.** Synchronous execution in the request (loses durability and blocks the
socket); a worker pool (reintroduces the races the per-object lock removes); an external queue
(Redis/systemd timers) — extra moving parts for a single node.

---

## ADR-0007 — Idempotent ensure-state + roll-forward + one `reconcile` (D7)
**Status:** Accepted · 2026-10-30

**Context.** A job can die mid-way (power loss during a 4-step site creation). Compensating-transaction
rollback for local system resources is itself error-prone and can leave worse states.

**Decision.** Verbs are **idempotent ensure-state**: a re-run from the top makes zero changes when the
world already matches, and completes the work when it doesn't (**roll-forward**). Genuinely effectful,
non-idempotent actions (`backup.create`, `firewall.apply`, `service.restart`) are marked `Effectful`
and are *not* blindly re-run. One `reconcile` verb re-asserts declared state across the system.

**Consequences.** (+) Crash recovery = re-run; no bespoke undo paths for local resources. (+)
`reconcile` doubles as drift repair. (−) Every mutating verb must be written to be safely
re-runnable, which constrains how it's implemented. (−) Effectful verbs need explicit
confirm/dedup handling rather than free retries.

**Alternatives rejected.** Saga/compensation for local resources (fragile, as above); best-effort with
manual cleanup (leaves operators stranded).

---

## ADR-0008 — The config generator is an attacker-controlled sink (D8, invariant 6)
**Status:** Accepted · 2026-10-30

**Context.** Site names, domains, and paths are attacker-influenced and flow into nginx/FPM/systemd
config. An empty or unquoted interpolation, or a bad render pushed live, can break every site at once.

**Decision.** Treat every renderer as a sink. Validate names/paths against an allowlist at the agent
before they touch a template (F9/F15). Never render an empty or unquoted value. Write to a temp file,
validate (`nginx -t` / unit verify / equivalent), atomically rename into place, reload, health-probe,
and **revert on failure**.

**Consequences.** (+) A bad config can't take the server down; the live config is only ever replaced by
a validated one. (+) Injection via names is designed out at the sink. (−) Every render path carries the
full validate→temp→test→reload→probe→revert machinery; more code per renderer.

**Alternatives rejected.** Render-in-place then reload (a failed reload leaves a broken live config);
trusting upstream validation only (defense must be at the sink).

---

## ADR-0009 — Append-only audit, written by the agent (invariant 7)
**Status:** Accepted · 2026-10-30

**Context.** The audit trail of privileged actions is worthless if a compromised web tier can forge or
erase it.

**Decision.** The **agent** writes an append-only audit record for every privileged action, to a
root-owned log made append-only (`chattr +a`). The web tier cannot write or alter it. Each record
carries the resolved identity, role, verb, object key, outcome, and code. `actor` (ADR-0004) is the
audit label, not an authz input.

**Consequences.** (+) The record is trustworthy even after a web-tier compromise. (−) Audit is on the
agent's write path; it must be durable and cheap. (−) Rotation must respect the append-only attribute.

**Alternatives rejected.** Web-tier logging (forgeable); syslog only (not tamper-evident without extra
work); no structured object key (loses per-object audit queries).

---

## ADR-0010 — Secrets sealed by the agent with a root-held key (invariant 8)
**Status:** Accepted · 2026-10-30

**Context.** The panel holds provider tokens, DB passwords, repo credentials. If the web tier can read
them in plaintext, a web RCE exfiltrates everything.

**Decision.** Secrets are sealed by the agent with a root-held key and stored as ciphertext; the agent
unseals only when it needs the value to act. A `panel-web` compromise leaks ciphertext, not secrets.
Show-once values (new passwords, enrollment URIs) are delivered through the one-shot job `claim`
mechanism (F19), never persisted in a job result.

**Consequences.** (+) Secret confidentiality survives a web-tier compromise. (+) One sealing authority.
(−) Key management is the agent's responsibility (master-key provisioning/rotation must be specified
before it's relied on). (−) No secret is ever visible to the web tier for convenience.

**Alternatives rejected.** Plaintext at rest readable by the web uid (defeats the boundary); an
external KMS/HSM (operational weight not justified for single-node v1).

---

## ADR-0011 — Signed, anti-rollback updates; verify-before-parse (D2, invariant 9)
**Status:** Accepted · 2026-10-30

**Context.** A self-updating root daemon is a supply-chain target. Parsing an attacker-supplied
manifest before verifying it, or accepting an older signed build, are classic footguns.

**Decision.** Updates are notify + one-click apply. The release manifest is **ed25519-signed**; the
agent **verifies the signature before parsing** the manifest, refuses any version older than the one
installed (anti-rollback), and unpacks defensively (tar-slip guards). The pubkey is compiled in.

**Consequences.** (+) A forged or downgraded update is rejected before it can do anything. (−) Release
tooling must sign; the signing key is now critical infrastructure. (−) Emergency manual recovery needs
a documented path that doesn't bypass the checks silently.

**Alternatives rejected.** TLS-only trust in the release host (a host compromise ships anything);
parse-then-verify (parsing is attack surface); allowing downgrades (rollback to a vulnerable build).

---

## ADR-0012 — Per-site isolation: Unix user + FPM pool over a socket (D3)
**Status:** Accepted · 2026-10-30

**Context.** On a shared single VPS, one site's PHP must not read another site's files or run as a
shared web user.

**Decision.** Each site gets its own Unix user and its own PHP-FPM pool, talking to nginx over a
per-site Unix socket, with `pm=ondemand`. Static files are served nginx-direct. Document roots are
rooted per site; file verbs are rooted at the docroot, not the home.

**Consequences.** (+) Filesystem and process isolation between tenants. (+) `ondemand` keeps idle
memory low on a small box. (−) More users/pools/sockets to manage and reconcile. (−) The tmpfiles vs
`RuntimeDirectory=` subtlety matters (a unit stop must not delete live FPM sockets — see F1).

**Alternatives rejected.** One shared web user (no isolation); containers per site (too heavy for the
single-VPS target); TCP FPM (needless exposure vs a Unix socket).

---

## ADR-0013 — Modules compiled-in only; `module.install` never fetches or executes code (D4)
**Status:** Accepted · 2026-10-30

**Context.** A plugin system that downloads and runs third-party code on a root-adjacent daemon is a
standing RCE and supply-chain risk.

**Decision.** Modules are **first-party and compiled into the binary**. `module.install`/`enable`
toggle a feature that already ships in the build; they never fetch, unpack, or execute remote code.
Optional capabilities (OLS perf mode, mail, PowerDNS, FTP, Docker — Phase 9) land only after the core
is boring.

**Consequences.** (+) No runtime code-loading attack surface; the module set is auditable at build.
(−) Adding a module means a release, not a download; no third-party module ecosystem in v1.

**Alternatives rejected.** Downloadable plugins (the exact risk this avoids); scripting hooks
(string-building sink, violates ADR-0003).

---

## ADR-0014 — Webhooks: HMAC-SHA256 + replay protection, verified in the agent (D1)
**Status:** Accepted · 2026-10-30

**Context.** Deploy webhooks are an unauthenticated public entry point that triggers privileged
actions (a deploy). Signature checks done in the web tier are inside the untrusted zone.

**Decision.** Inbound webhooks carry an HMAC-SHA256 signature over the body plus a timestamp; the
**agent** verifies the signature and enforces replay protection (timestamp window + delivery dedup).
Failures return a **uniform refusal** (no oracle about why). The web tier only relays.

**Consequences.** (+) Webhook trust lives behind the boundary; a web compromise can't mint valid
deliveries. (+) Uniform refusal avoids a verification oracle. (−) The agent must track recent delivery
ids for replay defense.

**Alternatives rejected.** Verifying in the web tier (inside the untrusted zone); IP allowlists only
(spoofable, brittle with provider IP churn); distinct error codes per failure (information leak).

---

## ADR-0015 — SSL: three cert sources; force-HTTPS gated on a valid cert
**Status:** Accepted · 2026-10-30 · *Phase 3*

**Context.** Operators need automatic certs for most sites, but also must install a purchased/EV cert
by hand, and must be able to force HTTPS — without locking themselves out when no cert is present.

**Decision.** A site's `cert_source` is one of `none` | `letsencrypt` | `manual`. Let's Encrypt certs
auto-renew (toggle via `ssl.auto_set`); **manually uploaded certs are never auto-renewed**. `ssl.upload`
validates the key↔cert match and validity dates before installing. A separate **force-HTTPS redirect**
toggle is available but **gated on a valid cert** (it cannot be enabled when there is no usable cert).
Domains and SSL are distinct UI sections for this reason.

**Consequences.** (+) Covers auto, manual, and mixed estates without special cases. (+) Force-HTTPS
can't brick a site that has no cert. (−) Manual-cert expiry is the operator's responsibility; the panel
surfaces days-left but won't renew.

**Alternatives rejected.** Let's-Encrypt-only (blocks EV/purchased certs); auto-renewing uploaded certs
(impossible — no ACME authority for them); an unconditional force-HTTPS switch (foot-gun).

---

## ADR-0016 — Tech stack and target platform
**Status:** Accepted · 2026-10-30 · *see `00-foundations.md` F0a*

**Context.** A single-VPS panel should be operationally boring: few moving parts, native to the OS,
easy to package as static binaries.

**Decision.** Go (`module novapanel`); `net/http` + chi routing; `html/template` server-rendered with
HTMX + Alpine for interactivity and Tailwind compiled to static CSS (no CDN at runtime); SQLite in WAL
mode as the agent's store. Target **Ubuntu 24.04 LTS first** (Nginx 1.24, PHP 8.3, MariaDB 10.11),
with OS-specific facts resolved from a platform table, not scattered literals. ACME via `lego/v4`;
backups via Restic; firewall via UFW; external DNS via Cloudflare first (behind a `dns.Provider`
interface). RHEL/Rocky/Alma are a later port, not v1.

**Consequences.** (+) Two static binaries, systemd-native, minimal runtime deps. (+) Server-rendered
UI keeps the web tier simple and the attack surface small. (−) Distro-specific values must be tabled
and kept current; a second distro is real porting work. (−) Cloudflare-first DNS means other providers
wait on the `dns.Provider` interface.

**Alternatives rejected.** An SPA + JSON API as the only UI (more web-tier surface than a server-rendered
panel needs); Postgres/MySQL for panel state (weight on one node — see ADR-0005); bundling a browser
runtime or CDN assets (offline/air-gapped installs must work).
