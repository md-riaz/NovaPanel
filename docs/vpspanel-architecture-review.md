# VPSPanel — Whole-Plan Architecture Review

Review of the VPS control panel technical plan (`2026-10-28_043323-vps-panel-technical-plan.md`)
and the design handoff. Scope: tighten the **agent RPC surface**, the **phase gates**, and
the **risk model** before any one implementation thread begins.

**Context decision:** this will be a **greenfield Go rewrite in this repository**, replacing the
current PHP NovaPanel over time.

---

## 1. Verdict

The plan is **strong and unusually disciplined** for a control panel. The central thesis —
an unprivileged web process talking to a root agent that accepts **only typed, allowlisted
actions over a local Unix socket** — is the right spine, and the plan holds it consistently
instead of eroding it under feature pressure. Jobs + audit on every mutation, and
render → `nginx -t` → atomic swap → health-check → rollback, are exactly the invariants a
panel lives or dies on.

It is approvable as a direction. Before building, close the **nine issues** in §5 — most are
specification gaps, not design errors. Two are load-bearing and should block Phase 0 sign-off:
**secrets key management (5.1)** and **the agent trust model (2.1)**.

---

## 2. Strengths worth locking in (do not let these drift)

- **Privilege split with a typed socket.** No `shell.execute`, ever. This is the whole game.
- **Web-stack driver boundary** (`WebStack` interface) with Nginx as the only v1 driver — lets
  OpenLiteSpeed arrive later without infecting core.
- **Every mutation → durable job + audit event.** Idempotency keys, transactional claim,
  restart recovery. This is what makes the panel debuggable and safe.
- **Atomic config discipline** as a stated product principle (borrowed from Caddy), applied to
  Nginx and later OLS migration.
- **Deferral discipline.** Docker, mail, FTP, PowerDNS, OLS all pushed behind modules and gates
  rather than shipped at launch. The "after core is boring" framing is correct.
- **Single SQLite/WAL control plane**, metadata only, never content/archives. Right call for
  one VPS; Postgres explicitly reserved for a future multi-node control plane.

---

## 3. The agent RPC surface — the one place risk concentrates

The agent is the only root-owned panel service. Its API *is* the attack surface. Four
structural points:

### 3.1 The action registry is incomplete and stated twice, inconsistently
The handoff lists ~6 actions; the plan lists ~16; and the phase tasks imply **many more**
privileged operations with no corresponding RPC verb — PHP-FPM pool create, SSH key install,
UFW apply, Restic backup/verify/restore, Git clone/deploy, MariaDB user lifecycle, DNS writes,
SFTP user create, OLS/PowerDNS/FTP module ops.

**Action:** make `internal/rpc/messages.go` the **single canonical registry**, and add an
architecture test (already hinted in the Security checks section) that fails the build if any
`internal/agent/**` handler is reachable without a registered, typed action. Every privileged
op is a verb or it does not exist.

### 3.2 Who authorizes? — define the agent's trust model explicitly
The socket is `root:vpspanel`, mode `0660`. That means **any code running as `vpspanel` can
invoke any action.** So an RCE or SSRF in `panel-web` is, by construction, root on the box via
the agent. The split limits *blast radius of bugs in system-command construction*; it does
**not** contain a full compromise of `panel-web` on its own.

The plan never states whether the agent re-authorizes requests or is a pure executor. Decide
and write it down:

- The agent **cannot** enforce user RBAC it has no knowledge of — don't pretend it does.
- It **can and must** enforce *semantic* invariants independent of the caller: path confinement,
  service allowlists, "never drop SSH/panel", module = shipped-only, deadline + replay rejection,
  and per-action rate limits.
- Keep `panel-web`'s external attack surface minimal precisely because the agent trusts it.
  Treat every `panel-web` dependency and every inbound parser (webhooks, archives, Git URLs) as
  part of the root TCB.

**Action:** add a short "Agent trust model" section to `docs/architecture/trust-boundary.md`
before Phase 0 closes.

### 3.3 The three dangerous verbs need per-verb specs now

| Verb | The trap | Required invariant (agent-side) |
|---|---|---|
| `module.install` | Reads as "install arbitrary code as root." | §8.1 says modules are **first-party, compiled into the release**. So this verb must only *enable/configure* an already-shipped module — never fetch or exec downloaded content. State that in the RPC contract. |
| `backup.restore` | Writes files as root, can clobber production, can path-escape from a malicious archive. | Confine restore target, reset ownership to the site user, reject absolute/`../` archive paths, and take the pre-restore safety snapshot **in the agent**, not the UI. |
| `firewall.apply` | Can lock out SSH/panel. | The "SSH + panel port preserved" rule lives in the agent as a hard refusal, independent of what the UI sends. Apply → read back → verify SSH rule present → else auto-rollback. |

Also tighten `service.restart` to an **allowlist of unit names** (nginx, php*-fpm, mariadb,
the panel services) — not an arbitrary unit string.

