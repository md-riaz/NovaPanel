# Phase 9 — Optional modules (after the core is boring)

## Goal
Add capability **only after the core is stable and demand is proven**, as first-party compiled-in
modules (D4) that never widen the trust boundary. This phase is mostly **discipline and guardrails**:
nothing here starts until Phases 0–8 are solid in production.

## Depends on
A stable, boring core (Phases 0–8), backups + restore (Phase 6) **before** any destructive module,
and real demand. Reads: `00-foundations.md` (all), `vpspanel-plan.md` §1 non-goals, §4 D4.

## Deliverables (each is its own gated sub-project, built only on demand)
1. **OpenLiteSpeed WordPress-performance mode** — OLS **private, bound to 127.0.0.1 behind Nginx**;
   rewrite rules only, **not** full `.htaccess` emulation. A per-site opt-in.
2. **Local mail** (Postfix/Dovecot/Rspamd) — only if demand proves it; DKIM, per-hour sending cap.
   Mail is a large surface; it stays a module, never core.
3. **Authoritative DNS** (PowerDNS) — requires an **independent secondary NS before "healthy"**;
   reject same-IP `ns1`/`ns2`. (External DNS via Cloudflare in Phase 7 is the default; this is for
   users who want the panel to be authoritative.)
4. **Pure-FTPd** — explicit TLS only; off by default; per-site.
5. **Docker / Compose** — for self-hosted apps; isolated; never required by the core.

## Blueprint
- Each module is a package under `internal/modules/<name>/` exposing a `Module` interface:
  `Install()/Enable(site)/Disable(site)/Status()/Uninstall()`. `module.install` only
  **enables/configures a shipped module** — it never fetches or executes downloaded code (D4).
- A module registers its verbs in the **same** registry (F13) with the same validation/audit/job
  rules; a disabled module's verbs return `code:"unsupported"`.
- **Nginx remains the permanent public edge.** It owns ports 80 and 443, TLS, HTTP/2 and HTTP/3,
  ACME HTTP-01, redirects, access logs, WebSocket and reverse-proxy routes for every site type.
  OLS never owns a public port and does not replace Nginx.
- **OLS is a private, per-site WordPress backend.** The OLS listener binds only to `127.0.0.1` or a
  private Unix socket. Nginx proxies only opted-in WordPress sites to it. Static sites, ordinary PHP,
  Node, Python, Go and container applications stay on their existing Nginx routes.
- **Enablement is transactional per site, not a stack switch.** Install OLS beside the running site,
  render and validate its private vhost, record the site's HTTP status, redirect and certificate,
  atomically change that site's Nginx upstream, then re-probe it. On a worse result, restore the prior
  Nginx-to-PHP-FPM route automatically. A dry run performs installation, rendering, validation and the
  baseline probe without changing the upstream.
- **Render from structured state only.** Never edit generated Nginx or OLS vhosts with marker-comment
  heuristics. OLS supports declared rewrite rules, not arbitrary `.htaccess` directives.

### Resolved interface (B29, C22)
```go
type Module interface {
    Name() string
    Scope() ModuleScope            // Global or PerSite
    Install(ctx, *Agent) error
    Enable(ctx, *Agent, target string) error   // target = "" for Global modules, site name for PerSite
    Disable(ctx, *Agent, target string) error
    Status(ctx, *Agent) (any, error)
    Uninstall(ctx, *Agent) error
}
```
- A module's verbs carry `Verb.Module = "<name>"` (F13); the dispatcher returns **`unsupported`** when
  `module_state.enabled = 0` — the handler does not check per-call. `module.*` state lives in
  `module_state` (02-data-model).
- **Global vs PerSite** is explicit (`Scope()`), so `Enable("")` is meaningful for mail/PowerDNS and
  `Enable("<site>")` for OLS/FTP. This phase builds **no module**; it fixes the interface each future
  module sub-project implements. Each module is scheduled individually; "demand proven / core boring"
  is the operator's call, recorded when a module sub-project is opened — not a measurable gate here.

## External refs (gathered by the head, per module when it is scheduled)
- OLS behind nginx as a private upstream; rewrite-rule translation scope (explicitly not `.htaccess`).
- PowerDNS secondary-NS requirement and DNSSEC basics (gathered when this module is scheduled).
- Rspamd/DKIM + sending-cap patterns (BHPanel ships a per-hour cap and a capped `sendmail` shim —
  reference when mail is scheduled).

## Verification (the gate — per module)
```
1.  The module is OFF by default; a fresh install never enables it; its verbs return "unsupported"
      until explicitly enabled.
2.  Enabling/disabling is idempotent and audited; disabling fully reverts config + reloads.
3.  OLS (if built): force a failure while enabling it for one site → that site's prior Nginx-to-PHP-FPM
      route is restored and every other site remains untouched. A pre-change config backup exists.
4.  Mail (if built): outbound is capped; DKIM signs; no open relay (test a relay attempt → refused).
5.  PowerDNS (if built): refuses same-IP ns1/ns2; a zone is not "healthy" without an independent
      secondary.
6.  No module widened the trust boundary: the no-shell / no-unregistered-verb / argv-only arch tests
      still pass with the module compiled in.
```

## Definition of Done (per module)
- [ ] Off by default; demand-gated; its verbs are `unsupported` until enabled.
- [ ] Enable/disable idempotent, audited, fully reversible.
- [ ] If OLS is enabled for a site, it remains private behind Nginx and rollback restores only that
  site's Nginx-to-PHP-FPM route.
- [ ] All global invariants still machine-enforced with the module built in.

## Guardrails (this phase)
- **Do not start any module before the core is boring and backups/restore are proven.** A destructive
  module without restore is a foot-gun.
- **First-party, compiled-in only (D4).** No third-party executable plugins; `module.install` never
  fetches or runs downloaded code.
- **Nginx stays the only public server.** OLS, when built, binds only to loopback or a private Unix
  socket behind Nginx. Do not add a global `stack.switch` or let OLS bind public ports.
- Never edit a live vhost with marker-comment heuristics. Render a complete replacement from structured
  state, validate it, atomically install it, probe it, and restore the last-known-good config on failure.
- Each module is a separate gated sub-project. This phase does not authorize building all modules.
