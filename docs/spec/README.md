# NovaPanel — Implementation Spec Corpus

**Audience:** the implementing agents (and humans) who will build NovaPanel. This corpus is the
**contract you build against.** You do not re-decide what is written here; you implement it and prove
it with the verification gate. Architecture/decisions live in `../vpspanel-plan.md`; this corpus turns
those decisions into buildable, verifiable units of work.

> **Role split.** The architecture head authors and owns this corpus (specs, blueprints, verification
> gates, external-reference gathering). Implementing agents execute a phase spec exactly, make its
> verification gate pass, and stop. An implementing agent that finds a spec wrong or underspecified
> **raises it** (does not silently reinterpret) — the head amends the spec, then work resumes.

## How an implementing agent uses this corpus

1. Read `00-foundations.md` **first, every time** — the shared contract (incl. the `Agent` type,
   package layout, signatures, config, client). Then skim `01-rpc-and-verbs.md` (your verb's
   attributes), `03-verb-shapes.md` (its args/reply struct), `02-data-model.md` (tables you touch),
   and `06-test-strategy.md` (which checks are unit vs integration). Phases do not repeat these.
2. Read your assigned `phases/phase-N.md`. Build **only** its Deliverables. Honor its Guardrails.
3. Make its **Verification** gate pass — all of it. The gate is the definition of done, not your
   judgment that "it works."
4. Never cross a phase boundary. If phase N needs something a later phase owns, stub it per the spec
   and stop — do not pull the later work forward.
5. **Implementer's discretion (with a sane default) for unspecified micro-details.** The corpus fixes
   the contracts, the data model, every verb's attributes + args/reply, the security invariants, and
   the hard designs. It deliberately does **not** pin every library version, exact reply sub-field,
   buffer size, retry count, or log string — pick the idiomatic Go default, keep it consistent with
   the tables, and note it in the PR. Only a choice that would change a contract, an invariant, a
   schema, or a verb's attributes must come back to the head. (Do not treat an unpinned micro-detail
   as a blocker; a contradiction with a fixed contract **is** a blocker — raise it.)
6. **Source-of-truth precedence (read this before you copy any value).** The Phase-1 review surfaced a
   recurring class of bug: a value restated in a phase's prose had drifted from the canonical source.
   To prevent it, obey this precedence — the **canonical file wins over any phase prose**, always:
   - a verb's **Role / ReadOnly / Scope / Slow / Effectful / MFA / ObjectKey** → `01-rpc-and-verbs.md`
   - a verb's **args / reply shape** (snake_case field names, envelopes like `{jobs,next}`) → `03-verb-shapes.md`
   - an **error `code`** on the wire → the **F3 catalog** in `00-foundations.md` (never invent a code;
     if the one you want isn't listed, raise it — do not ship an unlisted code)
   - a **table / column / migration** → `02-data-model.md`
   - a **cross-cutting contract** (audit order, effectful class, claim lifecycle, config-render loop,
     validation, session/authz) → the named **F-section** in `00-foundations.md`

   A phase spec's prose is a **guide, not a second source of truth**: when it names a role, code, shape,
   or column, treat that as a pointer to the canonical file and **verify against the file**. If the two
   disagree, the canonical file governs and you **raise the drift** (the head fixes the prose) — you do
   **not** implement the prose value.
7. **Pre-flight contract check (run before coding, and again before you open the PR).** For every verb
   you touch: ☐ its attributes match `01`; ☐ its args/reply match `03` exactly (snake_case, documented
   envelope); ☐ every `code` you return is in the F3 catalog; ☐ every table/column matches `02`; ☐ each
   cross-cutting rule is **cited from its `00` F-section, not paraphrased**. A mismatch is a blocker you
   raise, not a judgment call. (This check is what would have caught all four Phase-1 reconciliations up
   front.)

## Pre-review verification checklist (every phase — tick all before requesting review)

An implementing agent **does not request architecture review until every box below is ticked on the PR
head**, and the PR comment that requests review **includes this checklist, ticked**. An unchecked box is
not "the reviewer will catch it" — it is work that isn't done. The same list gates the final wiring task.

- [ ] **Full gate green on the PR head:** `go build ./...`, `go vet`, `staticcheck`, `govulncheck`,
      `gofmt -l` (empty), `go test ./...`, and the three architecture tests **with their negative
      fixtures**. Paste the results in the PR.
- [ ] **This phase's own Verification section passes in full** (it is the definition of done, not your
      judgment that "it works").
