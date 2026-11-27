CREATE TABLE site_account_provenance_new (
  site_id TEXT PRIMARY KEY REFERENCES sites(id) ON DELETE SET NULL,
  account_name TEXT NOT NULL UNIQUE CHECK(length(account_name)>0),
  uid INTEGER NOT NULL UNIQUE CHECK(uid>0),
  gid INTEGER NOT NULL UNIQUE CHECK(gid>0),
  allocating_job TEXT NOT NULL UNIQUE REFERENCES jobs(id) ON DELETE RESTRICT,
  evidence TEXT NOT NULL CHECK(length(evidence)>0)
);
INSERT INTO site_account_provenance_new SELECT * FROM site_account_provenance;
DROP TABLE site_account_provenance;
ALTER TABLE site_account_provenance_new RENAME TO site_account_provenance;
CREATE TRIGGER site_account_provenance_no_update BEFORE UPDATE ON site_account_provenance
WHEN NOT (OLD.site_id IS NOT NULL AND NEW.site_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM sites WHERE id=OLD.site_id)
  AND NEW.account_name IS OLD.account_name AND NEW.uid IS OLD.uid AND NEW.gid IS OLD.gid
  AND NEW.allocating_job IS OLD.allocating_job AND NEW.evidence IS OLD.evidence)
BEGIN SELECT RAISE(ABORT,'immutable provenance'); END;
CREATE TRIGGER site_account_provenance_no_delete BEFORE DELETE ON site_account_provenance BEGIN SELECT RAISE(ABORT,'retained provenance'); END;
