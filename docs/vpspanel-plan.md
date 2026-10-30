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

**Concretely dropped (BHPanel's tenancy surface — ~90 of its 267 verbs):** customer SSO
(billing-system sign-in links), DNS-cluster connector (`dnscluster.*`), collaborator invites/ACLs
(`collab.*`), per-customer/reseller branding, the billing REST API and WHMCS/Blesta/FossBilling
integrations, admin/reseller/user **tiers + impersonation** ("log in as"), accounts-as-product
(`account.create/suspend/setPackage/export`), **packages/plans** (`package.*`), competitor-import
(`import.*`), the paid-license nag (`license.*`), and per-account quota/limits *as tenancy
boundaries*. Also simplified away: multiple panel "sides"/ports + per-port cookies → **one port, one
cookie**; three-panel role-gating → **one panel**.

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
  → panel-web   — Go, runs as unprivileged `vpspanel`; UI + API only, STATELESS (no DB)
  → Unix socket /run/vpspanel/agent.sock  (0660 root:vpspanel + SO_PEERCRED uid-pin)
  → panel-agent — Go, runs as root; the ONLY root component; owns ALL state (SQLite) + job worker; typed verbs only
  → Nginx, PHP-FPM, MariaDB, systemd, UFW, Restic, Git
```

### The agent RPC contract (modeled on BHPanel's proven `Daemon.php` pattern)
- **Transport:** newline-delimited JSON over the Unix socket. Request
  `{id, verb, args(object), actor, session, deadline}` → reply `{ok, data, error, code}` where
  `code` is a **stable string** the UI branches on.
- **Access is filesystem-gated + peer-pinned (D5):** socket `0660 root:vpspanel`; `panel-web` is in
  the `vpspanel` group. On every accepted connection the agent reads **SO_PEERCRED** and rejects any
  peer whose uid ≠ the `panel-web` uid — group membership alone is not enough. The web tier does
  **not** authenticate to the agent; the kernel already did.
- **Authorization lives in the agent, not the UI:** the agent issues a **session token at sign-in**
  and acts on its own record of who signed in; the web tier only presents the token. `actor` is an
  **audit label only, never an authz input** (the confused-deputy fix; Cockpit/polkit precedent).
  Role checks happen agent-side.
- **No command verb, ever.** Args are JSON, never interpolated; the agent executes **argv arrays,
  never `sh -c`**. "If the web tier is ever exploited, the attacker inherits exactly this: the
  ability to call a fixed set of typed verbs." `internal/rpc/messages.go` is the **single canonical
  registry**; an architecture test fails the build if any agent handler is reachable without a
  registered verb.
- **Forbidden permanently:** `shell.execute`, run-command, write-arbitrary-root-path.

### Job & state model (D6 / D7 / D9 — decided)
- **The agent owns all persistent state (D9).** One **root-only SQLite** at
  `/var/lib/vpspanel/panel.db` (`0700 root`); `panel-web` has **no database** and is a stateless
  renderer — every read and write is a typed verb. A web compromise then leaks nothing at rest, has
  no DB to poison, and never sits near the sealed secrets. (BHPanel ships exactly this; `/var/lib`
  is `0700 root`, the web user can't read it.)
- **Durable job queue, single in-process worker (D6).** A verb that can exceed ~2 s writes a durable
  `jobs` row and returns `202 {job}` immediately; **one worker goroutine** drains the queue,
  **serialized per object** (two verbs never interleave on one site — systemd-style conflict
  avoidance for free). The web tier polls `job.status`; fast reads stay synchronous. The job row *is*
  the durability across an agent restart. (HestiaCP's synchronous `v-*` model is the cautionary tale
  this avoids; ISPConfig/cPanel/Coolify all run slow work off a durable store.)
- **Idempotent "ensure-state" verbs + roll-forward recovery (D7).** Every verb means "make reality
  match this row," not "do this action" (check-before-create; temp-file + atomic rename). On worker
  start, any job stuck `running` **re-runs from the top** — safe because steps are idempotent; no
  compensation for local resources (explicit compensation only for external/billed steps: ACME,
  provider DNS). The crash-safety test is Ansible's: **run the verb twice; the second run changes
  nothing.** Serialize with a **liveness-checked lock**, never a bare lockfile (ISPConfig's
  `.ispconfig_lock` footgun: a segfault leaves it behind and wedges every later run).
- **One `reconcile` verb.** Re-renders all config from the DB (the desired state) and reloads
  services; heals both drift (hand-edited nginx) and half-created state. Config files are **derived
  artifacts**, never parsed back into the DB. (ISPConfig Resync / Plesk `repair` precedent.)

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
7. **Signed releases + explicit security advisories.** The update manifest is **ed25519-signed**; the
   agent **verifies the signature before it parses the manifest**, refuses any version **older than
   the installed one** (anti-rollback / no-downgrade), and checks each file's sha256 + size against
   the signed manifest. **Unpack defensively** (reject absolute paths, `..`, non-regular entries,
   anything outside the top folder, pathological entry counts — tar-slip). Every security fix gets a
   published advisory; a `security_fix` marker flags the update as urgent. Silent patching is a named
   anti-pattern (CyberPanel shipped its fix silently → operators stayed exposed). *(Full beta/stable
   channels + approval-gated CI signing → Phase 4; BHPanel's `bhpanel-releases` is the verified
   reference — public key compiled into the agent, publish files-first-manifest-last, mirror
   root-owned + release dir `chattr +i`.)*
8. **Append-only audit log** (e.g. `chattr +a`, root-owned), written by the **agent** as the source
   of truth for privileged actions — not forgeable by a compromised web tier.
9. **Assume the HTTP tier will eventually have an RCE** and make it non-catastrophic. This is the
   whole point of the agent boundary.
10. **Secrets sealed by the agent.** Provider tokens / DB creds are encrypted with a key the
    **agent** holds (root-only), so a `panel-web` compromise leaks ciphertext, not the key.

### Web-tier hardening checklist (verified in BHPanel's shipped code — adopt as our baseline)
```
[ ] Exactly ONE web-reachable file; everything else outside the document root.
[ ] CSRF checked in ONE central place for every POST (hash_equals compare).
[ ] Sec-Fetch-Site check on all state-changing methods (same-origin/none only).
[ ] Strict CSP: default-src 'none', NO unsafe-inline, self-hosted assets, no CDN.
    (Widths/visuals come from CSS classes, never inline style= — CSP drops it.)
[ ] Full header set: HSTS, X-Frame-Options: DENY, nosniff, Referrer-Policy: no-referrer,
    Permissions-Policy off camera/mic/geo.
[ ] Sessions: cookies-only, strict-mode, HttpOnly, SameSite=Strict, Secure, __Host- prefix;
    regenerate id on EVERY privilege change; idle (30m) + absolute (12h) timeouts.
[ ] Session validity re-checked against the agent EVERY request (session.whoami) — a
    removed/suspended/role-changed login dies on the next click, not at idle-timeout.
[ ] Session key never stored whole (server-side half XOR a browser-cookie mask).
[ ] Unconditional output escaping — there is no "trusted" string.
[ ] Open-redirect guard incl. the backslash→// normalization trick.
[ ] Argon2id-when-available / bcrypt fallback + rehash-on-login; length-first policy (>=12).
[ ] Lockout per-username AND per-IP from REMOTE_ADDR only — never X-Forwarded-For unless a
    trusted-proxy list is configured (spoofable header = lockout bypass / CDN foot-gun).
[ ] TOTP: hash_equals, single-use (remember matched step), recovery codes at enrolment,
    QR rendered locally as inline SVG (the 2FA secret never leaves the box).
[ ] Audit log append-only via chattr +a; records successes, refusals AND panics, with the
    real peer uid (SO_PEERCRED); logrotate by RENAME not copytruncate; web tier can't read it.
[ ] First-run setup gated by "no user exists yet" AND a one-time installer-printed token held
    in root-only agent state (so first-to-the-URL can't claim the panel).
[ ] Secrets shown ONCE in a dedicated card — never in a flash, URL, session, or the audit log.
    Private keys handled entirely in the web tier, never sent to the agent.
[ ] Token-click (email confirm, password reset) renders a confirm FORM (GET→POST), never acts
    on the GET — SameSite=Strict cookies don't ride a cross-site redirect, and a GET must not mutate.
```

### Usability patterns worth stealing (the "no fuss" that users feel)
```
- One-verb site creation: vhost + PHP pool + DB + cert bootstrap in a single agent call.
- Opinionated safe defaults, honestly named: first admin is NOT "admin"; show the exact fix in
  every error ("agent not running" vs "web user not in the vpspanel group"); upload size in words.
- Show-once secret cards for generated DB/API passwords and keys.
- A self-test that tests ENFORCEMENT (actually hit the limit) not configuration (read a number back).
- Paged lists (20/page) that degrade gracefully when the agent is down (dashes, not a broken page).
```

---

## 4. Settled decisions

- **D1 — Webhooks: HMAC-SHA256 + replay protection.** Git-provider/external webhooks carry an
  HMAC-SHA256 signature over the raw body + timestamp/nonce replay window. Simple deploy-trigger API
  uses a scoped bearer token. **Verify the signature in the agent** (secret root-only); the
  internet-facing route holds no secret and returns a **uniform refusal** (no id enumeration).
  Matches BHPanel's real `X-Hub-Signature-256` prior art; exceeds Coolify (which is bearer-only).
- **D2 — Updates: notify + one-click apply** (never silent auto-apply). Prominent, persistent
  in-panel advisory + one-click apply. **Mechanism (lighter, decided):** ed25519-signed manifest,
  public key compiled into the agent, **signature verified before the manifest is parsed**,
  **anti-rollback** (refuse a version older than installed), sha256 + size per file, tar-slip-hardened
  unpack; a `security_fix` marker flags an update urgent. Beta/stable channels + an approval-gated CI
  signing pipeline are **deferred to Phase 4** (BHPanel's full `bhpanel-releases` model is the ready
  reference).
- **D3 — Per-site isolation: Unix socket + `pm=ondemand` + Nginx-direct static.** See §6.
- **D4 — Modules: first-party, compiled-in only.** `module.install` only enables/configures a
  shipped module — never fetches or executes downloaded code. No third-party executable plugins.
- **D5 — Caller trust: session token + SO_PEERCRED uid-pin.** `actor` is an audit label only;
  identity is the agent-minted session token; the agent rejects any socket peer whose uid ≠
  `panel-web`. (Cockpit/polkit precedent; the confused-deputy fix. BHPanel proves the token model;
  the uid-pin is one hardening it lacks.)
- **D6 — Job model: durable queue + single in-process worker goroutine, per-object serialized.**
  `202 {job}` + `job.status` for anything slow; sync only for reads. Job rows live in the agent's
  root-only SQLite, so the queue survives an agent restart.
- **D7 — Crash safety: idempotent ensure-state verbs + roll-forward.** Re-run a stuck job from the
  top (no compensation for local resources; compensation only for ACME/provider-DNS). One
  `reconcile` verb heals drift + half-created state. Liveness-checked lock, never a bare lockfile.
- **D8 — The config generator is an attacker-controlled sink.** Allowlist every domain/site/user
  name before it reaches a template or argv; **no empty/unquoted interpolation**; `nginx -t` on a
  temp render before atomic rename + reload; writes under site homes use `O_NOFOLLOW`; never
  `chown -R` a site-influenced path. (BHServe `root $root;` outage; Froxlor `/etc`-chown CVE; Hestia
  DNS-field injection.) **Build-enforced** (CI grep + hostile-name test).
- **D9 — The agent owns all persistent state** (root-only SQLite); `panel-web` is a stateless
  renderer with no DB. See §2 and §5.

---

## 5. Control-plane data

One SQLite DB at `/var/lib/vpspanel/panel.db` — **root-only (`0700`), owned by the `panel-agent`
(D9); `panel-web` has no database**. **Metadata only.**
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

> **Minimal viable isolation (decided, from the BHPanel mining audit):** BHPanel ties each account to
> a Linux user + **four kernel boundaries** (per-account cgroup slice, dual-cgroup caps for FPM *and*
> cron, XFS/ext4 disk+inode quota) *because it sells accounts*. With one owner there are no competing
> tenants to meter. **v1 keeps only:** the per-site non-login user + `0750` home + per-site FPM pool
> (defense-in-depth so a compromised site can't read another). **v1 drops:** kernel disk/inode quotas
> (and the quota-bootstrap/reboot dance and `quotaon`-ordering drop-in). PHP compatibility restrictions
> are not the boundary: `open_basedir` and command-function deny lists stay off by default and are an
> explicit hardened per-site option. **Optional later:** a single shared `vpspanel.slice` CPU/memory cap
> to keep the box responsive under load — one cap for the
> whole machine, not per-site metering.

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
  WordPress-performance mode, private on `127.0.0.1` or a Unix socket behind the permanent Nginx
  edge. It is per-site, supports structured rewrite rules only, and never binds public ports or
  replaces Nginx. Then only if demand proves it — local mail (Postfix/Dovecot/Rspamd), authoritative
  DNS (**PowerDNS**, requires an independent secondary NS before "healthy"; reject same-IP ns1/ns2),
  optional Pure-FTPd (explicit TLS only), Docker/Compose for self-hosted apps. OLS enablement must be
  transactional per site, with config backup, health probe and rollback to that site's PHP-FPM route.

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
  backup-config|repair-nginx`; **idempotent, rerun-safe installer** (state detected from disk, no
  stored-profile "second source of truth," every completed step safe to repeat).
  - **Sign the release archive with an out-of-band key** (do *not* ship only an in-tree
    `SHA256SUMS` — BHPanel's own installer documents that an in-tree checksum is a corruption check,
    **not** supply-chain authenticity, "anyone who can rewrite the binary can rewrite the checksum in
    the same edit." Authenticity must be a signature over the whole archive verified against a key
    that does not travel with it.). Build with `CGO_ENABLED=0`.
  - **Design the upgrade path up front.** BHPanel has **none** — every install is fresh, and its
    installer refuses a non-fresh server. Rerun-to-finish is a good *install* model but not an
    *upgrade* model; a product we iterate on needs migrations + a safe in-place update (ties to **D2**).
  - **Trusted-proxy note:** the panel reads `REMOTE_ADDR` and ignores `X-Forwarded-For` by default
    (correct — a spoofable header is a lockout bypass). Before placing it behind a CDN/LB, configure
    an explicit trusted-proxy list, or rate-limiting and audit IPs break. Document this.

---

## 9. Decisions & remaining blockers

1. **Product name — DECIDED: `NovaPanel`** (binary `novapanelctl`).
2. **`go.mod` module path — DECIDED: `novapanel`** (bare, no domain). Imports read
   `novapanel/internal/...`. Builds with zero setup; not remotely `go get`-able (irrelevant for a
   proprietary app built from its own repo). The path is the import prefix in every `.go` file.
3. **PHP cutover — DECIDED: freeze on a branch.** Preserve the current PHP at a branch/tag
   (`php-legacy`); the Go rewrite takes a clean repo root. PHP history stays in git. Executed as the
   first Phase-0 action on a fresh branch.
4. **License — DECIDED: proprietary.** Repo is currently **MIT**; relicensing (new `LICENSE`, source
   headers) is deliberate and prior MIT commits stay MIT. Consider making the repo private before the
   first proprietary Go commit. *Mechanics executed on request — not changed unilaterally.*
5. **Agent design — DECIDED (D5–D9).** Agent owns all state (root-only SQLite; stateless web);
   durable job queue + single in-process worker, per-object serialized; session token + SO_PEERCRED
   uid-pin; idempotent ensure-state verbs + roll-forward + one `reconcile` verb; config generator
   treated as an attacker-controlled, build-enforced sink. See §2 and §4.
6. **Updates — DECIDED (D2, lighter).** Signed manifest + verify-before-parse + anti-rollback +
   tar-slip-hardened unpack now; beta/stable channels and the approval-gated CI signing pipeline in
   Phase 4. See §3 invariant 7 and §4.

**No design decisions remain open — Phase 0 is unblocked.** (Phase 0 first action is still the PHP
freeze on `php-legacy`, then the Go scaffold; not started, awaiting the go-ahead.)

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

---

## Appendix A — Known traps to get right the first time

Real, dated failures BHPanel hit and documented in its shipped code — a free pre-mortem. Each is a
**silent** failure (it works until it doesn't, with no error), so each needs a test.

| Trap | What happens | Guard |
|---|---|---|
| seccomp kills `quotactl` silently | Daemon reads "quota unenforced" and refuses to provision on a fine server | If quotas are used, allowlist `quotactl`/`quotactl_fd` explicitly; A/B test with and without the filter |
| `quotaon` races the rw-remount at boot | Quota enforcement is a boot-time coin toss | systemd drop-in ordering `After=systemd-remount-fs` |
| `StartLimit*` placed in `[Service]` | systemd silently ignores it; a crash-loop never rate-limits | Keep `StartLimit*` in `[Unit]`; `systemd-analyze verify` in CI catches it |
| `RuntimeDirectory=` on the daemon | Daemon restart **deletes** every live PHP socket → every site 503s while units show "active" | Create `/run/vpspanel` via `tmpfiles.d` (survives unit stop); `RuntimeDirectoryPreserve=yes` |
| logrotate `copytruncate` on the audit log | Loses records in the truncate race — exactly the window an attacker wants | Rotate by **rename**, have the agent follow the rotation |
| CSP drops inline `style=` | Meters/bars render full or empty with no error | Visuals from CSS classes, never inline styles; test rendered output |
| `chattr +a` audit log vs `systemd LogsDirectory=` | systemd re-chowns the ExecDirectory on every start and fails against the append-only bit → daemon won't start | No `LogsDirectory=`; installer/agent create the log dir themselves |
| `X-Forwarded-For` trusted blindly | Lockout bypass / spoofed audit IPs | `REMOTE_ADDR` only unless a trusted-proxy list is set (see §8) |

> **Highest-value process to keep re-reviewing:** after the agent itself, the web PHP/FPM master (it
> binds a socket, then drops privilege). BHPanel starts it as root and contains it with caps +
> `ProtectSystem=strict`; prefer **systemd socket activation** to avoid root in the web path entirely
> if feasible. And the **deploy webhook is the only endpoint that answers a stranger** — audit it
> hardest (HMAC in the agent, uniform refusal, body-capped, 404 before reading).
