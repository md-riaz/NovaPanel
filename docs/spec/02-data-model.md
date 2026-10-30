# 02 — Data model (agent-owned SQLite)

Complete DDL for the control-plane DB (`/var/lib/novapanel/panel.db`, `0700 root`, D9). This is the
source of truth for every table a phase references. Conventions (F5): WAL; `PRAGMA foreign_keys=ON`;
**IDs are ULID strings** (`internal/id`, crockford base32); **timestamps are INTEGER unix-millis UTC**;
money/none. Migrations are forward-only SQL under `internal/store/migrations/NNNN_name.sql`, embedded
with `go:embed`, applied in a transaction, recorded in `schema_migrations`. A migration is **never
edited after it ships**; a test hashes each migration file and fails the build if a shipped hash
changed (integrity) or if applying `0001..N` to a fresh DB errors.

Tables are grouped by the phase that introduces them; a later phase's `ALTER`/new table ships as a new
migration. `0001_init.sql` creates the Phase-1 core (identity, jobs, audit, secrets, settings).

## Phase 1 — core

```sql
CREATE TABLE schema_migrations (
  version   TEXT PRIMARY KEY,         -- "0001", applied in order
  hash      TEXT NOT NULL,            -- sha256 of the file at apply time (integrity)
  applied_at INTEGER NOT NULL
);

CREATE TABLE users (
  id            TEXT PRIMARY KEY,     -- ULID
  username      TEXT NOT NULL UNIQUE,
  email         TEXT NOT NULL,
  pw_phc        TEXT NOT NULL,        -- argon2id PHC string (F15/phase-2)
  role          TEXT NOT NULL CHECK (role IN ('owner','admin','read_only')),
  totp_secret   TEXT,                 -- sealed (F12); NULL = MFA not enrolled
  disabled      INTEGER NOT NULL DEFAULT 0,
  created       INTEGER NOT NULL,
  updated       INTEGER NOT NULL
);

CREATE TABLE backup_codes (            -- MFA recovery (phase-2)
  id        TEXT PRIMARY KEY,
  user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_phc  TEXT NOT NULL,            -- argon2id hash of a single-use code
  used_at   INTEGER
);

CREATE TABLE sessions (
  id            TEXT PRIMARY KEY,     -- ULID; the opaque token is this id + a secret, hashed below
  token_hash    TEXT NOT NULL,        -- sha256 of the session secret (sessions are high-entropy; no argon2 needed)
  kind          TEXT NOT NULL CHECK (kind IN ('anon','user','token')),
  user_id       TEXT REFERENCES users(id) ON DELETE CASCADE,
  role          TEXT,                 -- resolved role for a user session
  csrf_token    TEXT NOT NULL,        -- synchronizer CSRF token (F4); not a cookie
  mfa_at        INTEGER,              -- last TOTP verification (F4 reauth window)
  created       INTEGER NOT NULL,
  last_seen     INTEGER NOT NULL,
  idle_deadline INTEGER NOT NULL,
  abs_deadline  INTEGER NOT NULL
);
CREATE INDEX idx_sessions_user ON sessions(user_id);

CREATE TABLE jobs (
  id         TEXT PRIMARY KEY,
  verb       TEXT NOT NULL,
  args_json  TEXT NOT NULL,
  object_key TEXT NOT NULL,
  effectful  INTEGER NOT NULL DEFAULT 0, -- from Verb.Effectful at enqueue; recoverRunning reads it w/o the Registry
  actor      TEXT NOT NULL,             -- caller audit label at enqueue (job-side audit)
  role       TEXT,                      -- caller role at enqueue
  created_by TEXT,                      -- session/token id that created the job (owner may read/claim its own job)
  state      TEXT NOT NULL CHECK (state IN ('queued','running','done','failed')),
  step       TEXT NOT NULL DEFAULT '',
  attempts   INTEGER NOT NULL DEFAULT 0,
  progress   REAL NOT NULL DEFAULT 0,
  result_json TEXT,                   -- non-secret result only (F6)
  error      TEXT,
  code       TEXT,                    -- F3 code on failure
  created    INTEGER NOT NULL,
  updated    INTEGER NOT NULL,
  finished   INTEGER
);
CREATE INDEX idx_jobs_state ON jobs(state, created);
CREATE INDEX idx_jobs_object ON jobs(object_key);

CREATE TABLE job_events (
  id      TEXT PRIMARY KEY,
  job_id  TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  ts      INTEGER NOT NULL,
  msg     TEXT NOT NULL
);
CREATE INDEX idx_job_events_job ON job_events(job_id, ts);

CREATE TABLE job_claims (              -- show-once secret delivery (F6 claim)
  job_id     TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
  claim_hash TEXT NOT NULL,           -- sha256 of the one-time claim token
  nonce      BLOB NOT NULL,           -- the secret is sealed (F12) at rest here
  ciphertext BLOB NOT NULL,
  expires    INTEGER NOT NULL,        -- short TTL; row deleted on claim or expiry
  claimed    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE schedules (               -- the agent's internal scheduler (F18)
  id        TEXT PRIMARY KEY,
  verb      TEXT NOT NULL,            -- the verb to enqueue
  args_json TEXT NOT NULL,
  cron      TEXT NOT NULL,            -- OnCalendar-style spec
  state_json TEXT NOT NULL DEFAULT '{}', -- e.g. the rolling read-data-subset n
  next_run  INTEGER NOT NULL,
  enabled   INTEGER NOT NULL DEFAULT 1,
  last_run  INTEGER
);

CREATE TABLE events (                  -- audit (F11); mirror of the append-only file
  id         TEXT PRIMARY KEY,
  ts         INTEGER NOT NULL,
  actor      TEXT NOT NULL,
  role       TEXT,
  verb       TEXT NOT NULL,
  object_key TEXT,
  outcome    TEXT NOT NULL CHECK (outcome IN ('ok','refused','error')),
  code       TEXT,
  detail_json TEXT                     -- redacted (F12) before write
);
CREATE INDEX idx_events_ts ON events(ts);

CREATE TABLE secrets (                 -- F12 sealed blobs
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  nonce      BLOB NOT NULL,
  ciphertext BLOB NOT NULL,
  created    INTEGER NOT NULL
);

CREATE TABLE settings ( key TEXT PRIMARY KEY, value_json TEXT NOT NULL );
```

