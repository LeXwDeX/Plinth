package agentconfiguration_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func storeWithSkill(t *testing.T, f followFixture, expected string, base agentconfiguration.Configuration, skill catalog.SkillSchema) agentconfiguration.Version {
	t.Helper()
	c := base
	c.Skills = append(append([]catalog.SkillSchema(nil), base.Skills...), skill)
	v, err := f.repo.AppendAgentConfiguration(context.Background(), expected, agentconfiguration.Version{
		Snapshot:  agentconfiguration.Snapshot{Configuration: c},
		CreatedAt: time.Now().UTC(), ActorID: "admin", Source: "publish"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// #665: a Schema upgrade that a packaged Skill no longer satisfies skips that
// Skill on every read and at startup, warns once, and is not deleted by a
// later publish; the stored version is not rewritten.
func TestInvalidPackagedSkillIsSkippedKeptAndWarnedOnce665(t *testing.T) {
	ctx := context.Background()
	f := newFollowFixture(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	base, err := f.service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	packaged := catalog.SkillSchema{ID: "packaged-665", Name: "Packaged", Category: "category-retired-by-an-upgrade", Prompt: "x"}
	f.service.MarkPackagedForTest(packaged)
	stored := storeWithSkill(t, f, base.Revision, base.Configuration, packaged)

	for range 2 {
		snapshot, err := f.service.Snapshot(ctx)
		if err != nil {
			t.Fatalf("an invalid packaged Skill failed the snapshot: %v", err)
		}
		if snapshot.Revision != stored.Revision || hasSkill(snapshot.Configuration, packaged.ID) {
			t.Fatalf("snapshot = %s with packaged Skill %v", snapshot.Revision, hasSkill(snapshot.Configuration, packaged.ID))
		}
	}
	if err := f.service.ReconcileRetiredBuiltInSkills(ctx); err != nil {
		t.Fatalf("startup reconcile failed on an invalid packaged Skill: %v", err)
	}
	if latest, _ := f.repo.LatestAgentConfiguration(ctx); latest.Revision != stored.Revision {
		t.Fatalf("reconcile rewrote the version: %s", latest.Revision)
	}
	if n := strings.Count(logs.String(), "packaged-665"); n != 1 {
		t.Fatalf("skip warning logged %d times, want once:\n%s", n, logs.String())
	}

	// The administrator publishes what the management page showed, which
	// never contained the Skill: it is carried over, not deleted.
	management, err := f.service.Management(ctx)
	if err != nil {
		t.Fatal(err)
	}
	published, err := f.service.Publish(ctx, management.Snapshot.Revision, "admin", "publish", management.Snapshot.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	if hasSkill(published.Configuration, packaged.ID) {
		t.Fatal("the publish result shows the invalid packaged Skill")
	}
	latest, err := f.repo.LatestAgentConfiguration(ctx)
	if err != nil || !hasSkill(latest.Configuration, packaged.ID) {
		t.Fatalf("a publish deleted the unseen packaged Skill (err %v)", err)
	}
}

// #665: a Skill an administrator wrote, including one that reuses a packaged
// ID with edited content, is never dropped: an invalid one fails the read and
// startup, naming it.
func TestInvalidAdministratorSkillFailsLoudly665(t *testing.T) {
	ctx := context.Background()
	retired, err := catalog.RetiredBuiltInSkills()
	if err != nil || len(retired) == 0 {
		t.Fatalf("retired built-in Skills: %v", err)
	}
	for name, id := range map[string]string{"own ID": "admin-written", "edited packaged ID": retired[0].ID} {
		t.Run(name, func(t *testing.T) {
			f := newFollowFixture(t)
			base, err := f.service.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			storeWithSkill(t, f, base.Revision, base.Configuration,
				catalog.SkillSchema{ID: id, Name: "Mine", Category: "category-retired-by-an-upgrade", Prompt: "x"})
			if _, err := f.service.Snapshot(ctx); err == nil || !strings.Contains(err.Error(), id) {
				t.Fatalf("snapshot error = %v, want one naming %s", err, id)
			}
			if err := f.service.ReconcileRetiredBuiltInSkills(ctx); err == nil || !strings.Contains(err.Error(), id) {
				t.Fatalf("reconcile error = %v, want one naming %s", err, id)
			}
		})
	}
}
