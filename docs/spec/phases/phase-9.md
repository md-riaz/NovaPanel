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
- **Web-stack switching is server-wide, transactional, migration-free (verified against BHPanel's real
  `stack.switch`).** The stack is one global choice (e.g. `nginx` ↔ `ols`); **no data migration** —
  PHP-FPM pools, certificates, DB users and accounts are stack-independent, so only the web-server
  config is **re-rendered from the DB** (the derived-artifact model). aaPanel corrupts vhosts by
  editing them with `#`-marker heuristics — **never do that**; always regenerate from the DB. The
  `stack.switch` job (owner, MFA, effectful, `objectKey system`) follows BHPanel's sequence:
  1. **Install the new stack beside the running one** — visitors unaffected.
  2. **Render + validate** the new stack's config for every site; if validation is refused, **stop —
     nothing has changed**.
  3. **Baseline:** record how every site answers now (status, redirect, certificate), site by site.
  4. **Swap** — a brief all-sites outage of a few seconds (honest; not zero-downtime).
  5. **Re-probe** every site; **if any answers worse, switch back automatically** and report which.
  A **dry-run ("only check, don't switch")** runs steps 1–3. `stack.status` reports the current stack,
  versions, and whether a switch is in progress. (Note BHPanel's lesson: there is no "Nginx-only" —
  Nginx silently ignores `.htaccess`; our v1 is Nginx-only *because* we render rewrite rules from
  structured fields rather than honoring `.htaccess`, so this is a module concern only if an
  Apache-compatible stack is ever offered.)

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
3.  Web-stack switch: force a failure mid-switch → the prior stack's config is restored and the site
      stays online (no half-switched vhost). A pre-switch backup exists.
4.  Mail (if built): outbound is capped; DKIM signs; no open relay (test a relay attempt → refused).
5.  PowerDNS (if built): refuses same-IP ns1/ns2; a zone is not "healthy" without an independent
      secondary.
6.  No module widened the trust boundary: the no-shell / no-unregistered-verb / argv-only arch tests
      still pass with the module compiled in.
```

## Definition of Done (per module)
- [ ] Off by default; demand-gated; its verbs are `unsupported` until enabled.
- [ ] Enable/disable idempotent, audited, fully reversible.
- [ ] If it replaces a web stack, the switch is transactional with backup + health-probe + rollback.
- [ ] All global invariants still machine-enforced with the module built in.

## Guardrails (this phase)
- **Do not start any module before the core is boring and backups/restore are proven.** A destructive
  module without restore is a foot-gun.
- **First-party, compiled-in only (D4).** No third-party executable plugins; `module.install` never
  fetches or runs downloaded code.
- **No stack switch without backup + rollback.** Never edit a live vhost with marker-comment
  heuristics.
- Each module is a separate gated sub-project — this phase does not authorize building all of them;
  it sets the rules they are each built under.