## Phase 2 — API tokens

```sql
CREATE TABLE api_tokens (
  id         TEXT PRIMARY KEY,        -- the <id> embedded in npat_<id>_<secret>
  name       TEXT NOT NULL,
  secret_phc TEXT NOT NULL,           -- argon2id hash of <secret>
  scopes     TEXT NOT NULL,           -- JSON array of scope strings (F4 / 01-rpc-and-verbs)
  automation INTEGER NOT NULL DEFAULT 0,
  created_by TEXT NOT NULL REFERENCES users(id),
  created    INTEGER NOT NULL,
  expires    INTEGER,                 -- NULL = no expiry
  revoked    INTEGER NOT NULL DEFAULT 0,
  last_used  INTEGER
);

CREATE TABLE idempotency (             -- F4 (public API)
  id          TEXT PRIMARY KEY,        -- sha256(token_id || ':' || key)
  job_id      TEXT REFERENCES jobs(id),-- set for async (202+job) mutations
  result_json TEXT,                    -- set for sync mutations (no job)
  payload_hash TEXT NOT NULL,          -- sha256(method || path || body); replay w/ a different hash → 409
  created     INTEGER NOT NULL         -- TTL enforced on read (default 24h)
);
```

### Migration `0002_schedule_authority.sql` (Phase 1, forward-only)

`0001_init.sql` ships **byte-for-byte immutable** (the integrity test above enforces it); persisted
authority for the scheduler and for restart-safe job re-authorization lands as a **new** migration,
never a retrofit of `0001`. `jobs` already carries `effectful`, `actor`, `role`, `created_by`; this adds
the scope/automation snapshot so an effectful job resumed after a crash and an owner-bound claim check
(F19) authorize from **stored** authority, not a session that may be gone. `schedules` gains the owner's
authority so a firing (F18 `EnqueueScheduledJob`) runs as the stored owner without a live session.

**Worker admission clarification.** No schema migration is added. For directly dispatched jobs,
`role='token'` identifies a token and `scopes_json` contains its scopes; user/local-root jobs retain
the resolved role and empty scopes. Legacy token snapshots with an empty role and nonempty scopes
remain scope-checked. `jobs.automation` snapshots successful MFA admission: for tokens it is only
the token automation grant; for user/local-root it is set only after dispatch passes that verb's
fresh-MFA gate. The worker requires this snapshot for MFA verbs and never rechecks a live session.
This does not confer an automation grant on the user's session or schedules. Old MFA user jobs
with a false snapshot fail closed and must be submitted again. Schedules keep their existing
explicit automation-grant requirement.

