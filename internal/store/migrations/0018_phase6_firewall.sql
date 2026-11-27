CREATE TABLE fw_pending (
  id TEXT PRIMARY KEY,
  snapshot_path TEXT NOT NULL,
  revert_unit TEXT NOT NULL,
  deadline INTEGER NOT NULL,
  confirmed INTEGER NOT NULL DEFAULT 0
);
