CREATE TABLE dns_zones (
  id            TEXT PRIMARY KEY,
  integration_id TEXT NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
  zone          TEXT NOT NULL UNIQUE,
  provider_zone_id TEXT NOT NULL,
  created       INTEGER NOT NULL
);
CREATE TABLE dns_records (
  obj_id             TEXT PRIMARY KEY,            -- row ULID = F20 provenance identity (never name/existence)
  zone_id            TEXT NOT NULL REFERENCES dns_zones(id) ON DELETE CASCADE,
  type               TEXT NOT NULL,               -- A|AAAA|CNAME|TXT|MX
  name               TEXT NOT NULL,               -- fqdn
  value              TEXT NOT NULL,
  ttl                INTEGER NOT NULL,            -- 1 = auto
  priority           INTEGER NOT NULL DEFAULT 0,  -- MX only
  proxied            INTEGER NOT NULL DEFAULT 0,  -- A/AAAA/CNAME only (server-guarded)
  provider_record_id TEXT,                        -- provider id, recorded after create confirms
  phase              TEXT NOT NULL,               -- reserved|created|updating|deleting (F20 provenance phase)
  reconciled         INTEGER NOT NULL DEFAULT 0,
  conflict_error     TEXT,
  created            INTEGER NOT NULL,
  updated            INTEGER NOT NULL
);  -- F20 provider-side provenance = obj_id stamped in the record comment (novapanel:obj_id=<obj_id>)
