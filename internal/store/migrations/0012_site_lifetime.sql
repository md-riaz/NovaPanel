ALTER TABLE sites ADD COLUMN lifetime_state TEXT NOT NULL DEFAULT 'served' CHECK(lifetime_state IN ('provisioning','served'));
CREATE TRIGGER site_lifetime_no_rewind BEFORE UPDATE OF lifetime_state ON sites WHEN OLD.lifetime_state = 'served' AND NEW.lifetime_state <> 'served' BEGIN SELECT RAISE(ABORT,'site lifetime cannot rewind'); END;
