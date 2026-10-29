# NovaPanel — Design Session Log & Handoff

**Purpose:** resume the NovaPanel redesign cleanly in a future session. This is the "start here"
index: what was decided, what's still open, what every document contains, and the next step.

- **Branch:** `ccr-9388fb5b-pkmsyf` · **PR:** md-riaz/novapanel#54 (draft, docs-only)
- **Last updated:** 2026-10-28
- **What this is:** a greenfield **Go rewrite** of NovaPanel as a **simple-yet-powerful, single-VPS
  control panel** (one owner/team; no multi-tenancy, resellers, packages, or billing), replacing the
  current PHP NovaPanel in this repo over time.

---

## 1. Document index (read in this order)

| Doc | What it is |
|---|---|
| `docs/vpspanel-plan.md` | **The authoritative Plan v2.** Vision, boundary, architecture, security invariants, decisions, data model, isolation, phases, tests, release, acceptance criteria, known-traps appendix. Start here. |
| `docs/vpspanel-architecture-review.md` | Structural critique of the original plan (RPC surface, phase gates, risks) + source-audit provenance table. |
| `docs/vpspanel-plan-amendments.md` | The **source audit**: panel-by-panel corrections, the four settled decisions, cross-cutting CVE-derived invariants, and §0 — BHPanel verified against its real shipped release. |
| `docs/vpspanel-api.md` | **API design** — public REST API (tokens, scopes, async jobs, endpoints, webhooks) + internal agent RPC. |
| `docs/prototypes/novapanel-dashboard.html` | Clickable **UI prototype** (hPanel-inspired, single-VPS). Also published as an artifact (link below). |
| `docs/vpspanel-session-log.md` | This file. |

---

## 2. Decisions settled

- **Product name:** `NovaPanel`. Binary `novapanelctl`.
- **`go.mod` module path:** **`novapanel`** (bare — no domain). Imports read `novapanel/internal/...`.
  Builds locally with zero setup; not remotely `go get`-able, which doesn't matter for a proprietary
  app built from its own repo. Switchable to a domain path later via a find-replace if ever needed.
- **PHP cutover:** **freeze the existing PHP on a branch/tag** (e.g. `php-legacy`); the Go rewrite
  takes a clean repo root. PHP history stays in git. Execute as the first Phase-0 step, on a fresh
  branch (not done yet).
- **License:** proprietary. (Repo is currently MIT; relicensing mechanics to be executed on request;
  prior MIT commits remain MIT in history. Consider making the repo private before the first
  proprietary Go commit.)
- **Architecture:** unprivileged Go `panel-web` + root Go `panel-agent` over a `0660 root:novapanel`
  Unix socket, typed verbs only, authz enforced in the agent. Verified sound against BHPanel's real
  release.
- **D1 — Webhooks:** HMAC-SHA256 + replay protection; secret verified **in the agent**; uniform
  refusal. Scoped bearer token for the simple deploy API.
- **D2 — Updates:** notify + one-click apply (never silent auto-apply); signature verified before
  apply; explicit advisories.
- **D3 — Per-site isolation:** Unix socket + `pm=ondemand` + Nginx-direct static; per-site non-login
  user + `0750` home + per-site FPM pool. **Drop kernel disk/inode quotas** (tenant-metering only);
  optional single shared `novapanel.slice` cap later.
- **D4 — Modules:** first-party, compiled-in only; `module.install` enables/configures a shipped
  module, never fetches/executes code.
- **D5 — Caller trust:** `actor` = audit label only; identity = agent-minted session token; socket
  gated by `0660` group **+ SO_PEERCRED uid-pin** (reject any peer uid ≠ `panel-web`).
- **D6 — Job model:** durable job queue + **single in-process worker goroutine**, per-object
  serialized; `202 {job}` + `job.status` for slow verbs; sync only for reads.
- **D7 — Crash safety:** idempotent "ensure-state" verbs + **roll-forward** (re-run stuck job from
  top); one `reconcile` verb heals drift + half-created state; liveness-checked lock.
- **D8 — Config generator = attacker-controlled sink:** allowlist names, no empty/unquoted
  interpolation, `nginx -t` before atomic-rename+reload, `O_NOFOLLOW`, no `chown -R` of site paths.
  Build-enforced.
- **D9 — State ownership:** the **agent owns all state** (root-only SQLite); `panel-web` is
  stateless (no DB). (Switched from the earlier "web owns SQLite" plan.)
- **Updates (D2, lighter):** ed25519-signed manifest + verify-before-parse + **anti-rollback** +
  tar-slip-hardened unpack now; beta/stable channels + approval-gated CI signing → Phase 4.
