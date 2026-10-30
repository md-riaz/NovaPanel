-- Trusted scheduler authority and enqueue metadata, never supplied by verb args.
ALTER TABLE schedules ADD COLUMN actor TEXT NOT NULL DEFAULT '';
ALTER TABLE schedules ADD COLUMN role TEXT NOT NULL DEFAULT '';
ALTER TABLE schedules ADD COLUMN created_by TEXT NOT NULL DEFAULT '';
ALTER TABLE schedules ADD COLUMN scopes_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE schedules ADD COLUMN automation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN scopes_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE jobs ADD COLUMN automation INTEGER NOT NULL DEFAULT 0;
