-- TOTP replay set: a time-step accepted within its window is never re-accepted (survives restart). F4/phase-2.
CREATE TABLE mfa_used (
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  step_counter INTEGER NOT NULL,        -- the TOTP time-step that was consumed
  used_at      INTEGER NOT NULL,
  PRIMARY KEY (user_id, step_counter)
);
-- pending enrollment held between mfa.enroll and mfa.confirm; sealed (F12); bounded expiry.
ALTER TABLE users ADD COLUMN totp_pending    TEXT;     -- sealed; NULL = no enrollment in progress
ALTER TABLE users ADD COLUMN totp_pending_at INTEGER;  -- enrollment start (ms); expired pending is treated as absent