```sql
-- jobs: persist the full authority snapshot captured at the agent boundary (F4/F6/F8 ruling)
ALTER TABLE jobs      ADD COLUMN scopes_json TEXT NOT NULL DEFAULT '[]'; -- token scopes at enqueue ([] for user/local-root)
ALTER TABLE jobs      ADD COLUMN automation  INTEGER NOT NULL DEFAULT 0; -- MFA admission snapshot (see below)

-- schedules: the stored owner the scheduler fires as (never re-derived from a live session — F18)
ALTER TABLE schedules ADD COLUMN actor       TEXT NOT NULL DEFAULT 'scheduler@local';
ALTER TABLE schedules ADD COLUMN role        TEXT;                        -- owner role at schedule.set
ALTER TABLE schedules ADD COLUMN scopes_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE schedules ADD COLUMN automation  INTEGER NOT NULL DEFAULT 0;  -- 1 iff owner holds an automation grant
ALTER TABLE schedules ADD COLUMN owner_id    TEXT;                        -- created_by session/token id (NULL = system-seeded)
```

> `created_by` is the caller's **session/token id** (F4), never a `users.id` — the worker and
> `job.claim` key ownership off it; a uid-0 local caller is the reserved sentinel `local-root`
> (no `sessions` row), which can never equal a real session id.

### Migration `0003_mfa.sql` (Phase 2, forward-only)

Phase-2 MFA needs persistent TOTP replay prevention and a slot for an **un-confirmed** enrollment,
added without touching any shipped migration. `users.totp_secret` keeps its meaning (**NULL = not
enrolled**); the pending secret lives in a separate sealed column, so a half-enrolled user is still
"not enrolled" until `mfa.confirm`.

```sql
-- TOTP replay set: a time-step accepted within its window is never re-accepted (survives restart). F4/phase-2.
CREATE TABLE mfa_used (
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  step_counter INTEGER NOT NULL,        -- the TOTP time-step that was consumed
  used_at      INTEGER NOT NULL,
  PRIMARY KEY (user_id, step_counter)
);
-- pending enrollment held between mfa.enroll and mfa.confirm; sealed (F12); bounded expiry.
ALTER TABLE users ADD COLUMN totp_pending    TEXT;     -- sealed; NULL = no enrollment in progress
ALTER TABLE users ADD COLUMN totp_pending_at INTEGER;  -- enrollment start (ms); expired pending is treated as absent
```

- **`mfa.enroll`** seals the new secret into `totp_pending` (+`totp_pending_at`), replacing any prior
  **unconfirmed** pending; it **never** overwrites an active `totp_secret` (enroll on an already-enrolled
  user is refused — `mfa.disable` first). **`mfa.confirm`** verifies a TOTP against `totp_pending`, then
  **atomically** moves it into `totp_secret` and clears both pending columns. An expired `totp_pending`
  is treated as absent.
- `mfa_used` rows are pruned beyond the accepted skew window. Both `totp_secret` and `totp_pending` are
  agent-sealed (F12).

## Phase 3 — sites, domains, certs

```sql
CREATE TABLE sites (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL UNIQUE,  -- ^[a-z][a-z0-9-]{1,30}$ ; == the unix user name
  type         TEXT NOT NULL CHECK (type IN ('static','php','wordpress')),
  stack_driver TEXT NOT NULL DEFAULT 'nginx',
  docroot      TEXT NOT NULL,         -- /srv/sites/<name>/public
  php_version  TEXT,                  -- NULL for static
  force_https  INTEGER NOT NULL DEFAULT 0,
  hsts_max_age INTEGER NOT NULL DEFAULT 0,  -- 0 = no HSTS; set by site.update (owner), only served in render state 3
  cert_source  TEXT NOT NULL DEFAULT 'none' CHECK (cert_source IN ('none','letsencrypt','manual')),
  ssl_auto     INTEGER NOT NULL DEFAULT 1,  -- Let's Encrypt auto-renew; always ignored for a manual cert
  maintenance  INTEGER NOT NULL DEFAULT 0,
  status       TEXT NOT NULL DEFAULT 'creating' CHECK (status IN ('creating','active','failed','deleting')),  -- lifecycle (F6); site.list/get return it
  config_hash  TEXT,                  -- hash of the last rendered nginx config (skip-reload compare)
  created      INTEGER NOT NULL,
  updated      INTEGER NOT NULL
);

CREATE TABLE domains (
  id         TEXT PRIMARY KEY,
  site_id    TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  fqdn       TEXT NOT NULL UNIQUE,
  is_primary INTEGER NOT NULL DEFAULT 0,
  cert_state TEXT NOT NULL DEFAULT 'none' CHECK (cert_state IN ('none','issued','expired')),
  cert_staging INTEGER NOT NULL DEFAULT 0,  -- 1 = cert is from the ACME staging CA → NOT valid for force-HTTPS
  cert_not_after INTEGER,
  created    INTEGER NOT NULL
);
CREATE INDEX idx_domains_site ON domains(site_id);

CREATE TABLE redirects (
  id       TEXT PRIMARY KEY,
  site_id  TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  source   TEXT NOT NULL,            -- path or host
  target   TEXT NOT NULL,
  code     INTEGER NOT NULL CHECK (code IN (301,302))
);
```
ACME account key + registration are stored in `secrets` (name `acme:account`). TLS cert/key live on
disk under `tls_dir/<fqdn>/` (versioned dir + symlink swap, B-D9), not in the DB.

