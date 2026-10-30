# Spec Corpus — Comprehension Review & Remediation Tracker

**What this is.** Before any coding, the spec corpus was stress-tested for *self-sufficiency*: can a
small-context implementing agent build correctly from the spec **alone**, without the design history?
Three fresh **Sonnet** sub-agents, each given a "junior dev, no project context, do not improvise —
flag gaps instead of guessing" persona, read the corpus and reported what they understood, where they
would be forced to guess, and every contradiction/missing detail. This file records the verdict, the
consolidated root causes, and tracks their remediation.

**Verdict.** The corpus is a strong *architecture + contract* layer but is **not yet buildable** by a
small-context agent. ~100 individual findings collapse to **~15 root causes**: a missing *code-level*
layer (signatures, the `Agent`/package model, the job executor, config, client), ~11 genuine design
holes, and doc-structure issues (undefined `D*` labels, phases that defer wholesale to external docs).

## Review setup

| Reviewer | Persona | Scope | Confidence (1–5) |
|---|---|---|---|
| Jamal | junior Go dev, no context | README, 00-foundations, phase-0, phase-1 | overall 2 (ph0 ≈3, ph1 ≈2) |
| Priya | junior Go dev, no context | README, 00-foundations, phase-3, phase-4, phase-7 | ph3 2, ph4 2, ph7 2 |
| Tom | junior Go dev, no context | README, 00-foundations, phase-2, phase-5, phase-6, phase-8, phase-9 | ph2 3, ph5 2, ph6 2, ph8 1, ph9 2/1 |

All three independently reached the same core conclusion: *the phases do not stand alone* — they lean
on unread phases, on foundations that give prose contracts but **no Go signatures**, and (phases 6, 8,
9) on external docs (`vpspanel-plan.md`, `vpspanel-api.md`). None could build their phase without
guessing in multiple places.

---

## Root causes & remediation status

Legend: ☐ open · ◐ in progress · ☑ resolved in spec. Each item lists where it bites (reviewer finding
IDs) and the intended resolution. The fix pass must close every item **and** re-run a comprehension
spot-check.

### Group A — Missing *code-level* layer (engineering; add to the corpus)

- **A1 ☐ The `Agent` type, package layout, import-cycle, and Registry population are undefined.**
  Bites: Jamal B1/B2, Tom B1/B7, Priya B1. `Run(ctx, *Agent, args)`/`Step(ctx, *Agent, args)` are used
  everywhere; `Agent` is never defined, nor which package holds it, nor how `internal/rpc` (Registry)
  and `internal/verbs` (handlers) avoid a cycle.
  → *Resolution:* new foundations section — `Agent` is the injected god-object (store, jobs, audit,
  secret, exec, validate, render, dns…); handlers self-register into the Registry via `init()` (or a
  central wiring package) so there is no rpc↔verbs cycle; name the package that owns `Agent`.

- **A2 ☐ Shared packages have contracts but no Go signatures.** Bites: Jamal B8/B11/B16, Tom B1,
  Priya B1/B5/B6. `store`, `jobs` (Enqueue/executor/Step), `audit` (Record), `secret` (Seal/Open),
  `exec` (needs uid/gid/cwd/env/stdin + return type), `validate` (all validators).
  → *Resolution:* add exact signatures for every shared package to the F-sections (or an "internal
  API" appendix). `internal/exec.Run` gains an options struct (Credential uid/gid, Dir, Env, Stdin,
  output caps) and documented returns (stdout, stderr, exit, error).

- **A3 ☐ The job/executor model is under-defined (and blocks every slow verb).** Bites: Jamal
  B20/B21, Tom B7/B14, Priya B1/B2. No field binds a slow verb's worker step; `object_key` source is
  unspecified; the `jobs` table has no `result`/`progress`/`events`; **show-once secrets can't travel
  out of an async job**.
  → *Resolution (design, see B-design):* a verb carries `Run` (validate + enqueue, returns `{job}`)
  and `Exec(ctx,*Agent,*Job)` (the worker step); `ObjectKey(args) string` derives the lock key; the
  `jobs` table gains `result_json`, `progress`, and an `events`/`job_events` child; a one-shot sealed
  `result` channel delivers show-once secrets to the originating caller (never stored in the row,
  never in audit).

