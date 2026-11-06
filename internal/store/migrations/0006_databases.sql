CREATE TABLE databases (
  id TEXT PRIMARY KEY,
  site_id TEXT REFERENCES sites(id) ON DELETE SET NULL,
  name TEXT NOT NULL UNIQUE,
  engine TEXT NOT NULL DEFAULT 'mariadb',
  created INTEGER NOT NULL
);
CREATE TABLE db_users (
  id TEXT PRIMARY KEY,
  db_id TEXT NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
  username TEXT NOT NULL UNIQUE,
  host TEXT NOT NULL DEFAULT '127.0.0.1',
  owner INTEGER NOT NULL DEFAULT 0 CHECK (owner IN (0,1)),
  created INTEGER NOT NULL
);
CREATE UNIQUE INDEX db_users_one_owner ON db_users(db_id) WHERE owner=1;
