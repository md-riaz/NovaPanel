# VPSPanel — Consolidated Plan v2 (source-audited, single-VPS)

The authoritative plan. It supersedes the original technical plan and folds in the panel-source
audit. Companion docs: `vpspanel-architecture-review.md` (structural review) and
`vpspanel-plan-amendments.md` (audit evidence, corrections, settled decisions). Where they differ,
**this document wins**.

> **North star:** a **simple-yet-powerful control panel for one VPS**, strong on usability, no fuss.
> Opinionated defaults over endless knobs. Every powerful action is safe by default.

---

## 1. Product vision & boundary

**Promise:** run websites, WordPress, PHP/Node apps, databases, domains, SSL, backups, and basic
server ops from a friendly panel — without living in SSH — on **one VPS you own**.

**Users:** a single VPS owner, or a small team sharing one server (a freelancer, an agency dev, a
small business, a self-hoster). One owner, optional teammates. That's it.

### In scope (v1)
```
Sites: static, PHP, WordPress, Laravel-style — each isolated
Domains, subdomains, redirects, Let's Encrypt SSL
MariaDB databases + users
Files (manager + safe editor), Git deploy, cron, log viewer
SFTP + SSH public-key management
Cloudflare DNS integration
Restic backups (local / SFTP / S3) + granular restore
UFW firewall presets, 2FA, audit log
Jobs with progress/logs/retries
```

### Explicitly NOT in this product (hard non-goals)
```
Multi-tenancy, reseller tiers, customer accounts-as-a-product   ← the BHPanel model we DROP
Billing, invoices, plans/packages, WHMCS
Per-customer branding, customer SSO, reseller hierarchies
Docker as the default runtime for normal sites
Caddy / Apache / full .htaccess compatibility
Local mail server, authoritative DNS — by default
Browser terminal, multi-OS, remote multi-server — in v1
Third-party executable plugins
```

> **Why this matters (audit finding):** BHPanel — the closest real analog, whose release we read —
> is **multi-tenant** (admin/reseller/user tiers, customer packages a reseller sells). We adopt its
> *security architecture* (below) and **deliberately drop its tenancy**. A single-VPS panel needs
> none of that machinery, and leaving it out is most of what "no fuss" means. One owner model →
> roles are minimal: **owner, admin, read-only**. No tiers, no packages, no per-customer isolation
> *as a product* (per-site isolation stays, for safety — see §6).

### Design principles (the "no fuss" contract)
```
Opinionated defaults; few knobs. Structured settings, never raw config editing (v1).
Every mutation is a job with an audit event and a typed result.
Powerful actions are safe by default: confirmations, dry-runs, timed auto-revert.
One language (Go), static binaries, no Node runtime in production.
A failed apply always leaves the prior working state online.
Error messages name the fix, not just the failure.
```

---

## 2. Architecture (verified against BHPanel's real release)

Two processes, split by privilege — the single most important decision, and **confirmed sound** by
reading BHPanel 0.2.88's shipped code (see `vpspanel-plan-amendments.md` §0):

```
Browser
  → Nginx (public TLS, :80/:443)
  → panel-web   — Go, runs as unprivileged `vpspanel`; UI, API, SQLite, job queue
  → Unix socket /run/vpspanel/agent.sock  (0660 root:vpspanel)
  → panel-agent — Go, runs as root; the ONLY root component; typed verbs only
  → Nginx, PHP-FPM, MariaDB, systemd, UFW, Restic, Git
```

### The agent RPC contract (modeled on BHPanel's proven `Daemon.php` pattern)
- **Transport:** newline-delimited JSON over the Unix socket. Request
  `{id, verb, args(object), actor, session, deadline}` → reply `{ok, data, error, code}` where
  `code` is a **stable string** the UI branches on.
- **Access is filesystem-gated:** socket `0660 root:vpspanel`; `panel-web` is in the `vpspanel`
  group. The web tier does **not** authenticate to the agent — the kernel already did.
- **Authorization lives in the agent, not the UI** (resolves the trust-model gap): the agent issues
  a **session token at sign-in** and acts on its own record of who signed in; the web tier only
  presents the token. Role checks happen agent-side.
