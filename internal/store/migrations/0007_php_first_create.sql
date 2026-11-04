CREATE TABLE phase4_operations_new (
  job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE RESTRICT,
  kind TEXT NOT NULL CHECK(kind IN ('database_create','database_rotate','dbuser_create','wordpress_install','site_delete','database_delete','dbuser_delete','php_set','php_site_create')),
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
INSERT INTO phase4_operations_new SELECT * FROM phase4_operations;
DROP TABLE phase4_operations;
ALTER TABLE phase4_operations_new RENAME TO phase4_operations;
CREATE INDEX idx_phase4_operations_expiry ON phase4_operations(expires,job_id);

CREATE TABLE php_create_reservations (
  job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE RESTRICT,
  site_id TEXT NOT NULL UNIQUE,
  site_name TEXT NOT NULL UNIQUE,
  domain_id TEXT NOT NULL UNIQUE,
  fqdn TEXT NOT NULL UNIQUE
);
CREATE TABLE site_account_provenance (
  site_id TEXT PRIMARY KEY REFERENCES sites(id) ON DELETE RESTRICT,
  account_name TEXT NOT NULL UNIQUE CHECK(length(account_name)>0),
  uid INTEGER NOT NULL UNIQUE CHECK(uid>0),
  gid INTEGER NOT NULL UNIQUE CHECK(gid>0),
  allocating_job TEXT NOT NULL UNIQUE REFERENCES jobs(id) ON DELETE RESTRICT,
  evidence TEXT NOT NULL CHECK(length(evidence)>0)
);
CREATE TRIGGER site_account_provenance_no_update BEFORE UPDATE ON site_account_provenance BEGIN SELECT RAISE(ABORT,'immutable provenance'); END;
CREATE TRIGGER site_account_provenance_no_delete BEFORE DELETE ON site_account_provenance BEGIN SELECT RAISE(ABORT,'retained provenance'); END;