## Phase 4 databases, PHP limits and bounded recovery

Reserve `0006_phase4.sql` only if still the next unused migration at authorized implementation time; otherwise allocate the next number without editing 0001 through 0005. Register its actual computed hash in hashes.json. Replace the unimplemented Phase 4 DDL section with this DDL. Existing sites and shipped migrations remain unchanged.

```sql
CREATE TABLE site_php_limits (
  site_id TEXT PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
  memory_mb INTEGER NOT NULL DEFAULT 256 CHECK(memory_mb BETWEEN 32 AND 2048),
  upload_mb INTEGER NOT NULL DEFAULT 64 CHECK(upload_mb BETWEEN 1 AND 1024),
  max_exec_sec INTEGER NOT NULL DEFAULT 60 CHECK(max_exec_sec BETWEEN 1 AND 600),
  CHECK(upload_mb <= memory_mb)
);
CREATE TABLE databases (
  id TEXT PRIMARY KEY,
  site_id TEXT REFERENCES sites(id) ON DELETE SET NULL,
  name TEXT NOT NULL UNIQUE,
  engine TEXT NOT NULL DEFAULT 'mariadb' CHECK(engine='mariadb'),
  primary_user_id TEXT NOT NULL UNIQUE,
  created INTEGER NOT NULL,
  UNIQUE(id,site_id),
  FOREIGN KEY(primary_user_id,id) REFERENCES db_users(id,db_id)
    DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE db_users (
  id TEXT PRIMARY KEY,
  db_id TEXT NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
  username TEXT NOT NULL UNIQUE,
  host TEXT NOT NULL DEFAULT '127.0.0.1' CHECK(host='127.0.0.1'),
  created INTEGER NOT NULL,
  UNIQUE(id,db_id)
);
CREATE TABLE wordpress_bindings (
  site_id TEXT PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
  db_id TEXT NOT NULL UNIQUE,
  FOREIGN KEY(db_id,site_id) REFERENCES databases(id,site_id)
);
CREATE TABLE phase4_operations (
  job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE RESTRICT,
  kind TEXT NOT NULL CHECK(kind IN ('database_create','database_rotate','dbuser_create','wordpress_install','site_delete','database_delete','dbuser_delete')),
  site_id TEXT,
  db_id TEXT,
  checkpoint TEXT NOT NULL CHECK(checkpoint IN ('prepared','db_applied','config_published','verified','done','expired')),
  identities_json TEXT NOT NULL,
  reconciled INTEGER NOT NULL DEFAULT 0 CHECK(reconciled IN (0,1)),
  nonce BLOB,
  ciphertext BLOB,
  expires INTEGER NOT NULL,
  updated INTEGER NOT NULL,
  CHECK((nonce IS NULL)=(ciphertext IS NULL)),
  CHECK(checkpoint NOT IN ('done','expired') OR ciphertext IS NULL)
);
CREATE INDEX idx_phase4_operations_expiry ON phase4_operations(expires,job_id);
```

The deferred composite FK requires database plus primary user insertion in one transaction and forbids cross-database primary binding, primary-user deletion, or changing the binding to a missing user. The WordPress binding requires that the database belongs to that site. Create binding only after installation verifies consistency; a site in creating/failed state is not an installed WordPress app. Validate type=wordpress and immutable binding in typed store transactions. WordPress creation inserts its PHP limits defaults. php.set merges only supplied fields into persisted values, validates the full merged tuple, atomically persists after verified pool publication, and restores the previous desired state on failure. site.update.php_version delegates to that same path; no second PHP policy. Reject php.set on static sites. Pure reads never infer settings by parsing pool files. Memory also bounds post_max_size/upload_max_filesize; post_max_size equals upload_mb and upload_max_filesize equals upload_mb. pm.max_children is fixed at 2 for this bounded phase, not a caller-controlled value; raising it needs a separate deployment-capacity decision.

