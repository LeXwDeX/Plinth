package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestUpdatePersonalSkillPreservesCreationTime(t *testing.T) {
	ctx := context.Background()
	repo, err := Open(filepath.Join(t.TempDir(), "personal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	skill := catalog.SkillSchema{ID: "personal-a", Name: "Before", Prompt: "old"}
	if err := repo.InsertPersonalSkill(ctx, "owner", skill, 1); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := repo.db.QueryRowContext(ctx, `SELECT created_at FROM personal_skills WHERE id=?`, skill.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	skill.Name, skill.Prompt = "After", "new"
	if err := repo.UpdatePersonalSkill(ctx, "other", skill); !errors.Is(err, agentconfiguration.ErrPersonalSkillNotFound) {
		t.Fatalf("cross owner: %v", err)
	}
	if err := repo.UpdatePersonalSkill(ctx, "owner", skill); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRowContext(ctx, `SELECT created_at FROM personal_skills WHERE id=?`, skill.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("created_at changed: %s -> %s", before, after)
	}
	items, err := repo.ListPersonalSkills(ctx, "owner")
	if err != nil || len(items) != 1 || items[0].ID != skill.ID || items[0].Name != "After" || items[0].Prompt != "new" {
		t.Fatalf("updated record: %#v %v", items, err)
	}
}
