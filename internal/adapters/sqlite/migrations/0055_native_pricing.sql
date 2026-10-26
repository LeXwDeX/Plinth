ALTER TABLE generation_tasks ADD COLUMN native_quote_json TEXT;

-- Explicit immutable discriminator protects against a missing quote being
-- misread as a historical identity-only Prepared snapshot.
ALTER TABLE generation_prepared_inputs ADD COLUMN native_quote_required INTEGER NOT NULL DEFAULT 0 CHECK (native_quote_required IN (0,1));

-- List summaries must not load large private Offering snapshots to determine
-- whether a missing quote is historical or corrupt.
ALTER TABLE generation_tasks ADD COLUMN native_quote_required INTEGER NOT NULL DEFAULT 0 CHECK (native_quote_required IN (0,1));
CREATE TRIGGER generation_task_native_quote_immutable
BEFORE UPDATE OF native_quote_json,native_quote_required ON generation_tasks
WHEN NEW.native_quote_json IS NOT OLD.native_quote_json OR NEW.native_quote_required IS NOT OLD.native_quote_required
BEGIN SELECT RAISE(ABORT,'task native quote is immutable'); END;