- **Web stack:** Nginx-only in v1 (OpenLiteSpeed, mail, PowerDNS, FTP, Docker all deferred modules).
- **DB:** SQLite/WAL control plane (metadata only); MariaDB for sites; one engine by default.
- **Tenancy:** dropped entirely (~90 of BHPanel's 267 verbs). Roles = owner / admin / read-only.

## 3. Remaining actions (all decisions settled — these are execution steps for Phase 0)

1. **Execute the PHP freeze** — branch/tag the current PHP as `php-legacy`, give the Go rewrite a
   clean root. First Phase-0 action, on a fresh branch.
2. *(Optional, pairs with proprietary license)* make the repo private before the first Go commit.

No design decisions remain open — Phase 0 is unblocked.

---

## 4. Source audit — one-line conclusions

Full detail in `vpspanel-plan-amendments.md`. We downloaded and read BHPanel's real release (0.2.88).

- **BHPanel** — architecture **verified** (unprivileged PHP web + root Go daemon, typed verbs over a
  `0660` socket, agent-side authz, systemd-first, append-only `chattr +a` audit, HMAC deploy webhook
  verified in the daemon, signed installer). It's **multi-tenant**; we keep its security, drop its
  tenancy. Its stack is OpenLiteSpeed + mail/PowerDNS/Mongo; broader than our v1. Beta-only, no
  upgrade path, no user docs.
- **BHPanel release infra** (`wpexpertinbd/bhpanel-releases`, audited 2026-10-29; latest beta
  **0.2.91**) — a full **signed supply chain** we adopted a lighter form of: ed25519-signed
  beta/stable channel manifests, public key compiled into the daemon, **verify-before-parse**,
  **anti-rollback**, tar-slip-hardened unpack, `install.sh` sha256-pinned in the signed manifest,
  signing key only in a required-reviewer-gated CI `sign` environment, mirror root-owned with the
  release dir `chattr +i`. Drove the D2/§7 refinement. No new panel-code lessons (0.2.88→0.2.91 is
  three patch bumps).
- **aaPanel** — the **cautionary tale**: root monolith with authenticated command-injection → root
  RCE CVEs. The argument *for* our privilege split (only works with argv, no shell interpolation).
- **Coolify** — webhook is a **bearer token, NOT HMAC** (plan's "HMAC like Coolify" was false);
  Docker-mandatory; image-based rollback.
- **Caddy** — auto-reverts **load-time** failures only; our HTTPS health-check-and-revert loop is
  **net-new engineering**, not a port.
- **CloudPanel** — per-site user + FPM pool is proven prior art, but it uses **loopback TCP**, not a
  socket; "no shared www-data" is write-only (static still needs read).
- **ISPConfig** — scales multi-node **on MySQL** (shared control DB + pull agents); don't cite it for
  "Postgres for multi-server" — the real trigger is SQLite write-concurrency.
- **CyberPanel** — 2024 pre-auth RCE (POST-only filter bypassed by PUT; no auth check) → 22k boxes
  ransomed, CISA KEV; patched **silently**. Source of our invariants: validate at the sink on every
  verb, default-deny auth, argv-only, signed releases + explicit advisories.
- **HestiaCP** — firewall lockouts → our dry-run + timed auto-revert + never-removable SSH rule.
- **OpenPanel/Froxlor/Virtualmin** — containers cost a ~0.87 GB floor/user (justifies no-containers
  default); Virtualmin/Webmin runs its UI as root (the anti-pattern we reject).

---

## 5. UI prototype

- **Published artifact:** https://claude.ai/artifact/CMETzSRD3ieaL1sbzfPheW
- **Repo copy:** `docs/prototypes/novapanel-dashboard.html` (open locally or re-publish).
- hPanel-inspired: consolidated sidebar + Favourites, server-context card (single VPS), quicksearch,
  per-site **Manage** card dashboard, create-site wizard, dashboard/websites/databases/backups. Light
  + dark, responsive. Doubles as a visual spec for Phase 2/3.

---

## 6. Suggested next steps

1. Decide the **two open blockers** (`go.mod` path, PHP cutover).
2. Then **Phase 0/1 bootstrap**: Go workspace (`panel-web`, `panel-agent`, `novapanelctl`),
   threat-model + trust-boundary docs, the **canonical typed-RPC registry** (BHPanel's `Daemon.php`
   pattern is the reference), SQLite migrations, **CI + supply-chain gates** (none exist today), and
   the **walking-skeleton verb** (`service.status` end-to-end).
3. Or continue **UX**: expand the prototype (Domains & DNS, Files, live job drawer, firewall
   safe-apply screen), or turn it into the real `html/template` + HTMX shell.

## 7. How to resume

> "Read `docs/vpspanel-session-log.md` and the docs it indexes. We're building NovaPanel as a Go
> single-VPS panel. Pick up at [Phase 0 bootstrap / the PHP cutover decision / prototype iteration]."

PR #54 collects all of this. The current PHP NovaPanel is untouched on `main`.