- **No command verb, ever.** Args are JSON, never interpolated; the agent executes **argv arrays,
  never `sh -c`**. "If the web tier is ever exploited, the attacker inherits exactly this: the
  ability to call a fixed set of typed verbs." `internal/rpc/messages.go` is the **single canonical
  registry**; an architecture test fails the build if any agent handler is reachable without a
  registered verb.
- **Forbidden permanently:** `shell.execute`, run-command, write-arbitrary-root-path.

**Tech stack:** Go; `net/http` + `chi`; `html/template`; Tailwind (built to static CSS); HTMX;
Alpine.js for small local UI state only; SQLite (WAL); systemd; Nginx; PHP-FPM; MariaDB; `lego` or
`acme.sh` for ACME; Restic; UFW; Git CLI with fixed argv; OpenAPI 3.1.

---

## 3. Security model & invariants (each maps to a real exploited failure)

Put these in `docs/architecture/threat-model.md`; back each with a test. (Sources: CyberPanel 2024
pre-auth RCE chain → PSAUX ransomware, 22k boxes, CISA KEV; aaPanel command-injection CVEs; Hestia
lockouts; CloudPanel/Froxlor generator CVEs.)

1. **Validate at the sink, on every HTTP verb.** CyberPanel's filter ran on POST only; a PUT
   bypassed it. Never put a security check in method-conditional middleware.
2. **Default-deny authentication, centrally enforced**, with an explicit public-route allowlist and
   a test that fails the build if any route lacks an auth decision. (The fatal CyberPanel route had
   no auth check at all.)
3. **Agent executes argv arrays, never `sh -c`;** no shell-string construction near user input.
   Typed args; path/identifier validation in the agent.
4. **Treat site/user names as attacker-controlled** in the privileged config generator (CloudPanel &
   Froxlor CVEs both hit exactly this component).
5. **Firewall: dry-run → apply → read back → verify SSH allow rule present → auto-revert if not
   confirmed within N minutes.** A never-removable SSH rule. (Hestia lockouts.)
6. **Nginx apply loop (net-new engineering, not a Caddy port):** `render → nginx -t → write-temp +
   atomic rename → reload → active HTTPS health probe → restore prior good config + reload on
   failure`. Skip reload when the rendered config hash is unchanged. (Caddy only auto-reverts
   *load-time* failures; the runtime health-check-and-revert is ours to build.)
7. **Signed releases + explicit security advisories.** Verify the release signature before applying
   any update; every security fix gets a published advisory. Silent patching is a named anti-pattern
   (CyberPanel shipped its fix silently → operators stayed exposed).
8. **Append-only audit log** (e.g. `chattr +a`, root-owned), written by the **agent** as the source
   of truth for privileged actions — not forgeable by a compromised web tier.
9. **Assume the HTTP tier will eventually have an RCE** and make it non-catastrophic. This is the
   whole point of the agent boundary.
10. **Secrets sealed by the agent.** Provider tokens / DB creds are encrypted with a key the
    **agent** holds (root-only), so a `panel-web` compromise leaks ciphertext, not the key.

---

## 4. Settled decisions

- **D1 — Webhooks: HMAC-SHA256 + replay protection.** Git-provider/external webhooks carry an
  HMAC-SHA256 signature over the raw body + timestamp/nonce replay window. Simple deploy-trigger API
  uses a scoped bearer token. **Verify the signature in the agent** (secret root-only); the
  internet-facing route holds no secret and returns a **uniform refusal** (no id enumeration).
  Matches BHPanel's real `X-Hub-Signature-256` prior art; exceeds Coolify (which is bearer-only).
- **D2 — Updates: notify + one-click apply** (never silent auto-apply). Prominent, persistent
  in-panel advisory + one-click apply; signature verified before apply; explicit advisories.
- **D3 — Per-site isolation: Unix socket + `pm=ondemand` + Nginx-direct static.** See §6.
- **D4 — Modules: first-party, compiled-in only.** `module.install` only enables/configures a
  shipped module — never fetches or executes downloaded code. No third-party executable plugins.

---

## 5. Control-plane data

One SQLite DB at `/var/lib/vpspanel/panel.db`, **metadata only**.
```sql
PRAGMA journal_mode = WAL;  PRAGMA foreign_keys = ON;  PRAGMA busy_timeout = 5000;
```
Stores: users, sessions, MFA, API tokens, sites, domains, DB metadata, jobs + events, backup
catalog, integration metadata, **agent-sealed secrets**, audit events, settings, module state.
Never stores: website files, MariaDB table data, backup archives, unbounded logs.