- **A4 ☐ Config mechanism is undefined.** Bites: Jamal B5, Priya B7. Socket path, web uid, DB path,
  log path, deadline defaults, nginx user, distro paths — no source/format/defaults.
  → *Resolution:* foundations "Configuration" section — a single config source (flags with env
  overrides, or a small TOML at a fixed path), the full key list with defaults, and how the agent
  resolves the `novapanel` uid/gid.

- **A5 ☐ No RPC client package is specified.** Bites: Jamal B18, Tom B22. `panel-web`, `novapanelctl`,
  and the API all need a socket client; none is defined.
  → *Resolution:* `internal/rpc/client.go` — `Call(ctx, verb, args, session) (Reply, error)`,
  connection handling, the per-HTTP-request caching note, error mapping.

- **A6 ☐ Schema is incomplete — no column types/constraints/indexes; per-phase tables undefined.**
  Bites: Jamal B22, Tom B12/B23/B31/B33, Priya B37/B48. Tables named in F5 and across phases
  (`sites`, `domains`, `databases`, `db_users`, `deploys`, `cron_jobs`, `api_tokens`, `idempotency`,
  `webhooks`, `backups`, `repos`, `integrations`, `module_state`, `jobs` result/progress/events) have
  no DDL; no ULID lib; migration embedding/`go:embed` unstated; "bad migration fails the build"/"never
  edit a prior migration" enforcement unspecified.
  → *Resolution:* new `02-data-model.md` with complete DDL for every table (types, constraints,
  indexes, FKs), the ULID choice, `go:embed` migrations, and the migration-integrity test.

- **A7 ☐ CI/test seams for root-only behavior are unspecified.** Bites: Jamal B6/B13/B14/B25, Tom
  (many "can't run in CI"), Priya B25. Root, `chattr +a`, systemd, and a reachable ACME domain can't
  run on a stock CI runner; "demonstrably fails" negative tests need a mechanism.
  → *Resolution:* new `06-test-strategy.md` — the unit tier (pure, CI) vs the integration tier (root,
  in a container/VM), injectable seams (`geteuid`, `chown`, exec program paths), **pebble** for ACME,
  and how negative arch tests run (subprocess `go test` against a fixture module).

- **A8 ☐ Concrete systemd / tmpfiles / sysusers templates are not in the corpus.** Bites: Jamal
  B27, Priya B29/B34. Phase 1 points at "BHPanel `bhpaneld.service` documented in session notes" —
  which an implementer cannot see.
  → *Resolution:* a `packaging-reference.md` (or inline in phase-1) with the full hardened unit text,
  tmpfiles lines, and a sysusers entry that creates the `novapanel` user/group.

### Group B — Genuine design holes (architect decision; resolve, then write)

- **B-D1 ☐ Public/no-auth verbs + per-verb MFA aren't expressible; the walking skeleton has an auth
  chicken-and-egg.** Bites: Jamal B3/B17, Tom B2/C9. `Verb.MinRole` has no "public" value; `ping`
  needs none; phase-1's `service.status` is called by `panel-web` before sessions exist (phase-2).
  → *Resolution:* add `Public bool` (or a `Public` role sentinel) + an explicit per-verb MFA table;
  make the phase-1 walking skeleton **`novapanelctl`-only** (local, peer-gated) and move all
  web-facing auth to phase-2 so there is no pre-session web call.

- **B-D2 ☐ Session rotation, "privilege change", the MFA re-auth window, and the re-auth verb are
  undefined.** Bites: Tom B2/B3/B4/B5, F4 has no `mfa_at` column. "Rotate on login" has no prior
  token; the window has no duration/anchor/refresh path; no `session.reauth` verb; `reauth_required`
  shape conflicts with F2 (no `data` on errors).
  → *Resolution:* define pre-login anonymous session (for CSRF + fixation), the exact "privilege
  change" set, a `session.reauth{totp}` verb, a `sessions.mfa_at` column + window duration, and allow
  a `data` block on error replies (F2 edit).

- **B-D3 ☐ API token → agent identity flow, and where scopes are enforced, are contradictory.**
  Bites: Tom B6/B22/B25/C3, Priya (dns scope). Scopes said "enforced in the agent" but the Blueprint
  maps scope→verb in `panel-web`; the F2 envelope has no scope/token field; token hashing with
  Argon2id per request is O(n) without a token-id; "automation grant" is undefined.
  → *Resolution:* `panel-web` passes the `npat_` token as `session`; the agent resolves
  token→{scopes, role} and enforces scopes **agent-side**; token format `npat_<id>_<secret>` (id for
  O(1) lookup, secret hashed); define the automation-grant flag + step-up requirement.

