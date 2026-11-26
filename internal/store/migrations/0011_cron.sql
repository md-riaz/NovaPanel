CREATE TABLE cron_jobs (
  id TEXT PRIMARY KEY,
  site_id TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  schedule TEXT NOT NULL,
  argv_json TEXT NOT NULL,
  unit_name TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created INTEGER NOT NULL
);
