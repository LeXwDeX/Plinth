-- Existing unbound agent grants have no authenticated turn binding and cannot
-- admit a new Task after this migration. Bound Task replay remains available.
ALTER TABLE execution_grants ADD COLUMN trigger_turn_id TEXT NOT NULL DEFAULT '';
CREATE INDEX execution_grants_trigger_turn ON execution_grants(trigger_turn_id)
    WHERE trigger_turn_id <> '';
