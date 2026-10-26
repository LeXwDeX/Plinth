-- This receipt is committed in the same transaction as the exact approved
-- deletion. It resolves unknown client outcomes without replaying a deletion.
CREATE TABLE generation_retention_receipts (
 manifest_sha256 TEXT PRIMARY KEY CHECK(length(manifest_sha256)=64),
 approval_nonce TEXT NOT NULL UNIQUE,
 receipt_json TEXT NOT NULL CHECK(json_valid(receipt_json))
);
CREATE TRIGGER generation_retention_receipts_immutable_update
BEFORE UPDATE ON generation_retention_receipts
BEGIN SELECT RAISE(ABORT, 'retention receipts are immutable'); END;
CREATE TRIGGER generation_retention_receipts_immutable_delete
BEFORE DELETE ON generation_retention_receipts
BEGIN SELECT RAISE(ABORT, 'retention receipts are immutable'); END;
