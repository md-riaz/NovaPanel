-- jobs: persist the full authority snapshot captured at the agent boundary (F4/F6/F8 ruling)
ALTER TABLE jobs      ADD COLUMN scopes_json TEXT NOT NULL DEFAULT '[]'; -- token scopes at enqueue ([] for user/local-root)
ALTER TABLE jobs      ADD COLUMN automation  INTEGER NOT NULL DEFAULT 0; -- 1 iff an automation-grant token enqueued it

-- schedules: the stored owner the scheduler fires as (never re-derived from a live session — F18)
ALTER TABLE schedules ADD COLUMN actor       TEXT NOT NULL DEFAULT 'scheduler@local';
ALTER TABLE schedules ADD COLUMN role        TEXT;                        -- owner role at schedule.set
ALTER TABLE schedules ADD COLUMN scopes_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE schedules ADD COLUMN automation  INTEGER NOT NULL DEFAULT 0;  -- 1 iff owner holds an automation grant
ALTER TABLE schedules ADD COLUMN owner_id    TEXT;                        -- created_by session/token id (NULL = system-seeded)
