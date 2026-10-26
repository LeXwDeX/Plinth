CREATE TABLE generation_balance_snapshots (
    id          TEXT PRIMARY KEY,
    balance     REAL NOT NULL,
    captured_at TEXT NOT NULL
);

CREATE INDEX generation_balance_snapshots_captured_at_idx
    ON generation_balance_snapshots(captured_at);