No plaintext password column is added. The operation payload is F12-sealed and contains generated credentials, salts, bound identities and desired protected-config bytes or equivalent typed fields. site_id/db_id are historical operation identifiers, deliberately not cascading FKs. The journal cannot disappear when its resource is removed. Nonsecret checkpoints are `prepared`, `db_applied`, `config_published`, `verified`, `done`, `expired`; delete uses `prepared`, `db_applied`, `done`. There is no separately committed claim_published state. Each mutation first ensures the recorded postcondition, then records its checkpoint, so a crash between effect and checkpoint reuses the same credential and identifiers. A DB transaction is not held during external IO. Commit verified metadata, completed job state/result, F19 sealed claim, journal done and recovery nonce/ciphertext removal in one SQLite transaction. For deletes the same transaction omits the claim. Rollback exposes none of these changes. Integrate this typed terminal method with the existing completion-audit boundary and preserve its consumed-claim exception. A done operation never republishes a consumed/expired claim. This requires a typed atomic store method, not separate best-effort writes.

Set operation expiry at preparation +24 hours, not extended by retries. Before each external mutation/restart, check expiry using the injected clock. Active operation records block conflicting mutation admission even while a crashed/failed job awaits recovery. Do not permit jobs retention deletion while a journal references it. Clear nonce/ciphertext at successful claim publication, or at verified terminal cleanup for jobs without claims. On startup before mutation admission opens, and once per minute thereafter, select at most 100 expired unfinished operations with the SQL below. In one transaction per bounded batch, clear their nonce/ciphertext, set checkpoint=expired and fail only their still-unfinished jobs with code=interrupted and a sanitized expiry message. Publish no claim. Never alter a done operation or completed job. Expired, unreconciled operations block only their conflicting resources until an authorized reconcile verifies a consistent or explicitly failed non-serving disposition and sets reconciled=1. Done and reconciled expired tombstones do not block admission. Use precondition_failed for bound-DB/primary-user refusals, not_found for unknown initial targets, conflict for ownership/membership/collision changes, locked for active conflicting operation, interrupted for expired recovery, and agent_error for integrity/custody failures. Ciphertext removal is logical erasure; WAL/backups may retain old encrypted bytes, never plaintext. Keep bounded nonsecret tombstones until normal job retention removes them via explicit deletion ordering, after reconciliation where needed. Expiry during inconsistent DB/config state leaves the site failed and non-serving; a fresh authorized rotation with a new sealed password must reconcile DB/config before reactivation. Do not delete the last recoverable ciphertext early while continuing the old operation. No permanent credential vault, automatic renewed expiry or rollback to guessed credentials.

`identities_json` is canonical, validated nonsecret JSON bound immutably to job_id and kind. It contains the resource IDs, generated MariaDB names and exact account username/host pairs, original optional site ID and deletion flags. Do not include credentials. `database_delete` snapshots its DB and every managed account; `dbuser_delete` snapshots its parent DB, target account and primary/binding refusal checks. `site_delete` snapshots all attached managed DB/accounts under its site lock before effects. Retain this same journal after deleting resource rows. A restart may ensure absence only of these recorded identities; never re-resolve an unknown name to adopt another resource. No second scheduler or permanent secret store. Delete checkpoints commit after each ensure-absent effect, and final resource-row cleanup, job done and journal done commit atomically. No claim is created for deletion.

Expiry query, bound now is injected unix-ms, with the terminal filter applied before the limit:

```sql
SELECT job_id FROM phase4_operations INDEXED BY idx_phase4_operations_expiry
WHERE expires <= ? AND checkpoint NOT IN ('done','expired')
ORDER BY expires,job_id LIMIT 100;
```

Preparation expiry is exactly now + 86400000 ms. Claim delivery TTL starts at actual atomic publication per F19, not preparation. An expired operation cannot continue old external mutations. A fresh authorized operation may reconcile only after resolving the old blocker under the complete lock set.

## Phase 5 — deploys, cron, ssh