> **Correction (ISPConfig audit):** a server-class DB (Postgres) is triggered by **SQLite
> write-concurrency**, not by "multi-server" — and multi-node is a separate control-DB + node-agent
> concern, out of scope here. Do not cite ISPConfig to justify Postgres. SQLite/WAL + short
> transactions + the durable job queue is right for one VPS.

---

## 6. Per-site isolation (D3, concrete)

The isolation primitive — kept for **safety and stability** (one site can't read or take down
another), *not* as a tenancy product:
- **One non-login Unix user per site** (`-s /usr/sbin/nologin`; no SSH unless SFTP is explicitly
  enabled for that site).
- **One PHP-FPM pool per site over a Unix socket.** Socket `listen.owner`/`listen.group` = the
  **Nginx** user, `listen.mode = 0660`; the FPM **worker** runs as the **site user**. *(Owning the
  socket as the site user is the #1 cause of 502 "permission denied" — assert this in the renderer
  test.)*
- **`pm = ondemand`** default (idle sites cost ~nothing); per-site override to `dynamic`/`static`.
- **Static served by Nginx directly**: grant the Nginx user group-read + traverse on the docroot.
  (Honest form of "no shared www-data *write*".)
- **Density guidance, not a promise:** every pool keeps a resident FPM master (~2–5 MB). Publish a
  per-VPS site-count guideline from (RAM − masters − system) ÷ (peak workers × ~30–80 MB).

> **Simplify vs BHPanel:** BHPanel adds per-account **cgroup slices + kernel disk/inode quotas**
> because it sells accounts. For a single-owner VPS this is optional polish, not v1 — offer an
> optional per-site cgroup cap later for stability, but don't make quotas a day-one requirement.
> (Pending the deeper BHPanel mining audit; this section may gain a short "optional hardening" note.)

---

## 7. Implementation phases (corrected & de-duplicated)

Delivery order: **Foundation → auth/UI → Nginx/static → TLS → PHP/MariaDB → WordPress →
files/Git/cron/logs → backups → firewall/SSH → DNS → integrations/API → optional modules.**

