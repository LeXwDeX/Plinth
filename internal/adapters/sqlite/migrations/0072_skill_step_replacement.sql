-- A three-view slot can hold a picture the person supplied instead of the one
-- the step generated (#65). The step keeps its own attempt, grant and task as
-- they were; the replacement is an owned input asset that stands in for its
-- output, and a later replacement overwrites it.
ALTER TABLE skill_run_steps ADD COLUMN replacement_asset_id TEXT REFERENCES generation_input_assets(id) ON DELETE RESTRICT;