```sql
CREATE TABLE deploys (
  id        TEXT PRIMARY KEY,
  site_id   TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  repo_url  TEXT NOT NULL,
  branch    TEXT NOT NULL,
  sha       TEXT,                    -- the pinned, deployed commit
  state     TEXT NOT NULL,
  created   INTEGER NOT NULL
);
CREATE TABLE cron_jobs (
  id        TEXT PRIMARY KEY,
  site_id   TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  schedule  TEXT NOT NULL,          -- OnCalendar form
  argv_json TEXT NOT NULL,          -- validated argv array
  unit_name TEXT NOT NULL,          -- novapanel-cron-<site>-<id>
  enabled   INTEGER NOT NULL DEFAULT 1,
  created   INTEGER NOT NULL
);
```
A per-site deploy key is stored sealed (`secrets` name `deploy:<site_id>`). SSH public keys live in
the site user's `authorized_keys` on disk, not the DB.

## Phase 6 — backups, firewall

```sql
CREATE TABLE repos (
  id        TEXT PRIMARY KEY,
  name      TEXT NOT NULL UNIQUE,
  kind      TEXT NOT NULL CHECK (kind IN ('local','sftp','s3')),  -- B2 via s3-compat endpoint
  role      TEXT NOT NULL DEFAULT 'site' CHECK (role IN ('site','panel')), -- exactly one 'panel' repo (self-backup)
  location  TEXT NOT NULL,          -- restic repo string
  retention_json TEXT NOT NULL DEFAULT '{"keep_daily":7,"keep_weekly":4,"keep_monthly":6}',
  created   INTEGER NOT NULL
);  -- repo password + object-store/sftp creds stored sealed (secrets names repo:<id>:password / repo:<id>:s3 / repo:<id>:sftp)
CREATE TABLE webhook_deliveries (    -- replay seen-set for deploy.hook (B-D1); pruned past the window
  id        TEXT PRIMARY KEY,        -- hook_id || ':' || signature
  seen_at   INTEGER NOT NULL
);
CREATE TABLE backups (
  id          TEXT PRIMARY KEY,
  repo_id     TEXT NOT NULL REFERENCES repos(id),
  target_kind TEXT NOT NULL CHECK (target_kind IN ('site','db','panel')),
  target_ref  TEXT NOT NULL,
  snapshot_id TEXT,                 -- restic snapshot id
  bytes       INTEGER,
  state       TEXT NOT NULL,
  created     INTEGER NOT NULL
);
CREATE TABLE fw_pending (            -- an armed firewall change awaiting confirm (B-D7)
  id          TEXT PRIMARY KEY,
  snapshot_path TEXT NOT NULL,      -- captured ruleset to revert to
  revert_unit TEXT NOT NULL,        -- the transient systemd timer unit name
  deadline    INTEGER NOT NULL,
  confirmed   INTEGER NOT NULL DEFAULT 0
);
```

## Phase 7 — integrations (DNS)

```sql
CREATE TABLE integrations (
  id        TEXT PRIMARY KEY,
  kind      TEXT NOT NULL,          -- 'cloudflare'
  name      TEXT NOT NULL,
  config_json TEXT NOT NULL,        -- non-secret (zone ids, etc.)
  created   INTEGER NOT NULL
);  -- provider API token stored sealed (secrets name integration:<id>:token)
CREATE TABLE dns_zones (
  id            TEXT PRIMARY KEY,
  integration_id TEXT NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
  zone          TEXT NOT NULL,
  provider_zone_id TEXT NOT NULL
);
```

## Phase 8 — webhooks

```sql
CREATE TABLE webhooks (
  id        TEXT PRIMARY KEY,       -- the <hook_id> in the public URL
  site_id   TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  branch    TEXT NOT NULL,
  created   INTEGER NOT NULL
);  -- per-hook HMAC secret stored sealed (secrets name webhook:<id>)
```

## Phase 9 — modules

```sql
CREATE TABLE module_state (
  name      TEXT PRIMARY KEY,       -- 'ols', 'mail', 'powerdns', 'ftp', 'docker'
  enabled   INTEGER NOT NULL DEFAULT 0,
  config_json TEXT NOT NULL DEFAULT '{}',
  updated   INTEGER NOT NULL
);
```

## Migration integrity test (A6)

A unit test (runs in CI, no root): applies `0001..N` to a fresh in-memory/temp DB and asserts success;
recomputes each file's sha256 and compares to `schema_migrations.hash` fixtures committed alongside, so
editing a shipped migration fails the build. New schema always arrives as a new `NNNN_*.sql`.
