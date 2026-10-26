ALTER TABLE authentication_users
    ADD COLUMN bootstrap_managed INTEGER NOT NULL DEFAULT 0;

CREATE TABLE etl_import_items (
    domain       TEXT NOT NULL,
    source_id    TEXT NOT NULL,
    migrated_at  TEXT NOT NULL,
    PRIMARY KEY(domain, source_id)
);

CREATE INDEX etl_import_items_migrated_at_idx
    ON etl_import_items(migrated_at);
