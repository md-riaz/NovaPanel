CREATE TABLE deploys (
  id TEXT PRIMARY KEY,
  site_id TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  repo_url TEXT NOT NULL,
  branch TEXT NOT NULL,
  sha TEXT,
  state TEXT NOT NULL,
  created INTEGER NOT NULL
);
CREATE TABLE deploy_config (
  site_id TEXT PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
  repo_url TEXT NOT NULL,
  branch TEXT NOT NULL,
  public_key TEXT NOT NULL,
  created INTEGER NOT NULL,
  updated INTEGER NOT NULL
);
