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

- **Product name:** `NovaPanel`. Binary `novapanelctl`. (Exact `go.mod` module path still to pick.)
- **License:** proprietary. (Repo is currently MIT; relicensing mechanics to be executed on request;
  prior MIT commits remain MIT in history.)
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
- **Web stack:** Nginx-only in v1 (OpenLiteSpeed, mail, PowerDNS, FTP, Docker all deferred modules).
- **DB:** SQLite/WAL control plane (metadata only); MariaDB for sites; one engine by default.
- **Tenancy:** dropped entirely (~90 of BHPanel's 267 verbs). Roles = owner / admin / read-only.

## 3. Open blockers (decide before first code commit)

1. **`go.mod` module path** — the import path / product namespace (name is NovaPanel; path TBD).
2. **PHP NovaPanel cutover** — move existing PHP under `legacy/` (or freeze on a branch), keep both
   buildable during transition, define the explicit cutover. **This is the last gate before Phase 0.**

---

## 4. Source audit — one-line conclusions

Full detail in `vpspanel-plan-amendments.md`. We downloaded and read BHPanel's real release (0.2.88).

- **BHPanel** — architecture **verified** (unprivileged PHP web + root Go daemon, typed verbs over a
  `0660` socket, agent-side authz, systemd-first, append-only `chattr +a` audit, HMAC deploy webhook
  verified in the daemon, signed installer). It's **multi-tenant**; we keep its security, drop its
  tenancy. Its stack is OpenLiteSpeed + mail/PowerDNS/Mongo; broader than our v1. Beta-only, no
  upgrade path, no user docs.
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
