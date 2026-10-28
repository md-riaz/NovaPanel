# VPSPanel — Plan Amendments v1 (source-audited)

Read alongside the technical plan (`2026-10-28_043323-vps-panel-technical-plan.md`). **Where this
document conflicts with the plan, this document wins** — it corrects claims that were verified
false or misleading against the referenced panels' real behavior, and records four product/security
decisions the plan left open.

**Provenance of these changes:** each correction below was checked against primary sources
(vendor docs, public source repos, CVE databases) for BHPanel, aaPanel/BaoTa, Coolify, Caddy,
CyberPanel, HestiaCP, ISPConfig, CloudPanel, OpenPanel, Froxlor, Virtualmin. Companion:
`vpspanel-architecture-review.md` (structural review of the plan itself).

---

## 0. BHPanel — verified against the real release (0.2.88 beta)

**This section supersedes an earlier, wrong conclusion** (that BHPanel's architecture was
"unverifiable marketing" and that "readable PHP/systemd code" should be struck). We downloaded
BHPanel's actual signed release via its installer (`releases.bhpanel.net` → `beta/manifest.json`,
ed25519-signed → `files/bhpanel-0.2.88-linux-x86_64.tar.gz`, sha256 verified) and **read the shipped
artifact**. The plan's architecture claims are **substantiated by the real code**, not just
marketing. *(Caveat: analyze the pattern, don't copy — BHPanel is proprietary/licensed. The IPC
pattern below is generic and fine to reimplement; its source is not ours to lift.)*

What the release actually contains:
- **A root daemon + unprivileged PHP web UI — real and hardened.** `bin/bhpaneld` (a ~27 MB static,
  stripped ELF; Go-like) is "the BHPanel privileged daemon," the **only** component running as root,
  with heavy systemd sandboxing (seccomp `SystemCallFilter`, `CapabilityBoundingSet`,
  `MemoryDenyWriteExecute`). The web UI is **readable PHP** (`web/src/*.php`, `web/routes/*.php`) run
  as a PHP-FPM pool whose workers **drop from root to the panel user**. So "readable PHP" is **true**
  for the web tier; the daemon is a compiled binary (not source). `bin/bhpanel-sockbridge` bridges
  the socket.
- **Typed verbs over a Unix socket — exactly the plan's design.** `web/src/Daemon.php` is "the only
  way this application reaches the system": it writes **newline-delimited JSON**
  `{verb, args(object), actor, session}` to a Unix socket and reads `{ok, data, error, code}` (code =
  stable string). Access is **filesystem-gated**: socket `0660 root:bhpanel`, web process is in the
  `bhpanel` group — "nothing here authenticates to the daemon, because the kernel already did."
  **There is deliberately no method that takes a command**; args are JSON-encoded, never
  interpolated — "if a bug in this web application is ever exploited, the attacker inherits exactly
  this: the ability to call a fixed set of verbs." The README states **267 verbs across 48
  namespaces**, key=value args (`bhpanelctl account.create username=alice tier=user`).
- **The daemon enforces authorization (resolves our P0 "agent trust model" gap).** The daemon issues
  a **session key at sign-in** and "acts on its own record of who signed in"; the web "never says who
  anybody is — there is no scope to forge or forget." Tier (admin/reseller/user) is enforced **in the
  daemon, not the UI**. This is the concrete answer to the trust-model question the architecture
  review raised: hold session + authz in the agent; the web tier presents only the session key.
- **systemd-first, and per-account isolation stronger than our plan.** Full unit set
  (`bhpaneld.service`, `bhpanel-web.service`, timers for backup/certs/update/scan, `bhpanel.slice`).
  An account = a Linux user + **0750 home + kernel disk/inode quota (XFS project or ext4 user) + a
  per-account cgroup slice (`bhpanel-<acct>.slice`) + its own PHP-FPM master**, created by one verb.
  Limits are written to **two** cgroups (the PHP slice *and* `user-<uid>.slice` for cron/login). Our
  §Decision D3 (socket + ondemand + per-site user) is the right direction; BHPanel shows the
  cgroup-slice + quota layer we should plan for.
