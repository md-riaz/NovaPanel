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
