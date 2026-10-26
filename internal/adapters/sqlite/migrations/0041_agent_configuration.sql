CREATE TABLE IF NOT EXISTS agent_configuration_versions (
 revision INTEGER PRIMARY KEY,
 configuration_json TEXT NOT NULL,
 actor_id TEXT NOT NULL,
 source TEXT NOT NULL,
 created_at TEXT NOT NULL
);