- [ ] **Pre-flight contract check done** (item 7 above): verb attributes ↔ `01`, args/reply ↔ `03`,
      every `code` ↔ the F3 catalog, tables/columns ↔ `02`, cross-cutting rules cited from `00`.
- [ ] **No invariant weakened to make something pass** — the agent boundary, the peer gate, the no-shell
      rule, the config-sink guards, and secret/audit handling are all intact. A green check bought by
      relaxing a guard is a blocker, not a pass.
- [ ] **CI is green for the right reason** — a check that can pass on warnings or skipped bodies doesn't
      count; the gate must actually exercise the code (this is the Phase-1 `systemd-verify`/integration
      lesson).
- [ ] **Scope is this phase only** — no later-phase work pulled forward; later-owned needs are stubbed
      per spec. Docs/prototype files match `main` unless this phase owns them.
- [ ] **Open items answered** — every bot/review finding is verified and either fixed or answered with a
      traced reason; nothing silently ignored.

If a box can't be ticked because the spec is wrong or underspecified, **raise it to the head** (don't
reinterpret); the head amends the spec, then work resumes.

## Contract-completeness check (head: before a phase spec is confirmed for coding)

Phase 2 surfaced six recurring classes of under-specification. The head runs this check over **every**
phase spec *before* it is opened for implementation, so each gap is closed in the contract instead of
being rediscovered mid-build (one gap → one stall → one patch). A phase that fails any item is amended
first.

1. **Error-code completeness.** Every documented refusal/precondition **names its F3 code** (no
   "refuses until X" without a code). Every code used is in the F3 catalog; the only non-catalog codes
   are the web-tier superset (`agent_unavailable`, the `429` `rate_limited`).
2. **Show-once secrets.** Every verb that returns a newly-minted secret is covered by the **F11
   show-once reply exception** (sync) or the **F19 claim path** (slow), or is explicitly excluded as
   cheaply re-obtainable.
3. **Stateless-web read paths.** Any per-request state the web tier must display (CSRF, a claim, a
   pubkey) has a **registered read path**; nothing assumes web-tier memory across requests.
4. **Throttle policy.** Every rate-limited / ACME-limited / deadman verb pins its **keying, window,
   reset, and exhaustion code**.
5. **Config wiring.** Every F16 key the phase uses is **read from `Cfg`**, not a restated literal
   (drift risk — the `idle_sec`/`abs_sec` and `firewall_window_min` lesson).
6. **Authz exceptions.** Every non-standard authz rule (self-scope, automation grant, internal/
   peer-only) spells out **subject-resolution and token-reachability** (the B4 self-service shape).

## The non-negotiable invariants (every phase, no exceptions)

These restate `vpspanel-plan.md` §3. Any phase that violates one is rejected regardless of features.

1. **`panel-web` is never root.** `panel-agent` is the only root process. CI asserts it.
2. **No shell.** The agent executes **argv arrays, never `sh -c`**; no command string is built from
   input. There is no `shell.execute`/run-command verb — ever. CI greps to keep it so.
3. **Every agent handler is a registered verb.** `internal/rpc/messages.go` is the single registry;
   an architecture test fails the build if any reachable handler is unregistered.
4. **Validate at the sink, on every verb.** Names (domain/site/user/db) are attacker-controlled;
   validate against an allowlist in the agent before they touch argv or a template.
5. **Default-deny auth.** Every route/verb has an explicit auth decision; a build test fails if any
   lacks one. `actor` is an audit label, never an authz input (identity = agent-minted session token).
6. **Never render an empty/unquoted value into a system config.** Renderers validate, write to temp,
   `nginx -t`/equivalent, atomic-rename, reload, health-probe, revert on failure.
7. **Append-only audit, written by the agent** (`chattr +a`, root-owned) — the source of truth for
   privileged actions, not forgeable by a compromised web tier.
