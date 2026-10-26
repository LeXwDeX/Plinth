ALTER TABLE generation_artifact_claims
    ADD COLUMN variant TEXT NOT NULL DEFAULT '';

ALTER TABLE generation_assets
    ADD COLUMN variant TEXT NOT NULL DEFAULT '';
