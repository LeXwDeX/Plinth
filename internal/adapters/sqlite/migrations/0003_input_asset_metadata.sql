ALTER TABLE generation_input_assets
    ADD COLUMN original_filename TEXT NOT NULL DEFAULT '';

ALTER TABLE generation_input_assets
    ADD COLUMN asset_type TEXT NOT NULL DEFAULT '';
