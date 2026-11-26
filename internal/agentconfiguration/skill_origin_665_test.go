package agentconfiguration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func storeConfiguration(t *testing.T, f followFixture, expected string, c agentconfiguration.Configuration) agentconfiguration.Version {
	t.Helper()
	v, err := f.repo.AppendAgentConfiguration(context.Background(), expected, agentconfiguration.Version{
		Snapshot:  agentconfiguration.Snapshot{Configuration: c},
		CreatedAt: time.Now().UTC(), ActorID: "admin", Source: "publish"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func withSkills(c agentconfiguration.Configuration, skills ...catalog.SkillSchema) agentconfiguration.Configuration {
	c.Skills = append(append([]catalog.SkillSchema(nil), c.Skills...), skills...)
	return c
}

// #665 wave 4: a stored copy of a packaged Skill carrying an older release's
// text (equal to no current default and no retired fingerprint) in a version
// stored before origins is still the product's copy: when the Schema tightens
// and it no longer passes, it is skipped instead of failing every new turn and
// startup. With an origin saying an administrator edited it, the same Skill
// fails loudly, naming it; with a packaged origin it is skipped.
func TestOlderPackagedSkillTextIsPackagedUnlessTheOriginSaysEdited665(t *testing.T) {
	ctx := context.Background()
	retired, err := catalog.RetiredBuiltInSkills()
	if err != nil || len(retired) == 0 {
		t.Fatalf("retired built-in Skills: %v", err)
	}
	id := retired[0].ID
	older := catalog.SkillSchema{ID: id, Name: "Packaged", Category: "category-retired-by-an-upgrade",
		Prompt: "an older release's prompt of this Skill"}
	for _, tc := range []struct {
		name    string
		origins map[string]string
		failing bool
	}{
		{"version stored before origins", nil, false},
		{"origin packaged", map[string]string{id: agentconfiguration.SkillOriginPackaged}, false},
		{"origin edited", map[string]string{id: agentconfiguration.SkillOriginEdited}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFollowFixture(t)
			base, err := f.service.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			stored := withSkills(base.Configuration, older)
			stored.SkillOrigins = tc.origins
			storeConfiguration(t, f, base.Revision, stored)
			snapshot, err := f.service.Snapshot(ctx)
			reconcileErr := f.service.ReconcileRetiredBuiltInSkills(ctx)
			if tc.failing {
				if err == nil || !strings.Contains(err.Error(), id) || reconcileErr == nil || !strings.Contains(reconcileErr.Error(), id) {
					t.Fatalf("snapshot error = %v, reconcile error = %v, want both naming %s", err, reconcileErr, id)
				}
				return
			}
			if err != nil || reconcileErr != nil {
				t.Fatalf("an older packaged text failed: snapshot %v, reconcile %v", err, reconcileErr)
			}
			if hasSkill(snapshot.Configuration, id) {
				t.Fatal("the invalid packaged Skill was not skipped")
			}
		})
	}
}

// #665 wave 4: a publish records the origin of every packaged-ID Skill: the
// packaged content is packaged, an administrator's edit is edited (and stays
// edited when published again unchanged), and a packaged text carried over
// unchanged from a version stored before origins stays packaged. The origin is
// never shown and a submission cannot set it.
func TestPublishRecordsSkillOrigins665(t *testing.T) {
	ctx := context.Background()
	f := newFollowFixture(t)
	base, err := f.service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	packaged := catalog.SkillSchema{ID: "packaged-origin-665", Name: "Packaged", Prompt: "the packaged prompt", RecommendedSize: "2048x2048"}
	f.service.MarkPackagedForTest(packaged)
	id := packaged.ID
	origin := func() string {
		t.Helper()
		latest, err := f.repo.LatestAgentConfiguration(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return latest.Configuration.SkillOrigins[id]
	}
	publish := func(c agentconfiguration.Configuration) {
		t.Helper()
		management, err := f.service.Management(ctx)
		if err != nil {
			t.Fatal(err)
		}
		published, err := f.service.Publish(ctx, management.Snapshot.Revision, "admin", "publish", c)
		if err != nil {
			t.Fatal(err)
		}
		if published.Configuration.SkillOrigins != nil {
			t.Fatalf("the publish result shows origins: %v", published.Configuration.SkillOrigins)
		}
	}

	forged := withSkills(base.Configuration, packaged)
	forged.SkillOrigins = map[string]string{id: agentconfiguration.SkillOriginEdited}
	publish(forged)
	if got := origin(); got != agentconfiguration.SkillOriginPackaged {
		t.Fatalf("packaged content origin = %q (did a submission set it?)", got)
	}
	edited := packaged
	edited.Prompt = "an administrator's prompt"
	publish(withSkills(base.Configuration, edited))
	if got := origin(); got != agentconfiguration.SkillOriginEdited {
		t.Fatalf("edited origin = %q", got)
	}
	snapshot, err := f.service.Snapshot(ctx)
	if err != nil || snapshot.Configuration.SkillOrigins != nil {
		t.Fatalf("snapshot shows origins %v (err %v)", snapshot.Configuration.SkillOrigins, err)
	}
	publish(snapshot.Configuration)
	if got := origin(); got != agentconfiguration.SkillOriginEdited {
		t.Fatalf("an edit published again unchanged became %q", got)
	}

	// A version stored before origins with an older packaged text.
	latest, err := f.repo.LatestAgentConfiguration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	older := packaged
	older.Prompt = "an older release's prompt"
	storeConfiguration(t, f, latest.Revision, withSkills(base.Configuration, older))
	management, err := f.service.Management(ctx)
	if err != nil {
		t.Fatal(err)
	}
	publish(management.Snapshot.Configuration)
	if got := origin(); got != agentconfiguration.SkillOriginPackaged {
		t.Fatalf("an older packaged text carried over unchanged became %q", got)
	}
}

// #665 wave 4: when a part of the configuration other than the Skills is
// invalid, that reason is reported; no valid Skill is named as failing.
func TestNonSkillFailureIsReportedBeforeSkills665(t *testing.T) {
	ctx := context.Background()
	f := newFollowFixture(t)
	base, err := f.service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := withSkills(base.Configuration, catalog.SkillSchema{ID: "admin-written", Name: "Mine", Prompt: "x"})
	c.EnabledToolNames = append(append([]string(nil), c.EnabledToolNames...), "no-such-tool")
	err = f.service.TolerantForTest(c)
	if err == nil || !strings.Contains(err.Error(), "no-such-tool") || strings.Contains(err.Error(), "admin-written") ||
		strings.Contains(err.Error(), "fail the current Schema") {
		t.Fatalf("error = %v, want the tool selection's reason only", err)
	}
}