### 3.4 Replay / idempotency at the agent
Requests already carry ID + deadline. Enforce both at the agent: reject expired requests and
deduplicate by request ID so a retried or replayed message cannot double-apply. Pair this with
the job-layer idempotency key so retries are safe end to end.

---

## 4. Phase-gate and sequencing issues

1. **No early walking skeleton of the boundary.** The first real browser → web → socket →
   agent → response round-trip doesn't happen until Phase 3. That's a lot built on an
   unexercised channel. **Add to Phase 1** one trivial typed action (e.g. `service.status`)
   flowing the full path, with systemd units wired, before any feature rides it.

2. **Cloudflare/DNS is specified twice.** Task **5.7.2** and Task **7.3** both create
   `internal/integrations/cloudflare/{client,dns,dns_test}.go` and the same template; the DNS
   abstraction (5.7.1) and the "Phase 7 DNS module" overlap. **Collapse into one** — build the
   `dns.Provider` interface + Cloudflare impl once (Phase 5.7), and let Phase 7 only add the
   *API/webhook* surface on top. As written, two tasks will fight over the same files.

3. **WordPress ships before restore exists.** WP install is 4.5; restore is 6.3. Task 4.5
   already says "no WP update/clone/staging until install/**backup/restore** is reliable" — good,
   but make it a hard gate: **no destructive WordPress operation before Phase 6 lands.** The
   first-beta acceptance criteria already require backup+restore, so this is consistent; just
   state the dependency.

4. **Wildcard TLS is blocked until DNS-01.** Phase 3.5 is HTTP-01 only (correct, no DNS
   dependency). Note explicitly that **wildcard certificates require DNS-01** and are therefore
   gated behind the DNS provider (5.7.2). Don't let a UI promise wildcards in Phase 3.

5. **MFA vs. API tokens is unresolved.** 2.2 requires MFA for security/firewall/restore once
   enabled; 7.1 issues API tokens with `backups:write`, `domains:write`, `projects:deploy`.
   A token is a non-interactive principal that **cannot** present a TOTP. Decide the policy:
   either sensitive scopes (restore/firewall) are **excluded from tokens entirely**, or they
   require a separately provisioned, auditable "automation" grant. Write it into 7.1's scope
   table.

6. **CI is in the test strategy but is nobody's task.** `go vet`, staticcheck, govulncheck,
   `systemd-analyze verify`, the "no root web process" and "no arbitrary shell action" assertions
   are listed — but no task creates the pipeline. The current PHP repo has **no `.github/workflows`
   at all**, so this gap is real and recurring. **Make CI a Phase 0 deliverable** (Task 0.1 or a
   new 0.4), running those checks from the first commit.

---

## 5. Gaps to close (prioritized)

**P0 — close before Phase 0 sign-off**

1. **Secrets master-key management is undefined.** "Encrypt secrets before SQLite storage" only
   protects against DB-file exfiltration *if the key isn't sitting next to the DB readable by
   `vpspanel`.* As written, host compromise = all secrets. Decide the key source and the threat
   it actually mitigates: options are a **root-held key the agent uses to seal/unseal on behalf
   of web** (strongest — web never holds plaintext key), a systemd credential, or an OS keyring.
   Recommend: provider tokens/DB creds are sealed by the **agent**, so `panel-web` compromise
   leaks ciphertext, not the key. Document in the threat model.

2. **Agent trust model** — §3.2. Write it down; it reframes every "the UI validates X" claim.

**P1 — close before the dependent phase**

3. **Authz model among users is missing.** There are `users`, `sessions`, API scopes — but no
   role model, despite "one owner/team." Is every user an admin? Define owner / team-member /
   read-only (even minimally) now, because it shapes every API handler and the audit `actor`.
   The current PHP NovaPanel *has* an RBAC model — don't regress it silently in the rewrite.

4. **Job compensation/rollback semantics.** Multi-resource jobs (WP install = site + DB + user +
   cert + files) need defined unwind on partial failure, not just states. Specify
   compensation/saga behavior in `internal/jobs` and per composite handler.

5. **Tamper-evident audit for root actions.** Audit rows are written by `panel-web` into its
   SQLite. A compromised web process could forge or drop them. Have the **agent write its own
   append-only, root-owned action log** as the source of truth for privileged operations;
   reconcile with the SQLite audit for the UI.

6. **Panel self-backup / disaster recovery.** Nothing backs up `panel.db` or the agent's
   config/keys. Losing `panel.db` loses all state. Add panel-config backup to Phase 6 and wire
   `vpspanelctl backup-config` (already promised in the installer section) to it.

**P2 — specify before it bites**

7. **Nginx structured-config coverage.** "No raw config in v1" is right, but real sites need
   `client_max_body_size`, cache/expires headers, custom error pages, HSTS, WP/Laravel location
   blocks, and PHP upload limits. Enumerate the **structured knobs v1 must expose** or users hit
   a wall and demand raw snippets. Define the allowed set deliberately.

