CREATE TABLE phase4_operations_new (
  job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE RESTRICT,
  kind TEXT NOT NULL CHECK(kind IN ('database_create','database_rotate','dbuser_create','wordpress_install','site_delete','database_delete','dbuser_delete','php_set','php_site_create','site_update')),
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

CREATE TABLE phase4_operation_children (
  job_id TEXT NOT NULL REFERENCES phase4_operations(job_id) ON DELETE RESTRICT,
  ordinal INTEGER NOT NULL,
  child_kind TEXT NOT NULL,
  checkpoint TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('pending','committed','failed')),
  typed_error TEXT,
  identities_json TEXT NOT NULL,
  bracket_engaged INTEGER NOT NULL DEFAULT 0 CHECK(bracket_engaged IN (0,1)),
  journal_ref TEXT,
  updated INTEGER NOT NULL,
  PRIMARY KEY(job_id,ordinal)
);
CREATE TRIGGER phase4_child_no_rewind BEFORE UPDATE OF status, checkpoint, identities_json ON phase4_operation_children
  WHEN OLD.status IN ('committed','failed') AND
       (NEW.status IS NOT OLD.status OR NEW.checkpoint IS NOT OLD.checkpoint OR
        NEW.identities_json IS NOT OLD.identities_json)
  BEGIN SELECT RAISE(ABORT,'child status cannot rewind'); END;
CREATE TRIGGER phase4_bracket_no_rewind BEFORE UPDATE OF bracket_engaged ON phase4_operation_children
  WHEN OLD.bracket_engaged = 1 AND NEW.bracket_engaged <> 1
  BEGIN SELECT RAISE(ABORT,'bracket intent cannot rewind'); END;