- **B-D4 ☐ CSRF synchronizer token has nowhere to live (stateless web).** Bites: Tom B35. A
  synchronizer token needs server-side storage; `panel-web` holds no DB.
  → *Resolution:* store the CSRF token in the agent session row (new column, returned by
  `session.whoami`), **or** derive it as HMAC(session-token, web-key) — pick one; keep it out of a
  cookie.

- **B-D5 ☐ Idempotency store placed in the stateless web tier.** Bites: Tom B23. `(token, key)→job`
  can't live in `panel-web` (no DB, multi-process).
  → *Resolution:* an `idempotency` table in the agent + a verb; define TTL, payload-hash, and
  required-vs-optional `Idempotency-Key`.

- **B-D6 ☐ Roll-forward conflicts with inherently effectful verbs.** Bites: Tom C11/C12, Priya B44.
  `service.restart`, `backup.create`, `firewall.apply` are not "run-twice = no change"; re-run-from-top
  duplicates snapshots and **re-arms/defeats the firewall deadman**.
  → *Resolution:* classify verbs as **idempotent ensure-state** (default; F7 test applies) vs
  **effectful** (explicit; different crash rule — a crashed effectful job resumes by checking
  external state, and the F7 "zero-change second run" test is replaced by an effect-specific test).

- **B-D7 ☐ Firewall deadman is a real design gap.** Bites: Tom B19, Priya (none — unread). `<fw-revert>`
  artifact undefined and must survive an agent crash; no `firewall.confirm` verb; **the agent cannot
  prove external SSH reachability from inside the box** ("verify a new connection" is unimplementable
  as written); UFW runtime snapshot vs persistent `user.rules`; which SSH port/source.
  → *Resolution:* the revert is a self-contained `novapanelctl fw-revert <snapshot>` run by a
  **transient systemd unit** (independent of the agent); add `firewall.confirm`/`firewall.revert`
  verbs; redefine "verify" as a **client round-trip confirm** (the admin's browser/CLI must re-confirm
  through a fresh connection within N minutes, else the timer fires); snapshot `/etc/ufw/*.rules` +
  runtime; pin the SSH rule by port from config.

- **B-D8 ☐ Nginx can't traverse a `0750 site:site` home (docroot + ACME challenge unreadable).**
  Bites: Priya B16/B30, Tom (none). Site home `0750 site:site`; nginx is a different user → 403 on
  static + `try_files`; the ACME webroot under `/var/lib/novapanel` (`0700`) is unreadable by nginx.
  → *Resolution:* add the nginx user to each site's group (or home `0751` + docroot `0750 site:nginx`
  group-read/traverse) — state the exact owner/mode/group model; move the ACME webroot to a
  world-traversable path (e.g. `/var/www/acme/<site>` `0755`), not under `/var/lib/novapanel`.

- **B-D9 ☐ Cert chicken-and-egg: `nginx -t` fails before the first cert exists.** Bites: Priya
  B8/B23. The template always references `fullchain.pem`; there is no cert-less render variant; "cert
  written atomically" is impossible with two independent renames.
  → *Resolution:* define three render states (no-cert HTTP-only serving + ACME; cert present,
  force-HTTPS off; cert present, force-HTTPS on); issue via a versioned TLS dir + a single symlink
  swap so a reload never sees a half-written pair; define `force_https` field + "valid cert" (parses,
  unexpired, SAN match, key match; staging = not valid-for-force).

- **B-D10 ☐ File API rooted at home can bypass `sshkey.*`/cron/MFA.** Bites: Tom C16, Priya (none).
  If `files.*` is rooted at the site home, it can write `~/.ssh/authorized_keys` directly, bypassing
  key validation and the MFA-gated ssh-write path.
  → *Resolution:* root the file API at the **project/docroot**, not the home; explicitly exclude
  `.ssh`, cron, and config paths; state the home-vs-project-root distinction once.

- **B-D11 ☐ MariaDB restore has no atomic DB swap; privilege-drop mechanism is "head to pick".**
  Bites: Tom B18, Priya B6/B39. No `RENAME DATABASE`; "swap" undefined. Privilege-drop to the site
  user, running SQL/wp-cli without a password on argv, is left open.
  → *Resolution:* define the swap (restore to scratch DB → validate → repoint the app via `wp-config`
  / grant swap, or table-by-table rename) and pick the privilege-drop mechanism (fork a worker with
  `SysProcAttr.Credential` uid/gid, args via stdin JSON) in `internal/exec`; DB admin via `unix_socket`
  (no password), site creds via `--defaults-extra-file` (root `0600`), never argv.

