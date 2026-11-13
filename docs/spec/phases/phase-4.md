# Phase 4 — PHP, WordPress, MariaDB

## Goal
PHP sites with per-site isolation (own Unix user + FPM pool), a least-privilege MariaDB DB/user
lifecycle, and a checksum-verified WordPress install. One supported PHP version first.

## Depends on
Phases 1–3 (jobs, auth, sites+nginx renderer). Reads: `00-foundations.md` F6–F12; `vpspanel-plan.md`
§6 (per-site isolation).

## Scope freeze & build order (head ruling — 2026-11-04)

This ruling governs **how** Phase 4 is built, after PR #87 reached 0/7 requirements in 143 commits /
+29k lines by building open-ended "durable foundation" scaffolding instead of closing deliverables.
It does not change the Deliverables, Verification, or DoD below — it bounds them.

**What "done" means here.** A deliverable counts only when its **Verification row passes end-to-end
with the runtime consumer open** — a `php.set` call, a `database.create` call, a `wordpress` site
create that actually runs. A "pure contract", "typed reservation", "journal", or "foundation" that
does not open the runtime path **counts for nothing** toward a deliverable (PR #87's own ledger says
the same). Build the feature, not the floor under the floor.

**Out of Phase 4 — do NOT build (anti-gold-plating).** Durability is already delivered by the Phase-1
job engine (roll-forward recovery, append-only audit, F19 claims). Phase 4 **consumes** it and does
**not** re-implement per-feature durability. Specifically out of scope, and not to be added to this
phase: power-loss / crash-recovery proofs, bespoke descriptor-custody protocols, cgroup execution
domains, fanotify extraction barriers, and any recovery/expiry/admission machinery beyond what the
Phase-1 job contract already provides. Final privileged-VM, cross-UID and power-loss **acceptance is
deferred to product finish** (owner ruling) and must not gate or expand source work. If a corner here
seems to need new durability machinery, that is a **blocker to raise to the head**, not a license to
build it.

**Build order — independent-first, each runtime-open and verified before the next:**
1. **MariaDB DB/user lifecycle** (Deliverable 3): `database.create/rotate_password/delete/list`,
   `dbuser.*`; `unix_socket` root admin, least-privilege grant (never `*.*`), show-once password via
   the F6 claim. No PHP/WordPress needed. Closes Verification rows 3, 4 (transport), 7.
2. **PHP runtime** (Deliverables 1–2): per-site user + FPM pool with the socket-owner **502 fix**, then
   `php.set` / `php.list`. Closes Verification rows 1, 2, 7.
3. **WordPress install** (Deliverable 4): `download → verify-checksums → config create → install`, run
   as the site user, wp-config 440/400, non-empty-docroot refusal; destructive ops stay `unsupported`
   (Deliverable 5 / row 6). Closes Verification rows 5, 6.

**One PR per step** (steps 1 and 2 may each ship on their own), each small and independently
mergeable — not one 285-file mega-draft. Drive each with `docs/spec/PHASE-DRIVER-PROMPT.md`
(`{{TARGET}}` = the step), with **one independent verifier pass per step, not per commit.**

## Deliverables
1. **Per-site Unix user + FPM pool** (§6): one non-login user per site (`/usr/sbin/nologin`, `0751`
   home, `2750 site:www-data` docroot); one PHP-FPM pool over a Unix socket where the **worker runs as the site user** but the
   **socket is owned by the nginx user** (the 502 fix); `pm=ondemand`; per-pool hardening.
2. **PHP version management** (`php.set`, `php.list`): one supported version first; per-site version +
   limits.
3. **MariaDB DB/user lifecycle** (`database.create/rotate_password/delete/list`, `dbuser.*`):
   per-site DB + dedicated user bound to loopback, **least-privilege grant** (never `*.*`), generated
   names + show-once passwords; the **agent authenticates to MariaDB as root via `unix_socket`** (no
   stored admin password anywhere).
4. **WordPress install** (`site.create type=wordpress` or `wp.install`): `wp core download` →
   `wp core verify-checksums` → `wp config create` → `wp core install`, run **as the site user**,
   `wp-config.php` perms `440`/`400`, never over a non-empty docroot.
5. **Guard:** no destructive WordPress ops (update/clone/staging) until backups/restore land (Phase 6).

## Blueprint

