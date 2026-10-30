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
