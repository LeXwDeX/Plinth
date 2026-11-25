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

// UpdatePersonalSkill replaces one row only when the owner created it. The
// created_at column is untouched, so list order and identity stay stable.
func (r *Repository) UpdatePersonalSkill(ctx context.Context, owner, id string, skill catalog.SkillSchema) error {
	if owner == "" || id == "" {
		return agentconfiguration.ErrPersonalSkillNotFound
	}
	if skill.ID != id {
		return errors.New("personal Skill update ID mismatch")
	}
	encoded, err := json.Marshal(skill)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE personal_skills SET skill_json=? WHERE owner_id=? AND id=?`, string(encoded), owner, id)
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

// ModifyPersonalSkill reads the owner's Skill and their other Skills, applies
// change and writes the result back in one transaction (#548). The write is
// also conditional on the row still holding what was read, so a concurrent
// change is never overwritten with a merge of stale content.
func (r *Repository) ModifyPersonalSkill(
	ctx context.Context, owner, id string,
	change func(current catalog.SkillSchema, others []catalog.SkillSchema) (catalog.SkillSchema, error),
) (catalog.SkillSchema, error) {
	if owner == "" || id == "" {
		return catalog.SkillSchema{}, agentconfiguration.ErrPersonalSkillNotFound
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return catalog.SkillSchema{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, skill_json FROM personal_skills WHERE owner_id=? ORDER BY created_at,id`, owner)
	if err != nil {
		return catalog.SkillSchema{}, err
	}
	var current catalog.SkillSchema
	var stored string
	found := false
	others := []catalog.SkillSchema{}
	for rows.Next() {
		var rowID, encoded string
		if err := rows.Scan(&rowID, &encoded); err != nil {
			rows.Close()
			return catalog.SkillSchema{}, err
		}
		var skill catalog.SkillSchema
		if err := json.Unmarshal([]byte(encoded), &skill); err != nil {
			rows.Close()
			return catalog.SkillSchema{}, err
		}
		if rowID == id {
			current, stored, found = skill, encoded, true
			continue
		}
		others = append(others, skill)
	}
	if err := rows.Close(); err != nil {
		return catalog.SkillSchema{}, err
	}
	if err := rows.Err(); err != nil {
		return catalog.SkillSchema{}, err
	}
	if !found {
		return catalog.SkillSchema{}, agentconfiguration.ErrPersonalSkillNotFound
	}
	next, err := change(current, others)
	if err != nil {
		return catalog.SkillSchema{}, err
	}
	if next.ID != id {
		return catalog.SkillSchema{}, errors.New("personal Skill update ID mismatch")
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return catalog.SkillSchema{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE personal_skills SET skill_json=? WHERE owner_id=? AND id=? AND skill_json=?`, string(encoded), owner, id, stored)
	if err != nil {
		return catalog.SkillSchema{}, err
	}
	if count, err := result.RowsAffected(); err != nil {
		return catalog.SkillSchema{}, err
	} else if count == 0 {
		return catalog.SkillSchema{}, errors.New("personal Skill changed concurrently")
	}
	if err := tx.Commit(); err != nil {
		return catalog.SkillSchema{}, err
	}
	return next, nil
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