- **Webhook = HMAC-SHA256, secret behind the daemon** (see Decision D1). `web/routes/deploy_hook.php`
  is "the only path that answers a stranger": it reads the raw body, forwards GitHub's
  `X-Hub-Signature-256` to the daemon's `deploy.hook` verb, and the **daemon** verifies (secret in
  `/etc/bhpanel/sites`, 0700 root). Uniform `202` refusal for both bad-signature and unknown-id (no
  enumeration), body capped, POST-only, routed before CSRF/sign-in.

Context worth noting (not changes to our plan):
- BHPanel's public web stack is **OpenLiteSpeed**, not Nginx, and 0.2.88 already ships mail
  (Postfix/Dovecot/Rspamd + DKIM), **PowerDNS + DNSSEC**, MariaDB/PostgreSQL/MongoDB, a Node tier,
  WordPress install/update, CSF + ModSecurity — i.e. **broader than our v1**. It is derived from a
  "BHServe" engine.
- It is **early**: beta-only (no stable channel published), README admits **no upgrade path** (every
  install is fresh) and **no user docs** yet.
- BHPanel is itself built **Claude-Code / subagent-style** (ships references to `CLAUDE.md`,
  `docs/ARCHITECTURE.md`, `docs/DECISIONS.md` with "open ones block work," phase plans) — the **same
  methodology** as this plan. Good validation that the approach produces a real, shippable system.

**Net effect on §1 below:** the BHPanel row is corrected from "unverifiable" to "verified." The
design is genuinely well-precedented; the only honest trim is to say the *daemon* is a compiled
binary we can't read, while the web tier, systemd packaging, and installer are readable and were read.

---

## 1. Provenance corrections (the plan's "we took X from Y" claims)