8. **Secrets sealed by the agent** with a root-held key; a `panel-web` compromise leaks ciphertext.
9. **Signed + anti-rollback updates;** verify signature before parsing a manifest; refuse a version
   older than installed; unpack defensively (tar-slip).
10. **Assume the web tier will eventually have an RCE and make it non-catastrophic.** That is the
    whole point of the agent boundary; never add a verb that undoes it.

## Phase-spec template (every `phases/phase-N.md` has exactly these sections)

```
# Phase N — <title>
Goal            — one sentence: what capability exists when this phase is done.
Depends on      — prior phases/sections that must be complete.
Deliverables    — the concrete files/features that exist at the end (the scope).
Blueprint       — files to create, interfaces/signatures, key logic (enough to implement from).
External refs   — links/material the implementer needs (gathered by the head, not re-researched).
Verification    — the EXACT tests/commands that must pass. This is the gate = definition of done.
Definition of Done — a checklist mirroring Verification + Deliverables.
Guardrails      — the "nevers" specific to this phase (plus the global invariants above).
```

## Corpus map

| File | What it specifies | Status |
|---|---|---|
| `00-foundations.md` | Shared contracts F0–F17: target platform, D1–D9 glossary, RPC envelope/codec, error catalog, session/authz/token/CSRF, job/executor + effectful class, config-render, audit, secrets, exec runner, validation, **package layout + the `Agent` type, internal signatures, config, RPC client** | **drafted** |
| `01-rpc-and-verbs.md` | The canonical v1 verb table — every verb's Role/ReadOnly/Scope/Slow/Effectful/MFA/ObjectKey; naming reconciliations | **drafted** |
| `02-data-model.md` | Complete SQLite DDL for every table (per phase), ULID/migrations/integrity test | **drafted** |
| `03-verb-shapes.md` | The args struct + reply shape for every v1 verb (complements 01's attributes) | **drafted** |
| `05-adr.md` | Architecture Decision Records (ADR-0001…0016): the *why*, cost, and rejected alternatives behind each binding decision; D1–D9 map onto these | **drafted** |
| `06-test-strategy.md` | Unit vs integration tiers, CI seams (injectable geteuid/exec, pebble for ACME), negative arch tests | **drafted** |
| `packaging-reference.md` | Concrete systemd units, tmpfiles, sysusers (the former "session notes") | **drafted** |
| `phases/phase-0.md` | Foundation: Go workspace, PHP freeze, RPC registry + codec, CI + supply-chain gates, arch tests | **drafted** |
| `phases/phase-1.md` | State & plumbing: SQLite + migrations, durable jobs (roll-forward, not compensation), audit, systemd packaging, walking skeleton (`service.status`) | **drafted** |
| `phases/phase-2.md` | Auth & UI shell: Argon2id, sessions, TOTP MFA, HTMX shell + CSRF, roles | **drafted** |
| `phases/phase-3.md` | Nginx sites/domains/TLS: site model, render-and-revert loop, ACME HTTP-01 | **drafted** |
| `phases/phase-4.md` | PHP/WordPress/MariaDB: per-site user+FPM pool, DB lifecycle, WP install | **drafted** |
| `phases/phase-5.md` | Daily controls: file API, Git deploy, cron, log viewer, SSH keys | **drafted** |
| `phases/phase-6.md` | Backups/firewall/status: Restic, UFW safe-apply, self-backup, updates (D2) | **drafted** |
| `phases/phase-7.md` | DNS (external first): `dns.Provider` + Cloudflare | **drafted** |
| `phases/phase-8.md` | Integrations & API: OpenAPI, scoped tokens, deploy API + webhooks (D1) | **drafted** |
| `phases/phase-9.md` | Optional modules (after core is boring): OLS perf mode, mail, PowerDNS, FTP, Docker | **drafted** |

**All phase specs are drafted** with external references gathered (Nginx/FPM/ACME, Restic/UFW,
Argon2id/TOTP/MariaDB/WordPress — verified values baked into the specs). Coding of a phase begins only
after its spec is confirmed; phases run in order, each gated by its Verification section.
