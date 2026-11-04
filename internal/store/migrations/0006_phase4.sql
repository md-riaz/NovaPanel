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
