ALTER TABLE generation_balance_snapshots
    ADD COLUMN account_id TEXT NOT NULL DEFAULT '';

CREATE INDEX generation_balance_snapshots_account_captured_at_idx
    ON generation_balance_snapshots(account_id, captured_at);