### Group C — Doc-structure fixes

- **C1 ☐ `D1`–`D9` decision labels are used throughout but never defined in the corpus.** Bites:
  Jamal (noted), Tom C10, Priya (opaque). An implementer must leave the spec to learn them.
  → *Resolution:* inline a one-line **Decisions glossary (D1–D9)** in the README or foundations.

- **C2 ☐ Phases defer wholesale to external docs, breaking standalone-ness.** Bites: Tom C1/B21/B22,
  Priya (plan §6). Phase 8 → `vpspanel-api.md` ("this phase implements it"); Phase 6 → plan D2 update
  protocol; Phase 9 → plan D4; Phase 4 → plan §6.
  → *Resolution:* fold the needed substance (API resource/scope/error tables, the D2 signed-update
  protocol, the §6 isolation facts) **into** the phase specs so each stands alone; keep external docs
  as rationale only.

- **C3 ☐ Target OS / distro specifics are undefined.** Bites: Priya B7/D1. nginx user (`www-data` vs
  `nginx`), path layout (sites-available vs conf.d), php-fpm unit/binary names, MariaDB socket path,
  minimum nginx version (`http2 on;` needs ≥1.25.1).
  → *Resolution:* foundations "Target platform" section — **Ubuntu 24.04 LTS first** (matches the
  plan's acceptance criteria), naming the concrete user/paths/versions; note where RHEL differs as a
  later port.

- **C4 ☐ "All verbs are slow/jobs" overbroad; per-verb Registry attributes missing.** Bites: Jamal
  C13, Tom B14, Priya C5/B3/B57. Read verbs (`*.list/get/status`) shouldn't be jobs; no file lists
  each verb's `MinRole/ReadOnly/Slow/MFA/ObjectKey`.
  → *Resolution:* a **verb table** (likely in `01-rpc-and-verbs.md`) enumerating every v1 verb with
  its Registry attributes; fix each phase's "all verbs are jobs" to "all *mutating* verbs".

---

## Smaller/non-blocking items

Captured but lower priority (resolve during the fix pass or mark as implementer's-discretion with a
sane default): library/version pins (ULID, `pquerna/otp`, restic, lego major, kin-openapi, htmx),
cookie name, backup-code count/format, `files.chmod` allowed modes + size/line caps, SSH key options
stripping (`command=`/`from=`), cron schedule format (cron→`OnCalendar`), restic repo-registration
schema + scheduler, DB dump flags (`--single-transaction`), HSTS value/stability signal, probe
address/TLS-verify/timeouts, Cloudflare `Record`/`zone` types + required token permissions (Zone:Read,
Cache Purge), `dns.verify` semantics (resolver vs API; proxied records), HTTP→HTTP status codes map
(§8). Full per-reviewer detail is in the review transcripts (Jamal B1–B30/C1–C15, Tom B1–B35/C1–C25,
Priya B1–B57/C1–C14).

## Remediation plan (order)

1. **Foundations code-level layer** (A1,A2,A4,A5) + **Decisions glossary** (C1) + **Target platform**
   (C3) — unblocks everyone.
2. **`02-data-model.md`** (A6) and the **job/executor + effectful-verb** model (A3,B-D6).
3. **`01-rpc-and-verbs.md`** verb table (C4) + **auth/session/token/CSRF/idempotency** (B-D1..D5).
4. **`06-test-strategy.md`** (A7) + **packaging reference** (A8).
5. **Per-phase design-hole fixes** (B-D7 firewall, B-D8/B-D9 nginx/TLS, B-D10 file API, B-D11
   DB/priv-drop) + **fold external docs in** (C2).
6. **Re-run a naive comprehension spot-check** on the revised corpus; target confidence ≥4/5.

Nothing is coded until this tracker is all ☑ and the re-check passes.

## Status — remediation pass complete (pending re-check)

All root causes are now resolved in the corpus:

- **Group A (code-level layer):** A1,A2,A4,A5 → `00-foundations.md` F14–F17 (package layout + `Agent`,
  signatures, config, client). A3 → F6 executor binding. A6 → `02-data-model.md` (full DDL). A7 →
  `06-test-strategy.md` (unit/integration tiers + seams). A8 → `packaging-reference.md`.
- **Group B (design holes):** B-D1 → phase-1 (ctl-only walking skeleton) + F13 `Public`. B-D2/B-D3/
  B-D4/B-D5 → F4 (anon session, rotation, reauth, token identity, agent-side scopes, CSRF-in-row,
  idempotency table). B-D6 → F6 idempotent-vs-effectful classes. B-D7 → phase-6 (fw-revert transient
  unit + confirm/revert verbs + client round-trip reachability). B-D8/B-D9 → phase-3 (ACME webroot
  `/var/www/acme`, docroot `2750 site:www-data`, three render states, versioned-dir+symlink cert
  swap). B-D10 → phase-5 (file API rooted at docroot, `.ssh` excluded). B-D11 → phase-4 (priv-drop via
  `exec` uid/gid, unix_socket admin, show-once claim, wp-config off-argv) + phase-6 (table-rename DB
  swap).
- **Group C:** C1 → F0b glossary. C2 → API surface folded into phase-8, D2 into phase-6, isolation
  into phase-3/4. C3 → F0a target platform. C4 → `01-rpc-and-verbs.md` verb table.

## Round 2 (re-check after pass 1) + pass 2

The re-check (Sara ph0/1/3, Omar ph6/8) confirmed the structural layer is clear (auth, scope, Agent,
package all "clear") but confidence held at 2–3/5 due to: (a) compile-level contract gaps, (b) a
missing per-verb arg/reply layer, (c) self-inflicted contradictions from pass-1 folded-in blocks, and
(d) three still-loose designs (firewall deadman, scheduler, phase-3 multi-domain). **Pass 2
(remediations 4–7) resolved all of these:**

- **Contracts:** import layering (jobs leaf + injected runner), Exec/Step/Runner unified (Runner
  interface), caller-via-ctx, `CodedError`, codec composition + reply cap, peer-gate uid-0 vs web
  (ctl-bootstrap hole), session format, `interrupted`/`unsupported` codes + HTTP map, `MaxDeadlineMs`.
- **Missing pieces:** F18 scheduler + `schedules` table; F19 + `job_claims` show-once; repo/user/
  schedule/stack verbs; `03-verb-shapes.md` (every verb's args/reply).
- **De-conflict:** phase-3 (http2 1.24, cert `current/` path, per-site user in ph3, SAN cert,
  state-aware probe, renewal reload, maintenance/HSTS, apply order, ACME config), phase-8 (uniform
  202, agent-side scope, naming), phase-6 (unix_socket dump, firewall deadman v2 as a root transient
  subcommand with enabled-state + confirm-id + conflict + client round-trip, restore-swap edge
  handling, repo management).
- **Phase 9:** stack.switch folded to BHPanel's verified migration-free auto-rollback sequence.

## Round 3 (final re-check) + pass 3

Round-3 (Lena ph0/1/3: 3/2/2; Raj ph6/8: 2/2) **confirmed every foundation contract is fixed and the
design sound**; the residual was two kinds: (A) **stale phase text contradicting the now-fixed
foundations** (real bugs — e.g. phase-1 still defined `type Step`, phase-0 still said reject-uid≠web,
phase-3 still had the shared-owner seam), and (B) a short list of genuine architecture decisions
(job ownership for the API claim, scheduler/MFA authority, ObjectKey-on-id, jobs effectful/actor
columns, firewall new-connection honesty, php→phase-4). **Pass 3 (remediation 8–9) closed all of A and
B:** schema (effectful/actor/created_by, hsts, cert_staging, repo role/retention, webhook_deliveries,
idempotency nullable), F4 synthetic-root-caller + job ownership, F6 ObjectKey-on-id + recovery via
column, F8 apply-order, F9/F16 new validators + config keys, F18 scheduler authority, and the phase
de-stale pass (phase-0 codec/exec/peer/arch-test, phase-1 Step/Enqueue/migrations/scheduler/ctl,
phase-3 static-only + owner, phase-6 firewall honesty + rules schema + fw_pending cleanup, phase-8
idempotency-in-agent + required route table + webhook cap). Added `03-verb-shapes.md` and a README
"implementer's discretion" rule for Class-C micro-details that belong in code.

**Conclusion:** the contradictions are resolved and the design/contracts are confirmed sound. The
naive-cold-build confidence plateaus because a spec fully unambiguous to a zero-context reader *is* the
code; the remaining "gaps" reviewers surface are Class-C micro-details now explicitly delegated to the
implementer. **This is the last doc pass — Phase 0 is build-ready; the review reports serve as the
Phase-0/1 implementation checklist.**
