ALTER TABLE generation_attempts ADD COLUMN last_observation_sequence INTEGER NOT NULL DEFAULT 0;
ALTER TABLE generation_attempts ADD COLUMN last_observation_at TEXT;
ALTER TABLE generation_attempts ADD COLUMN last_observation_phase TEXT NOT NULL DEFAULT '';
ALTER TABLE generation_attempts ADD COLUMN observation_failures INTEGER NOT NULL DEFAULT 0;
