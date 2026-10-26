CREATE TABLE IF NOT EXISTS personal_skills (
 id TEXT PRIMARY KEY,
 owner_id TEXT NOT NULL,
 skill_json TEXT NOT NULL,
 created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_personal_skills_owner_created
 ON personal_skills(owner_id, created_at, id);
