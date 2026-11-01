CREATE TABLE sites (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL UNIQUE,
  type         TEXT NOT NULL CHECK (type IN ('static','php','wordpress')),
  stack_driver TEXT NOT NULL DEFAULT 'nginx',
  docroot      TEXT NOT NULL,
  php_version  TEXT,
  force_https  INTEGER NOT NULL DEFAULT 0,
  hsts_max_age INTEGER NOT NULL DEFAULT 0,
  cert_source  TEXT NOT NULL DEFAULT 'none' CHECK (cert_source IN ('none','letsencrypt','manual')),
  ssl_auto     INTEGER NOT NULL DEFAULT 1,
  maintenance  INTEGER NOT NULL DEFAULT 0,
  status       TEXT NOT NULL DEFAULT 'creating' CHECK (status IN ('creating','active','failed','deleting')),
  config_hash  TEXT,
  created      INTEGER NOT NULL,
  updated      INTEGER NOT NULL
);

CREATE TABLE domains (
  id         TEXT PRIMARY KEY,
  site_id    TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  fqdn       TEXT NOT NULL UNIQUE,
  is_primary INTEGER NOT NULL DEFAULT 0,
  cert_state TEXT NOT NULL DEFAULT 'none' CHECK (cert_state IN ('none','issued','expired')),
  cert_staging INTEGER NOT NULL DEFAULT 0,
  cert_not_after INTEGER,
  created    INTEGER NOT NULL
);
CREATE INDEX idx_domains_site ON domains(site_id);

CREATE TABLE redirects (
  id       TEXT PRIMARY KEY,
  site_id  TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  source   TEXT NOT NULL,
  target   TEXT NOT NULL,
  code     INTEGER NOT NULL CHECK (code IN (301,302))
);
