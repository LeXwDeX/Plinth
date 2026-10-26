CREATE TABLE legacy_migration_records (
    source_table TEXT NOT NULL,
    source_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    archived_at TEXT NOT NULL,
    PRIMARY KEY (source_table, source_id)
);

CREATE INDEX legacy_migration_records_source_table_idx
    ON legacy_migration_records (source_table, source_id);