### FPM pool (reference — the 502 fix is load-bearing)
`/etc/php/<ver>/fpm/pool.d/novapanel-<site>.conf` (reserved `novapanel-` prefix; never the distro `www.conf`):
```ini
[{{site}}]
user  = {{siteuser}}
group = {{siteuser}}
listen = /run/php/novapanel/{{site}}.sock
listen.owner = nginx        ; the NGINX user owns the socket  ← without this: EACCES → 502
listen.group = nginx        ; (www-data on Debian/Ubuntu, nginx on RHEL/Rocky/Alma)
listen.mode  = 0660
pm = ondemand
pm.max_children         = {{cap}}     ; HARD per-pool cap — from committed SQLite limits (see sizing below)
pm.process_idle_timeout = 10s
pm.max_requests         = 500         ; respawn worker (leak guard)
php_admin_value[cgi.fix_pathinfo]  = 0        ; defense in depth w/ nginx try_files (Phase 3)
; Compatibility profile: no open_basedir and no command-function denylist.
; The worker's Unix uid, filesystem permissions and DB grant are the isolation boundary.
; dl remains disabled in the system PHP configuration.
php_admin_value[error_log]         = /var/log/novapanel/{{site}}/php-error.log
php_admin_flag[log_errors]         = on
```
- **Pool-file update & recovery (F6 Rule-B / F7 / F8 — no `.lkg`, no durable rollback marker).** The
  committed SQLite row (the site's php version + limits) is the **single source of truth**; the
  `pool.d/novapanel-<site>.conf` file is a disposable **projection** of it, so "last-known-good" is the committed
  SQLite state, not a saved previous copy. `php.set` order: render the **new** config to a temp file →
  `php8.3-fpm -t` validate → atomic `rename` into place → reload → probe, **all before** the SQLite
  checkpoint. On any validate/reload/probe failure, **re-render from the (still-committed, old) SQLite
  state** and reload — that is the F8 revert to last-known-good — then return the error; only a fully-good
  swap commits the new version to SQLite. **Recovery/reconcile on any crash** (half-written temp,
  renamed-but-uncommitted, failed dir fsync, failed restore, first creation) is unconditional:
  **re-render the file from the committed SQLite row and reload** (idempotent). This reconverges the file
  to SQLite in every pre/post-rename case — before the checkpoint it rolls back to the old version, after
  it rolls forward to the new — so no previous-marker record or durable rollback intent is needed (and
  none may be added: per-feature durability machinery is out of scope).
- **Managed pool filename & collision (never clobber distro `www.conf`).** The managed pool file is
  **`pool.d/novapanel-<site>.conf`** (reserved `novapanel-` prefix — it can never collide with the distro
  `www.conf`, even for a site literally named `www`) and carries a header line `; novapanel-managed
  site=<ULID>`. The renderer **only** writes/overwrites/deletes a file that is the managed path **and**
  carries that header; any pool file lacking it (distro `www.conf`, an operator's file) is **never touched**.
  A managed-path collision whose header names a **different** ULID → `conflict`, never overwrite.
- **FPM service lifecycle (zero-pool) — gate on the EFFECTIVE pool set, never only the managed count**
  (resolving the #121 stop-on-zero-managed-vs-unmanaged conflict). `php8.3-fpm -t` rejects an empty pool set
  (exit 78) and F0a does **not** require a permanent `www.conf`, but an operator may run their own unmanaged
  pool (`www.conf` or any non-`novapanel-` file in `pool.d/`) that NovaPanel must **never take offline**. So
  the service is ensure-state on the **effective** pool set = every `.conf` in `pool.d/` (managed **and**
  unmanaged): **effective set non-empty → ensure `php8.3-fpm` active**; **removing the last *managed* pool
  when any unmanaged pool remains → `reload` (keep serving the operator's pools), NOT stop**; **stop/disable
  `php8.3-fpm` only when the effective set is completely empty** (no managed, no unmanaged — valid, nothing to
  serve), rather than validate an empty config or keep a dummy pool. Same on **rollback of a failed first
  managed create**: reload/leave running if any pool remains, stop only on a truly empty set — never disrupt
  an operator pool. The next `site.create` re-ensures it active. Never report success on an invalid
  (zero-pool, un-`-t`-able) running config.
- **`pm.max_children` sizing (deterministic, persisted — a pure SQLite projection per #119).** The cap is
  **not** read from live RAM at render time (that would make the pool non-deterministic on reconcile). It is
  computed **once** at `site.create`/`php.set` and **persisted in the site's SQLite `limits`**; the pool
  file renders `pm.max_children` from that committed value and reconcile re-renders the same value. Default
  formula: `php_budget_mb = max(256, floor(MemTotalMB * 0.5)) − 256` (reserve for OS/MariaDB/nginx);
  `cap = clamp(floor(php_budget_mb / 64 / max(1, active_site_count)), 2, 32)` (worker RSS assumed **64 MB**;
  bounds **[2, 32]**). **No user-facing `max_children` override in v1** — correcting #122: canonical `03`
  `php.set.limits` carries only `memory_mb`/`upload_mb`/`max_exec_sec`, and `03` is **not** amended; the cap
  is always the computed+persisted value (an operator rebalance is future). If RAM can't be detected at
  compute time, fall back to the min (`2`) and persist it — never fabricate a RAM-sized value. **Legacy
  backfill:** an already-active site with **no persisted cap** gets the cap **computed once from the formula
  and persisted** on first encounter (site-create replay or reconcile) — deterministic, not a guess — then
  rendered from the persisted value. A later RAM/site-count change does **not** silently mutate already-committed
  caps; rebalancing is an explicit operator op that recomputes and re-persists. So "sized to RAM" = sized once
  from RAM at set/backfill time, then a committed constant.
- **Compatibility profile is the default.** Do not set `open_basedir` or disable `exec`, `passthru`,
  `shell_exec`, `system`, `proc_open` or `popen` by default. A VPS owner may need Composer, framework
  workers, image/PDF processors and deployment hooks. The per-site Unix uid, `0751` home + `2750
  site:www-data` docroot ownership and least-privilege DB user provide the actual boundary; `dl` stays disabled system-wide.
- **Optional hardened PHP profile:** an owner may enable `open_basedir` and a command-function denylist
  for one site. The panel must state that this can break child-process workloads, Composer, queue
  workers, deployment hooks and image/PDF processing. It is never enabled silently.

### MariaDB lifecycle (reference)
```sql
CREATE DATABASE `{{db}}` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER '{{dbuser}}'@'127.0.0.1' IDENTIFIED BY '{{generated}}';
GRANT SELECT,INSERT,UPDATE,DELETE,CREATE,DROP,ALTER,INDEX,CREATE TEMPORARY TABLES,LOCK TABLES,REFERENCES
  ON `{{db}}`.* TO '{{dbuser}}'@'127.0.0.1';          -- NEVER *.*, NEVER GRANT OPTION/FILE/SUPER
```
- **Transport decision (locked here):** **app/site connections use TCP `127.0.0.1`**; the DB user is
  `'{{dbuser}}'@'127.0.0.1'`, the Go/WordPress DSN uses `127.0.0.1`, and `wp config create --dbhost`
  = `127.0.0.1`. The three **must** match or installs fail "Access denied". The **agent's own admin**
  connection uses the **`unix_socket`** plugin (root over the socket — no password).
- **`bind-address = 127.0.0.1`** in server config (no network exposure). Rotation:
  `ALTER USER '{{dbuser}}'@'127.0.0.1' IDENTIFIED BY '{{new}}'`; when the database backs a WordPress
  site the **Step-3** WordPress path then rewrites that site's `wp-config.php` (a DB with no WP site has
  none to update — see the rotate ruling below). `mysql_secure_installation` equivalents at install
  (drop anon users, test DB, remote root; root via unix_socket).
- **No admin DB password is ever stored** (the single most important control): the agent authenticates
  by OS identity via `unix_socket`. If a file credential is ever unavoidable, root-owned `0600`
  outside the DB and web root; **never `MYSQL_PWD`** (deprecated, leaks via `/proc`), never `-p<pw>`
  on argv.

### MariaDB create/replay provenance (F20 — resolves the two-store boundary; head ruling 2026-11-04)
MariaDB DDL and the agent's SQLite are not one transaction, so a crash after `CREATE DATABASE`/
`CREATE USER` but before the SQLite success write would leave existence unable to distinguish *our own
half-finished resource* from *an unmanaged collision*. Resolve it with the **F20 provenance marker held
in MariaDB itself** — not a SQLite-only reservation (which proves the name, not far-side ownership).

Agent-owned root control schema, created once at bootstrap (no site user holds any grant on it):
```sql
CREATE DATABASE IF NOT EXISTS `novapanel`;                 -- control schema (agent/root only)
CREATE TABLE IF NOT EXISTS `novapanel`.`managed_objects` (
  kind       ENUM('schema','user') NOT NULL,
  name       VARCHAR(190)          NOT NULL,                -- schema name, or 'user@127.0.0.1'
  obj_id     CHAR(26)              NOT NULL,                -- owning SQLite resource ULID: databases.id (schema) / db_users.id (user)
  phase      ENUM('reserved','created','deleting') NOT NULL,
  created_at BIGINT                NOT NULL,                -- unix-ms
  PRIMARY KEY (kind, name)                                  -- name unique; OWNERSHIP is obj_id (F20.2), matched on create replay AND delete
);
```

**Bootstrap, availability & trust (head ruling — 2026-11-05).**

*MariaDB is **not** a global startup prerequisite.* The panel-agent **always** brings up its RPC
listener, workers, scheduler and every non-DB capability regardless of MariaDB — a missing
client/socket/server or a control-schema bootstrap error **must not** abort the agent (that would make
the panel unusable for fixing the very box). The systemd unit uses `Wants=`/`After=mariadb` (an ordering
**hint**), **never `Requires=`** (which couples failure); Phase-1's unit is unchanged and depends on no
DB. Control-schema bootstrap is **lazy**: attempted on first use of a `database.*`/`dbuser.*` verb (under
the `db:` lock), or as a non-blocking best-effort at startup that never gates readiness — a MariaDB that
comes up later is picked up without an agent restart. When MariaDB is unreachable/not installed, **only
the DB-lifecycle verbs fail closed** (`precondition_failed`, detail naming the cause; retry-safe once it
is up); unrelated panel operations stay available.

*The control schema is a **trust root** — verify it, never adopt it on sight.* The executor trusts
`managed_objects` markers to authorize a `DROP`, so a blind `CREATE … IF NOT EXISTS` over a **pre-
existing** `novapanel` schema is unsafe (it could be a foreign or planted schema, or one a site account
can write). `CREATE … IF NOT EXISTS` is correct **only for a fresh box** (schema absent → create it
clean, grants root/agent-only). A **pre-existing** `novapanel` is **verified before any marker in it is
trusted**:
- **Structure (table-absent vs malformed — head ruling, 2026-11-05).** The two-statement bootstrap
  (`CREATE DATABASE` then `CREATE TABLE`) is **not** transactional, so an interrupt between them leaves
  the schema present with `managed_objects` **absent** — the agent's own mid-bootstrap window, **not**
  evidence of foreign state. Distinguish by **table presence**:
  - **Table absent → incomplete bootstrap, not malformed.** Provided the Grant check (below) passes,
    **complete the bootstrap** (`CREATE TABLE IF NOT EXISTS`) and proceed — **no operator**. An
    empty/tableless schema holds **no markers**, so there is nothing to adopt and no trust to misplace:
    ownership proof exists only to protect *marker trust*, and zero markers need none; the one residual
    risk (a site user having written the schema) is exactly what the Grant check closes. The bootstrap is
    therefore an **F7/F20 roll-forward** — `CREATE DATABASE IF NOT EXISTS` → grant-verify →
    `CREATE TABLE IF NOT EXISTS` — that **replays cleanly across the inter-statement crash**; a fresh box
    is never wedged on its own partial bootstrap.
  - **Table present but malformed** (wrong columns/types/PK) → **fail closed** (`agent_error`, audited;
    DB verbs refuse), surface to the operator; never `ALTER` or adopt a foreign structure. The agent's
    own fixed DDL never produces a malformed table, so a malformed table is **positive evidence of
    foreign state**.
- **Grants.** **No non-root/site account holds any grant** on `novapanel.*`. Any site-reachable grant →
  the trust root is compromised → **fail closed**, refuse DB lifecycle, surface for operator
  reconciliation. The agent **never** trusts a marker from a schema a site user can write — and because
  this check **gates the table-absent completion above**, a **site-writable empty schema is not
  completed**. The root/agent exemption is by **exact account identity (`user@host`)** — the agent's own
  local-socket admin principal(s) — **never by username**: a `root@'10.0.0.5'`, `root@'%'`, or any
  `root@<non-local>` is a **different, non-exempt principal** and counts as a foreign grant holder here.
- **Never take over foreign state:** a pre-existing `novapanel` that fails verification (malformed table,
  or any site-reachable grant) is **not** dropped/altered/adopted — fail closed and let the operator
  reconcile (rename/remove it). The trust root is never auto-repaired.

Only after structure + grant verification passes does the executor trust `managed_objects` markers to
authorize a `DROP`.

**Startup/periodic provenance reconcile (F20.9 — the catch-all).** After F6 job roll-forward at agent
start, and periodically via the F18 scheduler, a reconcile pass sweeps `novapanel.managed_objects` against
`jobs` + `databases`/`db_users`, acting **only** on markers whose `obj_id` matches our own SQLite (a
foreign/unknown `obj_id` is never touched → operator). Per the F20.9 table: `deleting` + object absent →
remove the tombstone row; `deleting` + object present → complete the `DROP`, remove the row; `created`
with **no** owning `databases`/`db_users` row and the owning job terminal/gone → drive the F20.8 abort
(`deleting` → `DROP` by `obj_id` → remove), **safe because `created` proves ownership**; `reserved` +
object **absent** + no owning row + terminal/gone job → remove the stranded marker (no object to drop);
`reserved` + object **present** + no owning row + terminal/gone job → **`conflict` → operator; never
`DROP`, never adopt** (ownership unprovable — an indeterminate-exit `CREATE` is indistinguishable from a
foreign object; the terminal job removes the replay, not the ambiguity, so both object and marker are
preserved — F20.9 / #105); `created` + owning row present → healthy, leave; `reserved` + present with a
**live** job → leave to that job's replay. This converges any inter-store crash residue (tombstones, a
create whose SQLite row never committed) that a terminal job cannot itself clean, without adopting or
deleting an object of uncertain ownership. It is the F20 counterpart of F6's startup
roll-forward — generic, idempotent, using only canonical state; **not** per-feature machinery.

**Crash-safety is complete (freeze).** F20 rules 1–9 close the two-store `database.*` lifecycle: every
inter-store crash point is covered by F6 roll-forward **or** the F20.9 reconcile. Prove it with the F7
crash-safety replay test at each enumerated stop — before/after **reserve, `CREATE`, phase→`created`,
SQLite commit**, and for teardown **→`deleting`, `DROP`, terminal commit, row removal** — each followed by
a reconcile pass, asserting convergence (the job terminal and correct, and the store in its correct
terminal shape). For the provable stops that means **no orphan object and no stranded marker**. The **one
exception** is the stop **after `CREATE` but before `phase→created`, with the owning job then terminally
failed**: there the F7 assertion is the **#105 operator-conflict outcome** — the object **and** its
`reserved` marker are **both preserved** and surfaced to the operator, **not** automatically removed
(ownership unprovable; never-delete-uncertain). Do **not** assert universal removal for that stop. A sub-statement timing window already converged by rules 1–9 + reconcile is **not** a fresh
contract gap; power-loss/VM acceptance of the residue stays deferred (#90). Raise a new ruling only for a
genuine *contract* gap the table does not cover.

**Bounded contention.** Acquiring the `db:`/`dbuser:` lock and any MariaDB-side wait are **bounded with
cancellation** under the **per-attempt worker deadline** (F6: `Verb.MaxDeadlineMs`, `0` →
`deadline_default_ms`) — **not** the caller's request deadline, which ends at enqueue — never an unbounded
block, failing closed `deadline_exceeded` (F3) on timeout. The timeout is a **transient** attempt outcome:
the job rolls forward (`running`→`queued`) and retries with the **same sealed input unchanged** (F6
per-attempt deadline rule); it is not a terminal `failed`. This is a liveness correction, not a policy
relaxation.

**Locks (F20.7):** `database.create` touches both a schema and its owner user, so it holds **both**
`db:<db_id>` and `dbuser:<username>` (fixed order: `db:` then `dbuser:`); `dbuser.create` holds
`db:<db_id>` (parent) + `dbuser:<username>`. The username is a MariaDB-global namespace, so its own lock
is what serializes two different databases contending the same username — the `db:` lock alone does not.
**A site-associated `database.create`/`database.delete` (non-null `site_id`) also holds the grandparent
`site:<site_id>`**, acquired **first** (order `site:` → `db:` → `dbuser:`), because `site.delete` locks
only `site:<site_id>` and the `db:`/`dbuser:` locks do not overlap it (F20.7 grandparent rule) — this is
what serializes a `database.create` against a concurrent `site.delete` of its site. A detached database
(NULL `site_id`) takes no `site:` lock. Root via `unix_socket`:
1. **SQLite write-ahead** (F20.1): the verb's **job** (02 `jobs`) carries, in `args_json`, the generated
   `db`/`dbuser` names **and the durable resource id** (`databases.id` / `db_users.id`, the `obj_id`),
   and the minted password **sealed** in `secrets` (F12) — written by `Verb.Run` before enqueue. **No new
   table or `prepared` state in 02**; "no success" = the job not yet `done` (replay trigger). **Ownership
   is the `obj_id`, not `jobs.id`** — create, replay, and the later delete are different jobs sharing one
   `obj_id`. Replay reuses these exact values.
2. **Pre-flight** (F20.3 + F20.8), **before any external mutation**, under the locks:
   - **Site association** (F20.8a): if `site_id` is non-null, the `sites` row must exist and be in a
     createable state → else **`not_found`** (unknown `site_id`) / **`precondition_failed`** (site
     `deleting`/`failed`), with **zero external effect**. A re-validation here under the held
     `site:<site_id>` lock is what closes the "valid at admission, `site.delete` racing in" window. A
     NULL `site_id` is a valid **detached** database — no site check.
   - **Names**: the schema **and** the owner username absent from `information_schema.SCHEMATA` /
     `mysql.user`? If present and no `managed_objects` row carries **this resource's `obj_id`** →
     **`conflict`** (unmanaged collision — never adopt, never `CREATE … IF NOT EXISTS`, never `DROP`).
     Pre-flighting **both** names here means an owner-username collision fails `conflict` before the
     schema is created, so no half-made schema is stranded.
3. **Reserve** (F20.2): `INSERT novapanel.managed_objects(kind,name,obj_id,phase='reserved')`; the
   `(kind,name)` PK already present under a *different* `obj_id` → `conflict`.
4. **Create**: `CREATE DATABASE …` / `CREATE USER …@'127.0.0.1' IDENTIFIED BY <sealed>` + least-priv
   `GRANT` (names/password from the sealed input; never `*.*`).
5. **Commit**: `UPDATE … phase='created'`; **the SQLite `databases`/`db_users` row is committed here, at
   success** (so a conflicted/failed create leaves no orphan resource row whose name could mislead a
   later delete); publish the **F19 claim** from the sealed password.

**No orphaned external object (F20.8).** The pre-flight above makes the common cases (unknown `site_id`,
name collision) fail with **zero external effect**. For a **terminal** failure that still strands an
object this create provably made — a TOCTOU collision landing after `CREATE DATABASE` — the create cleans
up via the **delete path**, in this **exact order** (F20.8): (1) advance the owned `managed_objects`
row(s) → **`phase='deleting'`** (the durable abort decision, in `managed_objects`, never a separate SQLite
"cleanup-intent" flag); (2) `DROP` by `obj_id`; (3) **commit the job `failed` + `code`** (SQLite
`jobs.state`/`jobs.code`) **before removing the last row**; (4) remove the `managed_objects` row(s). Step 3
is the linearization point: a `failed` job **never rolls forward**, so "no marker + no object" is reached
**only after** the job is terminal — closing the #101 window where removing the row first left a
non-terminal job with no signal (→ wrong re-create). **Replay before step 3** finds this create's **own
`obj_id` in `phase='deleting'`** → **abort, not create**: complete the `DROP`, finalize `failed` (code
from `jobs.code`, else re-derived by re-running pre-flight), remove the row — it **never** re-creates even
if the collision cleared. A leftover `deleting` row with the object absent (crash after step 3) is a
harmless **tombstone** — the `databases`/`db_users` names are generated-unique so no new create collides;
a startup reconcile or the next op on that name removes it (object absent under `deleting` = delete done →
remove row, never `DROP`). Net: a `database.create` is all-or-nothing across the two stores — success
leaves externals **+** SQLite row **+** `created` provenance; any terminal failure leaves **none** of its
own externals.

**Replay** (job not yet `done`) per F20.4, **branching on `managed_objects.phase`** — an *owned row* is
one carrying **this resource's `obj_id`**; the reserve row is written one DDL statement *before* the
object, so an owned row does not by itself prove the object exists:
- owned row `phase='created'` → object proven **ours** → re-run idempotent `GRANT`/`ALTER` with the
  sealed password, publish the claim (object unexpectedly absent → `conflict`, operator reconcile).
- owned row `phase='reserved'` → creation **unconfirmed**; pre-flight the object: **absent** → resume at
  `CREATE` (we hold the reservation + the `db:<db_id>`/`dbuser:<username>` locks — a clean resume, not
  adoption) → `GRANT`/`ALTER` → advance to `created` → publish the claim; **present** → ownership cannot
  be proven across the reserve→create window (an out-of-band create, **or our own `CREATE` whose client
  exit was indeterminate** — the two look identical) → `conflict`, operator reconcile (never adopt, never
  `DROP`, on a `reserved` row alone). A **nonzero/indeterminate `mysql` client exit** (server-side outcome
  unknown) is **`agent_error`**, the op is **not** `done` and rolls forward into this branch; stamping the
  object with its `obj_id` to shrink this window is the **owed** optimization (out of Step-1 scope).
- owned row `phase='deleting'` → complete the `DROP` idempotently, then delete the row. For a
  `database.delete`/`dbuser.delete` this finalizes `done`; for a `database.create` finding its **own**
  `obj_id` here (an F20.8 abort) it finalizes **`failed`** (never re-creates) with the terminal state
  committed **before** the row is removed.
- no owned row for this `obj_id` (none for the name, or one under a *different* `obj_id`), object
  **present** → not ours → `conflict`, operator reconcile; object **absent** → restart the op with the
  same sealed identities.

**`database.rotate_password{db_id}` — which account.** A database has exactly one **owner** db_user
(the single `db_user` `database.create` returns; `db_users.owner=1`, one per database — enforced by a
partial unique index in 02) plus any secondary `dbuser.create` accounts (`owner=0`).
`rotate_password{db_id}` rotates the **owner** account. `db_id` unknown → `not_found`; the database
exists but has no owner row (inconsistent managed state) → `precondition_failed`. Rotating a secondary
account is **out of Step-1 scope** (a future `dbuser.rotate_password{dbuser_id}` would own it — not added
now). `dbuser.delete` **refuses the owner** account with `precondition_failed` (delete the database to
remove its owner), so a live database always has exactly one owner to rotate.

Mechanics: seal the new password write-ahead (F12/F20.1) **before** `ALTER USER …@'127.0.0.1' IDENTIFIED
BY <sealed>`; the claim publishes from the sealed value, so a crash between `ALTER` and claim re-applies
the same password (idempotent) and re-publishes — the new secret is never lost and the old is never
silently retained.

**wp-config is a WordPress (Step 3) concern, not Step 1.** Step-1 `rotate_password` rotates the MariaDB
credential and publishes the F19 claim; it does **not** write `wp-config.php`. A database with no
associated WordPress site (`databases.site_id` NULL, or a non-WordPress site) has no `wp-config.php` to
update — that step is **not applicable** and the Verification row passes without it. When a WordPress
site backs the database (built in Step 3), that step's rotation path additionally rewrites
`wp-config.php` atomically as the site user. Step 1 therefore proves rotate end-to-end **without**
WordPress; the wp-config coupling is deferred to Step 3 and is **not** silently waived.

**`database.delete` / `dbuser.delete`** is **provenance-last and `obj_id`-matched** (F20.5) — it never
DROPs by name alone. The delete job knows the resource id from its args and the name from the SQLite row;
it DROPs **only** a `managed_objects` row matching **both** `(kind,name)` **and** that `obj_id`:
`phase='deleting'` → `DROP … IF EXISTS` (ensure-absent, F7) → delete the row (a crash mid-delete leaves
`phase='deleting'`, object maybe gone → replay completes the `DROP` idempotently). A row for the name
under a **different** `obj_id` → **not ours**: never `DROP`; the SQLite row is a failed-create/stale
artifact → remove it, leaving the other owner's object untouched. No owned row + SQLite row present → the
object was never created under us → drop the SQLite row only. Already-deleted replay is a no-op; name
reuse is safe (a new resource reserves the freed name under its own `obj_id`, which a stale delete can
never match). (`DROP IF EXISTS` is correct for delete; `CREATE IF NOT EXISTS` stays forbidden for create
— F20.3.)

**`database.delete` cascade & queue ordering (F20.7).** Under `db:<db_id>`, `database.delete` removes the
schema **and every db_user of that database**, each by its own `obj_id` (owner + secondaries), provenance-
last. The object-key lock serializes **execution**, not admission: a `dbuser.create{db_id}` that was
admitted while a `database.delete{db_id}` sat queued is not rejected at admission — instead it
**re-validates its parent under the `db:<db_id>` lock at execution** (ensure-state) and, finding the
database gone/deleting, fails `precondition_failed` and clears any `reserved` row it left. So a stale
queued child can never resurrect a deleted parent, and the cascade never has to guess ownership of a
not-yet-created user. No admission-time machinery is added.

**`site.delete` × `database.*` (the grandparent boundary).** Phase 4 extends the Phase-3 `site.delete`:
on a site with any attached managed database (`databases.site_id` = this site), `site.delete`
**REFUSES** with `precondition_failed` listing the attached `db_id`s — it does **not** cascade or
silently drop a database (data-loss-safe; a cascade-with-typed-confirm flag is owed, not built — see
phase-3). The create↔delete **race** is settled by the F20.7 grandparent lock: because a site-associated
`database.create` holds `site:<site_id>` and `site.delete` holds the same, they serialize — whichever
runs second re-validates under the lock (`database.create` finds the site `deleting`/gone →
`precondition_failed` and rolls back its own `obj_id` externals per F20.8; `site.delete` finds the
database still attached → `precondition_failed`). The `databases.site_id ON DELETE SET NULL` FK stays a
backstop only; the verb refuses before the FK matters.

This is the authorized mechanism for the whole `database.*`/`dbuser.*` family; it is the minimal F20
contract, not a bespoke recovery framework, and does not reopen the #90 scope freeze. Power-loss/VM
acceptance of the crash window stays deferred (#90); the contract is proven by the F7 crash-safety
replay test against a simulated mid-op stop.

### WordPress install (reference — fixed argv, run as the site user)
```
wp core download --version=<v> --path=/srv/sites/{{site}}/public        # refuse if docroot non-empty → precondition_failed
wp core verify-checksums --version=<v>                                   # mismatch → agent_error (tampered); NEVER --insecure
wp config create --dbname={{db}} --dbuser={{dbuser}} --dbpass=<pw> \
    --dbhost=127.0.0.1 --dbprefix=wp_ --dbcharset=utf8mb4 --path=...     # salts unique per site
wp core install --url=https://{{domain}} --title=... --admin_user=... \
    --admin_email=... --skip-email --path=...                           # admin pw: see flag below
# then: chmod 440 wp-config.php (or 400); owned by the site user
```
WP-CLI runs as the **site user** (it refuses `--allow-root` by design); file ownership must match the
FPM pool user so auto-updates work.

#### WordPress install crash-safety & the rotate↔wp-config two-store boundary (Step 3 — head ruling)
The docroot is a **two-store** resource (files vs SQLite); these two windows are F20's filesystem analog.

- **Interrupted install vs the non-empty-docroot refusal.** The refusal (`precondition_failed` on a
  non-empty docroot) protects against clobbering *someone else's* files, but a crash mid-`wp core
  download` leaves **our own** docroot non-empty, and a blind replay would hit the refusal and wedge.
  Resolve it with an **install-intent journal** (SQLite, keyed to the site/job — the F6 "idempotent verb
  may roll forward *provided an intent journal prevents a half-applied state*" pattern, as `ssl.upload`
  already uses — **not** new machinery): the non-empty-docroot check runs **only on first entry** (no
  journal row); `Run` writes the intent row **before** the first filesystem write, so on replay the
  docroot is proven **ours** and the job **resumes** — re-running the idempotent WP-CLI steps
  (`core download`/`verify-checksums`/`config create`/`core install` are each re-runnable against a
  partial tree) and **skipping** the non-empty refusal for its own in-progress install. Clear the intent
  row on terminal success. A docroot that is non-empty with **no** intent row is still a hard
  `precondition_failed` (never adopt unknown content).
- **rotate↔wp-config (the second-store leg).** A password rotation touches two stores — MariaDB
  (`ALTER`) and the `wp-config.php` file. The DB leg is already crash-safe (seal-before-`ALTER` +
  re-publish, above). Bind the **file leg into the same job**: the wp-config rewrite reads the password
  **from the sealed value** (F12/F20.1), is an **atomic** write (temp+rename as the site user), and is
  **re-driven idempotently on replay**. The job is **not `done`** until *both* the DB `ALTER`/claim and
  the wp-config write have succeeded, so a crash in between leaves the job not-`done` → roll-forward
  re-applies the same sealed password to both stores; wp-config can never be left permanently stale, and
  no second/unseen secret is produced.

### Privilege drop, secrets, sockets (resolves B-D11, B29, B39)
- **PHP version pinned: 8.3** (F0a). Pool dir `/etc/php/8.3/fpm/pool.d/`, reload `php8.3-fpm`.
- **`php.list` / `php.set` semantics** (canonical `03`: `php.list{} → {versions:[string], default}` is
  sync RO `sites:read`; `php.set{site_id, version, limits?}` is slow `sites:write` and **`version` is
  required** — the registry/shape precedence the implementer asked about: follow `01`/`03`, not the
  issue prose). v1 supports exactly the pinned set `["8.3"]`. `php.list.versions` = the supported
  versions whose **CLI and FPM both probe OK** (`php8.3 -v` succeeds **and** the `php8.3-fpm` unit is
  present); `default` = the pinned `"8.3"` constant (there is no mutable global default yet, so it is not
  read from any stored setting). If the pinned runtime is missing or a probe fails, `php.list` still
  returns successfully with `versions: []` and `default: "8.3"` — report availability honestly, never
  fabricate it. `php.set` with a version **outside** the supported set → `unsupported` (F3); a supported
  version that is not installed/probing → `precondition_failed`.
- **Run as the site user** via `exec.Run` with `RunOpts{Uid, Gid, Groups}` (F15) — the agent forks a
  child dropped to the site uid/gid (`setgroups`+`setgid`+`setuid`); no `sudo`/`su`/`runuser`, no
  shell. `wp`/`git`/`mysql` all go through this.
- **DB admin** connects as root via the **`unix_socket`** plugin (no password, no argv secret). A
  **site** DB user's password is generated, applied with `CREATE USER … IDENTIFIED BY` /
  `ALTER USER … IDENTIFIED BY` run through an admin connection (DDL, not argv), and delivered to the
  caller **show-once via the F6 job `claim`** — never stored in `jobs.result_json`, the DB, or audit.
- **wp-config password off argv:** `wp config create … --prompt=dbpass` with the password on the
  child's **stdin** (`RunOpts.Stdin`); never `--dbpass=<pw>` on the command line. `mysqldump`/`mysql`
  that need the site cred use `--defaults-extra-file=<root-0600 tmp>` written then removed, not `-p`.
- **FPM socket dir:** `/run/php/novapanel/` created by a **tmpfiles** line
  (`d /run/php/novapanel 0755 root root -`) so it survives an fpm restart (same rationale as
  `/run/novapanel`). Socket `listen.owner/group = www-data` (F0a nginx user).
- **Docroot/home ownership** follows Phase 3's model (docroot `site:www-data 2750`, home
  `/srv/sites/<site>` **`0751`**) so nginx can traverse and serve. Home `0751` gives `other`
  traverse-only (`--x`), **not** read/list, and the `2750 site:www-data` docroot denies `other`
  entirely, so cross-site isolation still holds: a second site's uid can neither list another home nor
  read its docroot/files. Phase 4 creates the user and dirs to match:
  `useradd -r -s /usr/sbin/nologin -m -c "novapanel-site=<site_ULID>"` (GECOS marker — `:`/`,` are
  forbidden in GECOS, so the sentinel is `novapanel-site=<ULID>`), group = the site name;
  reserved-name list in `internal/validate`.
- **Directory provenance — home & log dir (transitive, no separate marker).** The per-site **home**
  (`/srv/sites/<site>`) and **log dir** (`/var/log/novapanel/<site>`) derive provenance **transitively**
  through the GECOS-marked owning user + `sites` row; no own marker, **ensured idempotently (F7)**, never
  adopted. `create`: a pre-existing **non-empty** such dir with **no** matching marked user/`sites` row →
  `conflict`; empty/own → ensure perms, continue.
- **ACME webroot is a SEPARATE root-owned tree (F16, Phase-3), not under docroot** (correcting #120). The
  challenge webroot is `/var/www/acme/<site>` (per-site subdir under `acme_webroot=/var/www/acme`,
  world-traversable B-D8), **root-owned** (`root:root`; provider/nginx read; no group/other write) — **not**
  `<docroot>/.well-known/…`. **Step 2 (PHP runtime) does not create, own, provenance-check, or delete this
  tree** — it is owned/ensured by **Phase-3/`ssl.*`** (resolving the #123 first-issue regression). Concretely:
  - **Creation is `ssl.issue`'s, not `site.create`'s.** `ssl.issue` **ensure-creates** the root-owned
    `/var/www/acme/<site>` (idempotent `mkdir` + F16 perms: `root:root`, world-traversable B-D8, no
    group/other write) **before** `acme.NewWebroot` consumes it, so its precondition is met without
    `site.create` providing the dir. Creating/ensuring our **own** root-owned empty dir is always safe
    (no foreign-adoption question — it is an ensure-state on a root-owned path we are about to write
    challenge files into). This un-regresses first issuance after `site.create` correctly stopped touching
    the tree (#123); **Step 2 code stays entirely out of the ACME tree.**
  - **The full ULID-bound lifecycle is DEFERRED to a tracked Phase-3/`ssl.*` item, not Step-2's gate.** The
    **name/path alone does not prove ownership** (the slug is reusable after the row is removed), so a durable
    **site-ULID provenance marker is owed** — a root-owned `.novapanel-owner` file carrying `<site_ULID>`
    inside the tree, keyed on the ULID not the name, with the same create/replay/retain/purge/legacy-migration
    shape as the OS user. Implement it in Phase-3/ssl (now or as a bounded follow-up); it is **not** required
    for the Step-2 PHP-runtime gate.
  - **Interim deletion safety (until the marker lands):** teardown (`ssl.*`/`site.delete`) removes
    `/var/www/acme/<site>` **only on proven ownership** — the matching marker, or the tree is empty / holds
    only our own challenge files; an ambiguous **non-empty** tree with no matching marker is **retained +
    surfaced to the operator**, never `rm`-by-name, never adopted. So there is no unsafe behavior in the
    deferral window. Step 2 **never deletes or adopts** `/var/www/acme/<site>`; a pre-existing or ambiguous
    tree is Phase-3's to reconcile, never Step 2's.
- **Log directory & file perms.** Panel creates `/var/log/novapanel/` `0755 root root`, and per site
  `/var/log/novapanel/<site>/` owned **`<siteuser>:adm` `0750`** (the FPM worker runs as the site user, so
  it must own/write its `php-error.log`; operators read via group `adm`; `other` none). The log file itself
  is `0640 <siteuser>:adm`. Created/ensured at site create (F7), removed on purge with the home.
- **Legacy (Phase-3) accounts & dirs** are handled by the one-time startup migration (`00` §Panel-owned
  OS-user provenance): it stamps `novapanel-site=<ULID>` onto an unmarked account matching a **settled**
  `sites` row (active/created) **only** — **never** a `creating` row (ownership not yet proven) or a
  `deleting` row. An interrupted `creating`/`deleting` row is resolved by the owning job's **roll-forward**
  (which stamps its own account as it proceeds, or fails closed `conflict` on a foreign collision), **not**
  by the migration — so the migration never stamps a foreign collision. (F9 "live rows" includes
  `creating`/`deleting`; "stampable" is the narrower **settled/proven** subset — being live is not proof of
  ownership, so startup never stamps a `creating`/`deleting` account.) Home/log dirs under a now-marked user
  need no separate stamping (transitive); the separate ACME tree is **not** stamped by this migration — its
  ULID provenance is owed to Phase-3/ssl (above). An unmarked account with **no** matching row stays foreign.
- **OS-user ownership = the GECOS marker, not the name** (`00` §Panel-owned OS-user provenance; F20
  instantiation). The site slug is reusable, so ownership is proven by the account's GECOS
  `novapanel-site=<ULID>` matching the op's `sites.id`, **never by the name alone** (this closes the
  earlier "reserved-name = orphan vs. foreign collision" ambiguity — the GECOS marker is the obj_id):
  - **create** (ensure-state): a name collision whose GECOS marks **this** site's ULID is our own prior
    attempt → idempotent roll-forward (F6/F7); a collision with **no** panel marker, or a **different**
    site's ULID, → `conflict`, never adopt/`usermod`.
  - **delete**: the owning `site.delete` job drives teardown by the sentinel + matching site ULID (its row
    is legitimately `deleting` with a live job — "no live row/job" is **not** required of it); ordered:
    pool file → reload. Then **only if `purge_files`** (phase-3): home guard → `userdel`. On **retain**,
    keep the home + user dormant and rewrite GECOS to `novapanel-retained=<ULID>` so the sweep spares it.
  - **reconcile (orphan sweep)**: `userdel` only an account whose GECOS carries the **`novapanel-site=`**
    sentinel (never a `novapanel-retained=` one) **and** a site ULID with **no** live `sites` row and **no**
    live job, so it never races the owning op nor deletes a retained account. Pre-existing unmarked accounts
    are handled by the one-time migration (`00`), never adopted on sight. A reserved-scheme account lacking
    the sentinel is foreign → never touched,
    surfaced to the operator. A crash leaves at most a marked orphan, which reconcile converges by its
    own ULID.
  - **Orphan purge order — no `userdel` while the UID still owns files (UID-reuse guard).** A proven orphan
    (GECOS `novapanel-site=<ULID>`, no live `sites` row, no live job) is purged **UID-owned files first,
    then the account**: remove the managed pool file → reload/stop-FPM-if-last → remove the log dir → remove
    the home tree `/srv/sites/<site>` → **then** `userdel`. The separate `/var/www/acme/<site>` is root-owned
    (no UID-reuse risk): remove by its deterministic path or leave for the operator. `userdel` runs **only
    after** every file owned by that UID is gone; if any purge step fails or any ownership is uncertain, **do
    not `userdel`** — retain the account and surface to the operator (the conservative skip is the correct
    fallback). This completes convergence while guaranteeing a released UID can never inherit surviving files.

## External refs (gathered — verified, with flags to resolve before coding)
- **502 cause (verified):** FPM socket default owner = FPM run-user (site user) mode 0660 → nginx
  `connect()` EACCES → 502; fix is `listen.owner/group = nginx`, not the worker identity.
- **MariaDB (verified):** `localhost`≠`127.0.0.1` are distinct accounts (socket vs TCP); `unix_socket`
  plugin auth = peer OS uid via SO_PEERCRED (no password) — cleanest for a root agent; an
  `unix_socket` account has no password so `SET PASSWORD` errors; `MYSQL_PWD` "extremely insecure",
  deprecated.
- **WordPress (verified):** `wp core verify-checksums` compares against WordPress.org hashes; **never
  `--insecure`**; wp-config perms **440/400**; run as site user not root; check docroot empty first.
- **Flags to resolve (do not guess):** (a) `wp core install --admin_password` may need a nightly
  WP-CLI — pin the version or set the admin password via `wp user update` after; (b) whether
  `wp config create` pulls salts from the WordPress.org secret-key API vs local gen is unstated —
  requirement is **unique-per-site salts**, inject remote salts explicitly if required; (c) confirm
  the exact privilege list against the target WordPress version.

## Verification (the gate)
```
1.  Per-site user: non-login, `0751` home + `2750 site:www-data` docroot; a second site's user can
      neither list another home nor read its docroot/files (home `0751` is traverse-only for `other`).
2.  FPM pool: worker runs as the SITE user (asserted), socket owned by the NGINX user mode 0660 →
      nginx serves PHP (no 502). A second site's worker cannot read the first site's files.
      The default compatibility profile leaves child-process functions available; a hardened profile is
      opt-in and its warning is tested.
3.  DB lifecycle: database.create makes a loopback-bound OWNER user with ONLY that db's privileges (a
      test asserts the user cannot read another db and has no *.* grant); rotate_password{db_id} rotates
      that owner account and publishes a new show-once claim; the agent used unix_socket (no admin
      password exists anywhere in config/DB). (wp-config rewrite on rotate is a Step-3/WordPress check,
      not required for a DB with no WP site.)
4.  Transport consistency: user host, Go/WP DSN, and wp-config --dbhost all 127.0.0.1; a mismatch
      test reproduces "Access denied" and the spec's single-transport rule prevents it.
5.  WP install: verify-checksums runs and a tampered file FAILS the install; wp-config.php is 440/400
      owned by the site user; install over a non-empty docroot is refused; WP-CLI ran as the site user.
6.  No destructive WP op (update/clone/staging) is callable yet (returns unsupported until Phase 6).
7.  All **mutating** verbs are jobs (reads are sync per 01); all are audited; passwords are show-once
      via the job claim (never returned twice, never in result_json/logs).
```

## Definition of Done
- [ ] Per-site user + FPM pool with the socket-owner 502 fix; cross-site isolation proven.
- [ ] Pool hardening: `cgi.fix_pathinfo=0`, per-site error log, and default compatibility profile
  documented. Hardened PHP is an explicit per-site option with compatibility warning.
- [ ] MariaDB per-site DB+user, least-privilege (no `*.*`), loopback-bound; admin via unix_socket, no stored admin password.
- [ ] Single DB transport (127.0.0.1) consistent across user host / DSN / wp-config.
- [ ] WordPress install verifies checksums (never --insecure), wp-config 440/400, runs as site user, refuses non-empty docroot.
- [ ] Show-once passwords; all verbs jobs + audited; no destructive WP ops before Phase 6.

## Guardrails (this phase)
- **The FPM socket is owned by the Nginx user, never the site user.** The worker runs as the site user;
  these settings must remain separate to avoid the 502 permission-denied failure.
- **Do not treat PHP configuration as the isolation boundary.** The default profile keeps process-execution
  functions available and does not set `open_basedir`; per-site Unix uid, `0751` home + `2750 site:www-data` docroot ownership,
  per-site socket and least-privilege DB grants contain a compromised project. Hardened PHP restrictions
  are optional and must carry a compatibility warning.
- **Never grant `*.*` or `GRANT OPTION`/`FILE`/`SUPER`;** per-site DB + loopback user only.
- **No DB-admin password stored anywhere** — agent uses `unix_socket`; never `MYSQL_PWD`/`-p` on argv.
- **Never skip checksum verification or use `--insecure`;** wp-config never world-readable; WP-CLI
  never as root.
- **No destructive WordPress ops until restore exists (Phase 6).**
