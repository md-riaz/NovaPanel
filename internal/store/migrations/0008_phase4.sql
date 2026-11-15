CREATE UNIQUE INDEX phase4_database_site ON databases(id,site_id);
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
  checkpoint TEXT NOT NULL CHECK(checkpoint IN ('prepared','teardown_started','db_applied','config_published','verified','done','expired')),
  identities_json TEXT NOT NULL,
  reconciled INTEGER NOT NULL DEFAULT 0 CHECK(reconciled IN (0,1)),
  nonce BLOB,
  ciphertext BLOB,
  expires INTEGER NOT NULL,
  updated INTEGER NOT NULL,
  CHECK((nonce IS NULL)=(ciphertext IS NULL)),
  CHECK(checkpoint <> 'done' OR ciphertext IS NULL)
);
CREATE INDEX idx_phase4_operations_expiry ON phase4_operations(expires,job_id);
