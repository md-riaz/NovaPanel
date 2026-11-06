-- Phase 6 production schema after the immutable Phase 4 schema predecessor.
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
CREATE UNIQUE INDEX repos_one_panel ON repos(role) WHERE role='panel';
