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
   attributes), `02-data-model.md` (tables you touch), and `06-test-strategy.md` (which checks are
   unit vs integration). Phases do not repeat these.
2. Read your assigned `phases/phase-N.md`. Build **only** its Deliverables. Honor its Guardrails.
3. Make its **Verification** gate pass — all of it. The gate is the definition of done, not your
   judgment that "it works."
4. Never cross a phase boundary. If phase N needs something a later phase owns, stub it per the spec
   and stop — do not pull the later work forward.

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