- **Phase 0 — foundation:** Go workspace (two binaries); trust-boundary + threat-model docs (incl.
  invariants §3); **canonical typed RPC registry** + codec + tests; **CI + supply-chain gates**
  (`go vet`, staticcheck, govulncheck, `systemd-analyze verify`, the "no root web process" and "no
  unregistered verb" architecture tests) — *this repo has no CI today; it is a Phase 0 deliverable.*
- **Phase 1 — state & plumbing:** SQLite + migrations; durable jobs (with **compensation/rollback**
  for multi-resource jobs); agent-written append-only audit; systemd packaging. **Walking skeleton:**
  one trivial typed verb (`service.status`) flowing browser→web→socket→agent→reply end-to-end,
  before any feature rides the channel.
- **Phase 2 — auth & UI shell:** Argon2id passwords; secure sessions (idle+absolute timeout,
  revoke); **TOTP MFA** (secret sealed by agent; required for firewall/restore/security actions);
  HTMX UI shell with uniform CSRF; read-only dashboard metrics. **Minimal roles: owner / admin /
  read-only** — no tiers.
- **Phase 3 — Nginx sites, domains, TLS:** site+domain model (`stack_driver='nginx'`); structured
  Nginx renderer with the §3.6 apply-and-revert loop; static-site workflow; redirects/subdomains/
  maintenance (structured fields, no raw directives); ACME via jobs (HTTP-01 first; **wildcard needs
  DNS-01, gated behind Phase 8 DNS**); force-HTTPS only after a valid cert.
- **Phase 4 — PHP, WordPress, MariaDB:** one supported PHP version first; per-site user + FPM pool
  (§6); MariaDB bound to localhost, admin cred stored outside panel SQLite; DB/user lifecycle
  (generated names, least-privilege grants, show-once passwords); WordPress install (checksum-
  verified, wp-config protected). **No destructive WordPress ops (update/clone/staging) before
  backups/restore land in Phase 6.**
- **Phase 5 — daily controls:** scoped file API (traversal/symlink/archive-escape tests) + safe
  editor; Git deploy (fixed argv, allowed URL forms, project deploy key, pinned SHA, history); cron
  (per-site files or systemd timers, no raw root cron); bounded log viewer; SSH **public-key only**
  management.
- **Phase 6 — backups, firewall, status:** Restic targets (local/SFTP/S3, fixed argv); per-site
  backup + verify-by-reading-back; restore (file → DB → full site, each with typed confirmation +
  pre-restore safety snapshot); **UFW presets with dry-run + timed auto-revert + never-removable SSH
  rule** (§3.5); **panel self-backup** (`panel.db` + agent config) via `vpspanelctl backup-config`;
  status + manual security updates per **D2**.
- **Phase 7 — DNS (external first):** one `dns.Provider` interface + **Cloudflare** implementation
  (A/AAAA/CNAME/TXT/MX CRUD, proxy toggle for HTTP records only — never MX/SPF/DKIM/DMARC, DNS
  verification, cache purge). *(De-duplicated: the original plan specified Cloudflare twice — one
  task only.)*
- **Phase 8 — integrations & API:** OpenAPI + scoped API tokens (no token scope maps to shell/raw
  agent). **MFA vs tokens:** sensitive scopes (restore/firewall) are **excluded from API tokens** or
  require a separate automation grant (a token can't present TOTP). Deployment API + webhooks per
  **D1**; GitHub OAuth/deploy-key/HMAC-webhook push-to-deploy.
- **Phase 9 — optional modules, after core is boring (D4, first-party compiled-in):** OpenLiteSpeed
  WordPress-performance mode (private, 127.0.0.1 behind Nginx; rewrite rules only, **not** full
  `.htaccess`); then only if demand proves it — local mail (Postfix/Dovecot/Rspamd), authoritative
  DNS (**PowerDNS**, requires an independent secondary NS before "healthy"; reject same-IP ns1/ns2),
  optional Pure-FTPd (explicit TLS only), Docker/Compose for self-hosted apps. **Switching web
  stacks must be transactional with backup + rollback** (aaPanel's switch corrupts vhosts via
  `#`-marker heuristics — don't copy that).

---

## 8. Test & release strategy

- **Unit:** input/hostname/path/URL validation, config rendering, migrations, job state machine +
  compensation, secret redaction, CSRF/auth/MFA, RPC decode + authorization, the no-unregistered-
  verb architecture test.
- **Integration:** Nginx renders + `nginx -t` passes + serves by Host header; PHP-FPM socket serves;
  one DB user can't read another DB; ACME staging; Restic backup+restore into a fixture; UFW change
  keeps SSH; Git deploy pins SHA.
- **Disposable-VM acceptance** on fresh Ubuntu 24.04 (never the dev host): install → static+PHP+WP
  sites with HTTPS → DB user → backup+restore one file and one DB → SSH key → cron → safe firewall
  preset → Git deploy → destroy.
- **Release:** Ubuntu 24.04 v1; fresh-VPS check or conflict report; **signed manifest + checksum
  verified before executing anything** (BHPanel's installer is the reference — signature-checked
  ed25519 manifest, sha256-pinned tarball, every entry validated before unpack); never change
  SSH/firewall during install without preserving access; `vpspanelctl status|doctor|logs|
  backup-config|repair-nginx`; **idempotent, rerun-safe installer** (every completed step detectable
  and safe to repeat).

---

## 9. Open blockers — decide before the first commit

1. **Product name / `go.mod` module path** — placeholder today; load-bearing for every import and the
   `vpspanelctl` binary name.
2. **License** — this repo is **MIT**; the product is **proprietary**. Prior MIT commits stay MIT;
   relicensing is a deliberate act before the first proprietary commit enters public history.
3. **PHP NovaPanel cutover** — greenfield-in-this-repo: move the existing PHP under `legacy/` (or
   freeze it on a branch), keep both buildable during transition, define an explicit cutover. Don't
   interleave Go and PHP at repo root.

---

## 10. Acceptance criteria (first public beta)
```
Fresh Ubuntu 24.04 install completes and survives reboot.
panel-web runs without root; panel-agent is the only root panel service; Nginx is the only public server.
Create static/PHP/WordPress sites with HTTPS; create + rotate a MariaDB credential.
File manager stays within project root; schedule a project cron job.
Back up and restore one site and one DB; add SSH keys and a UFW preset without lockout.
Every mutating action produces an audit event + job result.
A failed config/deploy leaves the prior working site online.
```
