package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// The count and insert run in one SQLite statement. Concurrent writers cannot
// exceed the per-owner cap by observing the same earlier count.
func (r *Repository) InsertPersonalSkill(ctx context.Context, owner string, skill catalog.SkillSchema, max int) error {
	encoded, err := json.Marshal(skill)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO personal_skills(id,owner_id,skill_json,created_at)
		 SELECT ?,?,?,? WHERE (SELECT COUNT(*) FROM personal_skills WHERE owner_id=?) < ?`,
		skill.ID, owner, string(encoded), time.Now().UTC().Format(time.RFC3339Nano), owner, max)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return agentconfiguration.ErrPersonalSkillLimit
	}
	return nil
}

// DeletePersonalSkill removes one Skill only when the owner created it.
// Turns that already froze the Skill keep their own snapshot.
func (r *Repository) DeletePersonalSkill(ctx context.Context, owner, id string) error {
	if owner == "" || id == "" {
		return agentconfiguration.ErrPersonalSkillNotFound
	}
	result, err := r.db.ExecContext(ctx, `DELETE FROM personal_skills WHERE owner_id=? AND id=?`, owner, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return agentconfiguration.ErrPersonalSkillNotFound
	}
	return nil
}

func (r *Repository) ListPersonalSkills(ctx context.Context, owner string) ([]catalog.SkillSchema, error) {
	if owner == "" {
		return nil, errors.New("personal Skill owner is empty")
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT skill_json FROM personal_skills WHERE owner_id=? ORDER BY created_at,id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []catalog.SkillSchema{}
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		var skill catalog.SkillSchema
		if err := json.Unmarshal([]byte(encoded), &skill); err != nil {
			return nil, err
		}
		result = append(result, skill)
	}
	return result, rows.Err()
}