8. **PHP-FPM pool density.** One pool per site with default `pm=dynamic` exhausts RAM on a small
   VPS at a few dozen sites. Default to `pm=ondemand`, document a density guideline, and surface
   pool memory in the dashboard.

9. **HTMX + CSRF propagation standard.** Pick one mechanism (CSRF token via `hx-headers` /
   request header vs hidden field) and apply it uniformly; partial HTMX swaps are the easy place
   to forget it.

---

## 6. Greenfield-in-this-repo specifics

Because this replaces the PHP NovaPanel **in place**, three decisions the plan doesn't cover:

- **Product name / module path.** The plan uses a placeholder ("neutral private product name").
  Pick the real `go.mod` path now — it's load-bearing for every import and the `vpspanelctl`
  binary name.
- **License.** This repo is currently **MIT**; the handoff says **proprietary**. You can license
  new code as you choose, but **already-published MIT commits stay MIT** — relicensing is a
  deliberate act (headers, LICENSE change, and awareness that forks of prior history keep MIT
  rights). Decide before the first proprietary commit lands in public history.
- **Coexistence & cutover.** Decide whether existing PHP NovaPanel installs get a **migration
  path** or this is net-new. Suggest: land the Go tree under the planned layout, move PHP under a
  `legacy/` path (or freeze it on a branch), keep both buildable during transition, and define an
  explicit cutover + data-migration task. Don't interleave Go and PHP at repo root.

---

## 7. Recommended immediate next steps

1. Amend the plan with: canonical RPC registry (§3.1), agent trust model (§3.2), per-verb specs
   for `module.install` / `backup.restore` / `firewall.apply` (§3.3), secrets key decision (§5.1),
   and the authz role model (§5.3).
2. Add **Task 0.4: CI + supply-chain gates**, and a **Phase 1 walking-skeleton** task.
3. De-duplicate the Cloudflare/DNS tasks (§4.2).
4. Resolve product name, license, and PHP cutover (§6) — these block the first commit.

Everything else in the plan can proceed on its stated order once these are settled. The bones
are good; this review is about hardening the two or three joints that carry all the weight.

---

## Appendix A — Source audit → decision provenance

The design draws from an explicit panel audit. Mapping each source to what the product takes,
and the one tension each introduces, for traceability:

| Source | Taken | Deliberately rejected | Tension this review watches |
|---|---|---|---|
| **BHPanel** | Unprivileged web + root Go daemon, Unix-socket typed RPC, audit, systemd-first, per-site isolation, signed/checksummed release, safe retryable installer | Reseller/billing/shared-hosting model, full mail+DNS+firewall in V1, binary-only privileged core | This is the **security spine** — §3 is entirely about not eroding it. |
| **aaPanel** | VPS feature breadth, Nginx-first LNMP, optional module model, multi-webserver vision | All-webservers-installed default, root web panel running arbitrary commands | **Breadth vs. "after core is boring."** Biggest scope-creep pressure; deferral gates in §4 exist to resist it. |
| **Coolify** | Git deploy lifecycle, deploy webhooks, CI-triggered deploy, deployment history, async jobs, commit/rollback mindset | Docker-first runtime for normal PHP/WordPress, PaaS scope in V1 | Keep deploy = fixed-arg Git + pinned SHA (plan 5.3); don't let webhooks widen the root TCB (§3.2). |
| **Caddy** | Config validate → atomic apply → runtime verify → rollback | Caddy itself; Caddy beside Nginx | Enforce the rollback invariant in the **agent**, not the UI. |
| **CyberPanel** | WordPress-first expectations, OLS as performance option, local mail/DNS/FTP as future | OLS-only lock-in, full `.htaccess` promise | OLS stays private behind Nginx (plan 8.2); wildcard/DNS-01 gating (§4.4). |
| **CloudPanel** | Focused VPS/app boundary, no reseller complexity | — | Confirms the product boundary; resist feature requests that re-introduce hierarchy. |
| **HestiaCP / ISPConfig / Virtualmin / Froxlor** | Traditional hosting operational reference; ISPConfig's multi-server *separation* as a future architecture note | Full mail/DNS default install, all-in-one day-one stack, Webmin-style generic sysadmin scope, multi-server/reseller in V1 | These all ship local mail/DNS — the demand pressure the §Risks mail/DNS gates are built to absorb. ISPConfig's multi-server idea maps to the reserved SQLite→Postgres control-plane decision. |
| **OpenPanel** | Container isolation + strong per-project separation as a *future option*, migration-oriented thinking | Containers as the default site runtime, higher RAM cost for simple sites | Aligns with Docker-as-later-module; per-site Unix user is the V1 isolation primitive. |

**Net:** the synthesis is sound and the audit does not introduce a new architectural concern
beyond the nine in §5. The through-line risk it makes vivid is **breadth discipline**: four of
these sources are feature-maximalist, full-hosting panels, and the product's safety case depends
on *not* becoming one in V1. The phase gates and module boundary are what keep that promise —
guard them.
