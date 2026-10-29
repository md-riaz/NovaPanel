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

**Column key:** Pub=callable with no/anon session · Role=min user role (O/A/RO) · R/O=callable by
`read_only` · Scope=API-token scope (— = not token-reachable) · Slow · Eff=effectful (F6) · MFA.
`ObjectKey` given where Slow.

| Verb | Pub | Role | R/O | Scope | Slow | Eff | MFA | ObjectKey / note |
|---|---|---|---|---|---|---|---|---|
| `ping` | ✓ | — | ✓ | — | | | | returns version |
| `agent.info` | | O | ✓ | — | | | | build/version/uptime |
| `session.login` | ✓ | — | | — | | | | rotates anon→user |
| `session.reauth` | | RO | | — | | | | sets mfa_at; rotates id |
| `session.logout` | | RO | | — | | | | |
| `session.whoami` | | RO | ✓ | — | | | | returns role+csrf_token |
| `mfa.enroll` / `mfa.confirm` | | RO | | — | | | | returns otpauth URI once |
| `mfa.disable` | | RO | | — | | | ✓ | |
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
| `backup.restore` | | A | | **backups:restore** | ✓ | ✓ | ✓ | `site:<name>` (excluded scope) |
| `firewall.status` | | A | ✓ | system:read | | | | |
| `firewall.apply` | | A | | **firewall:write** | ✓ | ✓ | ✓ | `firewall` (excluded scope, armed revert) |
| `firewall.confirm` / `firewall.revert` | | A | | — | | | ✓ | cancels/fires the deadman |
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

**Excluded scopes** (`backups:restore`, `firewall:write`, `ssh:write`, `tokens:write`,
`system:power`) are never on an ordinary token; they require a user+MFA session or an automation-grant
token (F4). **Forbidden permanently:** `shell.execute`, run-command, write-arbitrary-root-path (no
entry, ever).

**Internal, not web/registry-reachable:** `tokens.verify` and `deploy.hook` are called only by
`panel-web`'s API/webhook handlers over the socket with a trusted marker; they are not in the public
scope map and carry no `Scope`.
