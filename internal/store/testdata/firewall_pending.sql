CREATE TABLE fw_pending (            -- an armed firewall change awaiting confirm (B-D7)
  id          TEXT PRIMARY KEY,
  snapshot_path TEXT NOT NULL,      -- captured ruleset to revert to
  revert_unit TEXT NOT NULL,        -- the transient systemd timer unit name
  deadline    INTEGER NOT NULL,
  confirmed   INTEGER NOT NULL DEFAULT 0
);
