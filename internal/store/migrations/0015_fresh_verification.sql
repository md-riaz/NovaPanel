CREATE TABLE phase4_operations_new (
  job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE RESTRICT,
  kind TEXT NOT NULL CHECK(kind IN ('database_create','database_rotate','dbuser_create','wordpress_install','site_delete','database_delete','dbuser_delete','php_set','php_site_create')),
  site_id TEXT,
  db_id TEXT,
  checkpoint TEXT NOT NULL CHECK(checkpoint IN ('prepared','teardown_started','db_applied','verification_in_progress','verified_resumable','security_failed','config_published','verified','done','expired')),
  identities_json TEXT NOT NULL,
  reconciled INTEGER NOT NULL DEFAULT 0 CHECK(reconciled IN (0,1)),
  nonce BLOB,
  ciphertext BLOB,
  expires INTEGER NOT NULL,
  updated INTEGER NOT NULL,
  CHECK((nonce IS NULL)=(ciphertext IS NULL)),
  CHECK(checkpoint <> 'done' OR ciphertext IS NULL)
);
INSERT INTO phase4_operations_new SELECT * FROM phase4_operations;
DROP TABLE phase4_operations;
ALTER TABLE phase4_operations_new RENAME TO phase4_operations;
CREATE INDEX idx_phase4_operations_expiry ON phase4_operations(expires,job_id);

