# 01 — RPC verb registry (v1 canonical set)

The authoritative list of every v1 verb and its `Verb` attributes (F13). A verb's phase spec **must
match this table**; the Registry is the single source of truth and the arch test enforces it. Shapes
(args/reply) are defined in each phase; this file fixes **name, who may call, and execution class**.

**Naming reconciliations (decided — resolves the review's db.*/deploy.* ambiguity):**
- Database verbs are **`database.*`** and **`dbuser.*`** (not `db.*`). DNS record verbs are
  **`dns.record.*`**. The Git deploy executor is **`deploy.run`** (the earlier `deploy.pull` name is
  dropped; manual deploys and the webhook both enqueue `deploy.run`).
- "All verbs are jobs" is **wrong**: only verbs with `Slow=true` enqueue. Reads (`*.list/get/status`,
  `files.read`, `log.tail`) are synchronous.

**Column key:** Pub=callable with an anon/no session · Role=min user role (O/A/RO) · R/O=`read_only`
may call it too · Scope=API-token scope (— = not token-reachable) · Slow · Eff=effectful (F6) · MFA.
`ObjectKey` given where Slow.

**Semantics (per F13), resolving the review's Role-vs-R/O confusion:** `Role` is the floor for a
privileged user; **`R/O ✓` additionally admits a `read_only` user** to that (read-only) verb even
though its `Role` is higher — so `firewall.status` (Role A, R/O ✓) is callable by an admin, an owner,
**and** a read-only user, but not by a token lacking `system:read`. A `kind=token` session is gated by
`Scope` alone, never by `Role`. `anon` may call only `Pub ✓` verbs.

| Verb | Pub | Role | R/O | Scope | Slow | Eff | MFA | ObjectKey / note |
|---|---|---|---|---|---|---|---|---|
| `ping` | ✓ | — | ✓ | — | | | | returns version |
| `agent.info` | | O | ✓ | — | | | | build/version/uptime |
| `session.login` | ✓ | — | | — | | | | rotates anon→user |
| `session.start` | ✓ | — | | — | | | | **idempotent get-or-create** anon session + csrf (pre-login bootstrap): live anon presented → returns it unchanged; none → mints (rate-limited per peer); authed → `failed_precondition`. Serves the anon pre-login CSRF re-fetch (F4) |
| `session.reauth` | | RO | | — | | | | **self-service**; sets mfa_at; rotates id + csrf |
| `session.logout` | | RO | | — | | | | **self-service** |
| `session.whoami` | | RO | ✓ | — | | | | returns role+csrf_token |
| `mfa.enroll` / `mfa.confirm` | | RO | | — | | | | **self-service**; returns otpauth URI once |
| `mfa.disable` | | RO | | — | | | ✓ | **self-service** |
| `tokens.create` | | A | | — | | | ✓ | automation grant needs step-up |
| `tokens.list` / `tokens.revoke` | | A | list:✓ | — | | | | |
| `system.status` / `system.metrics` | | RO | ✓ | system:read | | | | cpu/mem/disk/uptime |
| `service.status` | | RO | ✓ | system:read | | | | systemctl is-active |
| `service.restart` | | A | | system:write | ✓ | ✓ | | `service:<unit>` |
| `site.list` / `site.get` | | RO | ✓ | sites:read | | | | |
| `site.create` | | A | | sites:write | ✓ | | | `site:<name>` |
| `site.update` | | A | | sites:write | ✓ | | | `site:<name>` |
| `site.delete` | | A | | sites:write | ✓ | | ✓ | `site:<name>` (ensure-absent) |
| `domain.add` / `domain.remove` | | A | | sites:write | ✓ | | | `site:<name>` |
| `ssl.issue` / `ssl.renew` | | A | | sites:write | ✓ | ✓ | | `site:<name>` (ACME, rate-limited) |
| `ssl.upload` | | A | | sites:write | ✓ | | ✓ | `site:<name>` — install a user-supplied cert+key (manual) |
| `ssl.auto_set` | | A | | sites:write | | | | `site:<name>` — turn Let's Encrypt auto-renew on/off |
| `ssl.status` | | RO | ✓ | sites:read | | | | |
| `php.set` | | A | | sites:write | ✓ | | | `site:<name>` |
| `php.list` | | RO | ✓ | sites:read | | | | |
| `database.create` | | A | | databases:write | ✓ | | | `db:<name>` (show-once pw via claim) |
| `database.rotate_password` | | A | | databases:write | ✓ | | ✓ | `db:<name>` |
| `database.delete` | | A | | databases:write | ✓ | | ✓ | `db:<name>` |
| `database.list` | | RO | ✓ | databases:read | | | | |
| `dbuser.create` / `dbuser.delete` | | A | | databases:write | ✓ | | | `db:<name>` |
| `files.list` / `files.read` | | A | | sites:read | | | | rooted at docroot, not home (B-D10) |
| `files.write`/`mkdir`/`move`/`delete`/`chmod` | | A | | sites:write | | | | runs as site user |
| `files.upload`/`archive`/`extract` | | A | | sites:write | ✓ | | | `site:<name>` (chunked/large) |
| `deploy.configure` | | A | | deploy | | | | per-site deploy key |
| `deploy.run` | | A | | deploy | ✓ | | | `site:<name>` (pinned SHA) |
| `deploy.rollback` | | A | | deploy | ✓ | | | `site:<name>` |
| `deploy.status` / `deploy.history` | | RO | ✓ | deploy | | | | |
| `cron.create`/`delete`/`enable`/`disable` | | A | | sites:write | ✓ | | | `site:<name>` |
| `cron.list` | | RO | ✓ | sites:read | | | | |
| `log.tail` | | A | | sites:read | | | | allowlisted targets only |
| `sshkey.add` / `sshkey.remove` | | A | | ssh:write | | | ✓ | public keys only |
| `sshkey.sftp_only` | | A | | ssh:write | ✓ | | ✓ | sshd render+reload |
| `sshkey.list` | | RO | ✓ | sites:read | | | | |
| `backup.create` | | A | | backups:write | ✓ | ✓ | | `backup:<repo>` |
| `backup.list` | | RO | ✓ | backups:read | | | | |
| `backup.verify` | | A | | backups:read | ✓ | | | `backup:<repo>` |
| `backup.restore` | | A | | **backups:restore** | ✓ | ✓ | ✓ | `db:<name>` or `site:<name>` per target (excluded scope) |
| `firewall.status` | | A | ✓ | system:read | | | | |
| `firewall.apply` | | A | | **firewall:write** | ✓ | ✓ | ✓ | `firewall` (excluded scope, armed revert) |
| `firewall.confirm` / `firewall.revert` | | A | | firewall:write | | | ✓ | cancels/fires the deadman (automation can confirm) |
| `dns.record.create`/`update`/`delete` | | A | | dns:write | ✓ | | | `zone:<zone>` |
| `dns.record.list` | | RO | ✓ | dns:read | | | | |
| `dns.verify` | | A | | dns:read | | | | resolver + provider check |
| `dns.purge` | | A | | dns:write | ✓ | | | `zone:<zone>` |
| `integration.set` | | A | | — | | | ✓ | stores sealed provider token |
| `job.status` / `job.list` | | RO | ✓ | system:read | | | | |
| `job.claim` | | RO | | — | | | | one-shot secret (F6) |
| `job.cancel` | | A | | system:write | | | | |
| `reconcile` | | O | | — | ✓ | ✓ | | `system` |
| `update.check` | | A | ✓ | system:read | | | | |
| `update.apply` | | O | | — | ✓ | ✓ | ✓ | `system` (D2 signed) |
| `panel.backup_config` | | O | | — | ✓ | ✓ | | self-backup (B-D11/B20) |
| `module.install`/`enable`/`disable` | | O | | — | ✓ | | ✓ | `module:<name>` |
| `module.status` / `module.list` | | RO | ✓ | system:read | | | | |
| `audit.tail` | | A | ✓ | system:read | | | | |

**Additional verbs (pass-2 — were missing rows):**

| Verb | Pub | Role | R/O | Scope | Slow | Eff | MFA | ObjectKey / note |
|---|---|---|---|---|---|---|---|---|
| `user.create`/`update`/`delete`/`set_password` | | O | | — | | | ✓ | first owner via local ctl |
| `user.list` | | A | ✓ | — | | | | |
| `repo.create` | | A | | backups:write | ✓ | | ✓ | runs `restic init`; password via claim |
| `repo.delete` | | A | | backups:write | | | ✓ | |
| `repo.list` | | RO | ✓ | backups:read | | | | |
| `schedule.list`/`set`/`enable`/`disable` | | A | list:✓ | system:read/write | | | | drives F18 |
| `ols.status` | | A | ✓ | system:read | | | | module (Phase 9, private per-site backend) |
| `ols.enable`/`ols.disable` | | O | | — | ✓ | ✓ | ✓ | site-scoped, auto-rollback |
| `firewall.apply` confirm flow → `firewall.confirm`/`revert` | | A | | **firewall:write** | | | ✓ | now token-reachable so automation can confirm |

**Internal** (not web/registry-reachable in the public scope map, called by `panel-web` handlers over
the socket from a **uid-authenticated** peer, i.e. the web uid, with an internal verb flag — not a
payload marker): `tokens.verify`, `deploy.hook`, `deploy.hook_create`/`list`/`delete`. These still
have `Registry` entries (invariant 3) with `Public=false, Scope="", Internal=true`; the arch test
counts them as registered, and the dispatcher refuses them unless the connection is the web peer.

**HTTP status for the pass-2 codes:** `interrupted` → 409; `unsupported` → 501.

**Excluded scopes** (`backups:restore`, `firewall:write`, `ssh:write`, `tokens:write`,
`system:power`) are never on an ordinary token; they require a user+MFA session or an automation-grant
token (F4). `firewall.confirm`/`revert` now carry `firewall:write` (above) so an automation-grant
apply can be confirmed instead of always auto-reverting. **Forbidden permanently:** `shell.execute`, run-command, write-arbitrary-root-path (no
entry, ever).