| Plan claim | Verdict | What is actually true / the fix |
|---|---|---|
| Security model **modeled on BHPanel** — Unix socket, typed verbs, readable PHP, systemd, audit events | **Verified** (see §0) | Confirmed against the **real shipped release 0.2.88**: unprivileged PHP web UI + single hardened root daemon, newline-JSON typed verbs over a `0660 root:bhpanel` socket, daemon-enforced authz, systemd-first, append-only audit log. Accurate trims: the **daemon is a compiled binary** (web tier + packaging are readable PHP/shell, and were read); "no public VCS repo" ≠ "closed design." Keep signed-release + checksum (real) and the setup-token + forced-2FA pattern. |
| **aaPanel** = feature-catalog inspiration | **Confirmed, but reframe** | aaPanel (Python/Flask) is a **root monolith** with a stream of authenticated **command-injection → full-host RCE** CVEs (e.g. CVE-2026-101007/8/9/11, ~9.3 CVSS). It is the **cautionary tale**, cited as the argument *for* our privilege split — valid **only if** the agent takes typed/argv args (see §3). Its plugins run as root (see Decision D4). Its multi-webserver switch is fragile (vhost corruption, `#`-marker heuristics) — a warning for the deferred OLS mode. |
| "**HMAC webhook verification like Coolify**" | **False** | Coolify uses a **shared bearer token in a header**, not HMAC. See Decision D1. |
| "**Caddy does rollback if config fails**" → justification for our nginx health-check rollback | **Over-claimed** | Caddy auto-reverts **load-time** failures only (invalid config, port won't bind). It does **not** revert a config that loads cleanly but breaks at runtime. Our `reload → HTTPS health-check → revert` loop is **net-new engineering we must build**, not a port (see §3). |
| **CloudPanel** proves the **socket-permission** isolation mechanism | **Misleading** | CloudPanel (Symfony/PHP, UI on :8443) uses **loopback TCP** for FPM (`127.0.0.1:9740`, `listen.allowed_clients`), not Unix sockets. Cite it for **per-site-user + per-site-pool + site-owned docroot** (all confirmed). Our socket choice is valid and tighter — but justify the socket-perm detail from the generic FPM pattern, and own the `listen.owner` gotcha (§Decision D3). |
| **ISPConfig** justifies "**Postgres** for multi-server" | **Misleading** | ISPConfig scales to many nodes **while staying on MySQL** — shared control DB + per-node **pull agents** (app-level replication), not a DB-engine change. **Reframe:** the trigger for a server-class DB is **SQLite write-concurrency**, not multi-node; multi-node is a separate **control-DB + node-agent sync** problem, largely orthogonal to SQLite-vs-Postgres. Do not cite ISPConfig for the Postgres line. |
| "**No shared www-data access**" (per-site isolation) | **Too absolute** | True for **write**. Nginx still needs **read/traverse** on the docroot to serve static assets directly. Honest claim: "no shared www-data **write**; static reads via nginx group-membership/ACL." See Decision D3. |
| **OLS** = WordPress perf, **rewrite rules not full `.htaccess`** | **Confirmed** | OLS reads `.htaccess` but honors **only `mod_rewrite`**; `<Files>`/`Order`/`Deny`/`Options`/auth blocks are **silently ignored**. Scoping is accurate. Keep 127.0.0.1-behind-Nginx. |
| **Secondary NS required** before authoritative-DNS "healthy"; **external DNS first** | **Confirmed** | Same-IP ns1/ns2 is non-compliant; real redundancy needs a topologically separate secondary. Deferring BIND/PowerDNS + mail is justified (open-resolver/open-relay burden). |
| **Avoid containers by default** (vs OpenPanel) | **Confirmed** | OpenPanel per-user containers carry a **~0.87 GB shared-services floor + ~0.25 core + ~100 MB disk per user**; a plain user + FPM pool costs single-digit MB. Containers are the outlier; opt-in later is correct. |

---

## 2. Settled decisions

### D1 — Webhook security: **HMAC-SHA256 + replay protection**
Git-provider and external webhooks must carry an **HMAC-SHA256 signature over the raw body**, with
a **timestamp + nonce** checked against a replay window; reject on mismatch or stale timestamp. The
simple deploy-trigger API uses a **scoped bearer token**. This **exceeds** Coolify (do not cite
Coolify as HMAC precedent) and **matches BHPanel's real prior art** (§0), which verifies GitHub's
`X-Hub-Signature-256`. **Import BHPanel's refinement: verify the signature in the agent, not the web
tier** — the secret lives behind the root agent (root-only file), the internet-facing route forwards
raw body + signature and holds no secret, and refusals are **uniform** (don't distinguish
bad-signature from unknown-id, to prevent enumeration). *Patches Tasks 7.2, 7.4.*

### D2 — Security-patch delivery: **notify + one-click apply** (not silent auto-apply)
The panel **never auto-applies** updates. It surfaces a **prominent in-panel security advisory** and a
**one-click apply** action; the release **signature is verified before apply**; every security fix gets
an **explicit published advisory/changelog entry**. CyberPanel's *silent patch inside a routine
update* (22k boxes ransomed, CISA KEV) is the named anti-pattern.
**Residual risk the owner accepted:** a critical fix can sit unapplied on an inattentive operator's
box. Mitigate by making the advisory **persistent/escalating** (not dismissable-and-forgotten) and by
keeping the signed-release + advisory channel reliable. *Patches Task 6.5 + installer/update policy.*

### D3 — Per-site isolation: **Unix socket + `pm=ondemand` + Nginx-direct static**
- **One non-login Unix user per site** (`-s /usr/sbin/nologin`, no SSH unless SFTP explicitly enabled).
- **One PHP-FPM pool per site over a Unix socket.** Socket `listen.owner`/`listen.group` = the
  **Nginx** user, `listen.mode = 0660`; the FPM **worker** runs as the **site user** (separate
  `user`/`group`). *(Mixing these up — socket owned by the site user — is the #1 cause of 502
  "connect() permission denied". Call it out in the renderer test.)*
- **`pm = ondemand`** default (`pm.process_idle_timeout ≈ 10s`) so idle sites cost ~nothing; expose a
  per-site override to `dynamic`/`static` for high-traffic sites.
- **Static files served by Nginx directly**: grant the Nginx user **group read + path traverse** on
  the docroot. (This is the qualified, honest form of "no shared www-data *write*".)
- **Density reality to document:** every pool keeps a resident **FPM master (~2–5 MB)** even under
  `ondemand`. Density ≈ (RAM − masters floor − system) ÷ (peak concurrent workers × per-worker RSS,
  ~30–80 MB for WordPress). Publish a per-VPS **site-count guideline** from this (e.g. 2 GB VPS →
  ~20–40 modest WP sites), not an unbounded promise. *Patches Tasks 3.2, 3.3, 4.2.*

### D4 — Module privilege: **first-party, compiled-in only**
Optional modules (OLS, mail, PowerDNS, FTP, Docker) are **first-party, versioned, compiled into the
release**. The `module.install` RPC verb may only **enable/configure a shipped module** — it must
**never fetch or execute downloaded code**. No third-party executable plugins in v1. (aaPanel's
"plugins run as root" is the failure mode this closes.) State this explicitly in the RPC contract.
*Patches Task 8.1 + `internal/rpc/messages.go`.*

---

## 3. New cross-cutting invariants (from live CVEs — add to threat model + tests)

These are not optional hardening; each maps to a specific exploited failure. Put them in
`docs/architecture/threat-model.md` and back each with an architecture/unit test.

1. **Validate at the sink, on every HTTP verb.** CyberPanel's filter ran on **POST only**; a **PUT**
   carried the same payload past it (CVE-2024-51567, pre-auth root RCE). Never put a security check
   in method-conditional middleware — validate where the value is used, for all verbs.
2. **Default-deny authentication, centrally enforced.** The fatal CyberPanel route simply had **no
   auth check**. Use a global default-deny with an explicit **public-route allowlist**, and a test
   that **fails the build if any route lacks an auth decision**.
3. **Agent executes argv arrays, never `sh -c`.** No shell-string construction anywhere near user
   input (firewall rules, DNS writes, mail config, WP install, site/user names). Typed structured
   args; path/identifier validation **in the agent**, not the web tier. aaPanel's and CyberPanel's
   RCEs are all "user value reaches a shell."
4. **Treat site/user names as attacker-controlled.** CloudPanel (CVE-2023-35885) and Froxlor
   (CVE-2026-41235) both had vulns in the **privileged config generator** — the exact component we're
   building. Validate/escape every identifier that becomes a path, username, or config token.
5. **Firewall: dry-run + timed auto-revert + never-removable SSH rule.** Hestia's documented SSH
   lockouts (duplicate-rule-instead-of-amend, deleted allow rules) show format-check-at-rebuild is
   not enough. Apply → read back → verify SSH allow rule present → **auto-revert if not confirmed
   within N minutes**. *Strengthens Task 6.4.*
6. **Nginx apply loop (net-new, not a Caddy port).** `render → nginx -t → write-temp + atomic
   rename/symlink-swap → reload → active HTTPS health probe → restore prior known-good config +
   reload on failure`. **Skip reload when the rendered config hash is unchanged** (Caddy's useful
   idempotency property). Keep the prior good config for revert. *Strengthens Task 3.2.*
7. **Release signing + advisory channel.** Verify the release signature before applying any update
   (ties to D2); maintain a security-advisory feed that flags security fixes explicitly.
8. **Assume an HTTP-layer RCE will eventually exist** and make it non-catastrophic — this is the
   whole justification for the agent boundary. Write that assumption into the threat model.

---

## 4. Decisions still open (block the first commit — unchanged from the review)

Not resolved by this audit; still the owner's call before any code lands:

1. **Product name / `go.mod` module path** — the plan uses a placeholder; it's load-bearing.
2. **License** — repo is **MIT**, product is **proprietary**; prior MIT commits stay MIT. Relicensing
   is a deliberate act before the first proprietary commit enters public history.
3. **PHP NovaPanel cutover** — greenfield-in-this-repo: move PHP under `legacy/` (or freeze on a
   branch), keep both buildable, define a cutover + any data-migration task. Don't interleave Go and
   PHP at repo root.

---

## 5. Suggested amendment order (before Phase 0 tasks begin)

1. Edit the plan text: apply all §1 corrections (strike the BHPanel-internals and Caddy-rollback and
   Coolify-HMAC and ISPConfig-Postgres claims; qualify www-data).
2. Fold the four §2 decisions into their tasks, and the eight §3 invariants into the threat model +
   test strategy.
3. Resolve the three §4 blockers.
4. Then start Phase 0 (workspace + threat model + RPC contracts) with the corrected spec.
