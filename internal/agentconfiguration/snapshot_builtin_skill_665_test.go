package agentconfiguration_test

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #665: a Schema upgrade that a packaged Skill no longer satisfies skips that
// Skill instead of failing every new turn; a Skill an administrator wrote that
// is invalid still fails the snapshot.
func TestSnapshotSkipsAnInvalidBuiltInSkillButNotAUserSkill665(t *testing.T) {
	ctx := context.Background()
	f := newFollowFixture(t)
	base, err := f.service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	retired, err := catalog.RetiredBuiltInSkills()
	if err != nil || len(retired) == 0 {
		t.Fatalf("retired built-in Skills: %v", err)
	}
	// A packaged Skill (here one the product shipped and retired) whose stored
	// content an upgraded Schema no longer accepts.
	brokenID := retired[0].ID
	broken := base.Configuration
	broken.Skills = append(append([]catalog.SkillSchema(nil), base.Configuration.Skills...),
		catalog.SkillSchema{ID: brokenID, Name: "Packaged", Category: "category-retired-by-an-upgrade", Prompt: "x"})
	stored, err := f.repo.AppendAgentConfiguration(ctx, base.Revision, agentconfiguration.Version{
		Snapshot:  agentconfiguration.Snapshot{Configuration: broken},
		CreatedAt: time.Now().UTC(), ActorID: "admin", Source: "publish"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.service.Snapshot(ctx)
	if err != nil {
		t.Fatalf("an invalid packaged Skill failed the snapshot: %v", err)
	}
	if snapshot.Revision != stored.Revision {
		t.Fatalf("snapshot revision = %s, want %s", snapshot.Revision, stored.Revision)
	}
	for _, skill := range snapshot.Configuration.Skills {
		if skill.ID == brokenID {
			t.Fatalf("the invalid packaged Skill %s was kept", brokenID)
		}
	}
	if len(snapshot.Configuration.Skills) != len(base.Configuration.Skills) {
		t.Fatalf("skills = %d, want every other Skill kept", len(snapshot.Configuration.Skills))
	}

	user := snapshot.Configuration
	user.Skills = append(append([]catalog.SkillSchema(nil), snapshot.Configuration.Skills...),
		catalog.SkillSchema{ID: "admin-written", Name: "Mine", Category: "category-retired-by-an-upgrade", Prompt: "x"})
	if _, err := f.repo.AppendAgentConfiguration(ctx, stored.Revision, agentconfiguration.Version{
		Snapshot:  agentconfiguration.Snapshot{Configuration: user},
		CreatedAt: time.Now().UTC(), ActorID: "admin", Source: "publish"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Snapshot(ctx); err == nil {
		t.Fatal("an invalid administrator Skill passed the snapshot")
	}
}
