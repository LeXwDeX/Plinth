package agentconfiguration_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func personalSkillService(t *testing.T) *agentconfiguration.Service {
	t.Helper()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "personal-548.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := catalog.LoadDefaultSkillCatalog()
	if err != nil {
		t.Fatal(err)
	}
	service, err := agentconfiguration.New(repo, product.Application(), defaults, product.List(""), agentconfiguration.WithPersonalSkills(repo))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func ptr(value string) *string { return &value }

// #548: creation keeps the standard fields; a Skill saved without a slug keeps
// the one derived from its ID; a slug is unique among the owner's Skills and
// off the system Skills' slugs, and a validation error names its field.
func TestPersonalSkillCreationKeepsStandardFields(t *testing.T) {
	ctx := context.Background()
	service := personalSkillService(t)
	created, err := service.CreatePersonalSkill(ctx, "owner", agentconfiguration.CreatePersonalSkillInput{
		Name: "清单做法", Prompt: "先列清单再动手。", Slug: "checklist-first",
		Description: "做多步任务前先列清单时使用。", Apply: catalog.SkillApplyInstruction, RecommendedSize: "2048x2048",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Slug != "checklist-first" || created.Description != "做多步任务前先列清单时使用。" || created.Kind() != catalog.SkillKindInstruction {
		t.Fatalf("created = %+v", created)
	}
	derived, err := service.CreatePersonalSkill(ctx, "owner", agentconfiguration.CreatePersonalSkillInput{Name: "水彩", Prompt: "柔和水彩。", RecommendedSize: "2048x2048"})
	if err != nil {
		t.Fatal(err)
	}
	if derived.Slug != catalog.SkillSlug(catalog.SkillSchema{ID: derived.ID}) {
		t.Fatalf("derived slug = %q", derived.Slug)
	}
	for name, test := range map[string]struct {
		input agentconfiguration.CreatePersonalSkillInput
		field string
	}{
		"own slug":           {agentconfiguration.CreatePersonalSkillInput{Name: "n", Prompt: "p", Slug: "checklist-first"}, agentconfiguration.PersonalSkillFieldSlug},
		"system slug":        {agentconfiguration.CreatePersonalSkillInput{Name: "n", Prompt: "p", Slug: "three-view"}, agentconfiguration.PersonalSkillFieldSlug},
		"invalid slug":       {agentconfiguration.CreatePersonalSkillInput{Name: "n", Prompt: "p", Slug: "Bad Slug"}, agentconfiguration.PersonalSkillFieldSlug},
		"two-line summary":   {agentconfiguration.CreatePersonalSkillInput{Name: "n", Prompt: "p", Description: "a\nb"}, agentconfiguration.PersonalSkillFieldDescription},
		"angle brackets":     {agentconfiguration.CreatePersonalSkillInput{Name: "n", Prompt: "p", Description: "<x>"}, agentconfiguration.PersonalSkillFieldDescription},
		"unknown apply":      {agentconfiguration.CreatePersonalSkillInput{Name: "n", Prompt: "p", Apply: "script"}, agentconfiguration.PersonalSkillFieldApply},
		"empty body":         {agentconfiguration.CreatePersonalSkillInput{Name: "n", Prompt: " "}, agentconfiguration.PersonalSkillFieldPrompt},
		"empty display name": {agentconfiguration.CreatePersonalSkillInput{Name: "", Prompt: "p"}, agentconfiguration.PersonalSkillFieldName},
	} {
		test.input.RecommendedSize = "2048x2048"
		_, err := service.CreatePersonalSkill(ctx, "owner", test.input)
		if !errors.Is(err, agentconfiguration.ErrInvalidPersonalSkill) || agentconfiguration.InvalidPersonalSkillField(err) != test.field {
			t.Errorf("%s: err = %v, field %q", name, err, agentconfiguration.InvalidPersonalSkillField(err))
		}
	}
	// Another owner may use the same slug.
	if _, err := service.CreatePersonalSkill(ctx, "other", agentconfiguration.CreatePersonalSkillInput{Name: "n", Prompt: "p", Slug: "checklist-first", RecommendedSize: "2048x2048"}); err != nil {
		t.Fatalf("another owner's slug: %v", err)
	}
}

// #548: PATCH changes only the members it carries, through the same validator,
// and leaves the row untouched when that fails.
func TestPersonalSkillPatchChangesOnlyTheGivenField(t *testing.T) {
	ctx := context.Background()
	service := personalSkillService(t)
	first, err := service.CreatePersonalSkill(ctx, "owner", agentconfiguration.CreatePersonalSkillInput{
		Name: "清单做法", Prompt: "先列清单。", Slug: "checklist-first", Description: "多步任务时使用。",
		Apply: catalog.SkillApplyInstruction, RecommendedSize: "2048x2048",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreatePersonalSkill(ctx, "owner", agentconfiguration.CreatePersonalSkillInput{Name: "水彩", Prompt: "柔和水彩。", RecommendedSize: "2048x2048"})
	if err != nil {
		t.Fatal(err)
	}
	patched, err := service.PatchPersonalSkill(ctx, "owner", first.ID, agentconfiguration.PatchPersonalSkillInput{Description: ptr(" 做多步任务前先列清单时使用。 ")})
	if err != nil {
		t.Fatal(err)
	}
	want := first
	want.Description = "做多步任务前先列清单时使用。"
	if !equalSkill(&patched, want) {
		t.Fatalf("patched = %+v, want %+v", patched, want)
	}
	for _, step := range []struct {
		input agentconfiguration.PatchPersonalSkillInput
		check func(catalog.SkillSchema) bool
	}{
		{agentconfiguration.PatchPersonalSkillInput{Slug: ptr("list-before-acting")}, func(s catalog.SkillSchema) bool { return s.Slug == "list-before-acting" }},
		{agentconfiguration.PatchPersonalSkillInput{Prompt: ptr("先列清单，再逐项完成。")}, func(s catalog.SkillSchema) bool { return s.Prompt == "先列清单，再逐项完成。" }},
		{agentconfiguration.PatchPersonalSkillInput{Name: ptr("清单优先")}, func(s catalog.SkillSchema) bool { return s.Name == "清单优先" }},
	} {
		patched, err := service.PatchPersonalSkill(ctx, "owner", first.ID, step.input)
		if err != nil || !step.check(patched) || patched.Apply != catalog.SkillApplyInstruction || patched.Description != want.Description {
			t.Fatalf("patch %+v: %+v, %v", step.input, patched, err)
		}
	}
	current, err := service.PersonalSkills(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		input agentconfiguration.PatchPersonalSkillInput
		field string
	}{
		"slug of another Skill": {agentconfiguration.PatchPersonalSkillInput{Slug: ptr(second.Slug)}, agentconfiguration.PersonalSkillFieldSlug},
		"system slug":           {agentconfiguration.PatchPersonalSkillInput{Slug: ptr("four-view")}, agentconfiguration.PersonalSkillFieldSlug},
		"cleared description":   {agentconfiguration.PatchPersonalSkillInput{Description: ptr(" ")}, agentconfiguration.PersonalSkillFieldDescription},
		"two-line description":  {agentconfiguration.PatchPersonalSkillInput{Description: ptr("a\nb")}, agentconfiguration.PersonalSkillFieldDescription},
		"empty body":            {agentconfiguration.PatchPersonalSkillInput{Prompt: ptr("")}, agentconfiguration.PersonalSkillFieldPrompt},
		"empty display name":    {agentconfiguration.PatchPersonalSkillInput{Name: ptr(" ")}, agentconfiguration.PersonalSkillFieldName},
	} {
		if _, err := service.PatchPersonalSkill(ctx, "owner", first.ID, test.input); agentconfiguration.InvalidPersonalSkillField(err) != test.field {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	unchanged, err := service.PersonalSkills(ctx, "owner")
	if err != nil || len(unchanged) != len(current) || !equalSkill(&unchanged[0], current[0]) {
		t.Fatalf("a refused patch changed the row: %+v %v", unchanged, err)
	}
	// A slug cleared on purpose falls back to the one derived from the ID.
	cleared, err := service.PatchPersonalSkill(ctx, "owner", first.ID, agentconfiguration.PatchPersonalSkillInput{Slug: ptr("")})
	if err != nil || cleared.Slug != catalog.SkillSlug(catalog.SkillSchema{ID: first.ID}) {
		t.Fatalf("cleared slug = %+v, %v", cleared, err)
	}
	// Another owner's Skill and an unknown ID are not found alike.
	for _, test := range []struct{ owner, id string }{{"other", first.ID}, {"owner", "personal-unknown"}} {
		if _, err := service.PatchPersonalSkill(ctx, test.owner, test.id, agentconfiguration.PatchPersonalSkillInput{Name: ptr("x")}); !errors.Is(err, agentconfiguration.ErrPersonalSkillNotFound) {
			t.Fatalf("%+v: err = %v", test, err)
		}
	}
}

func equalSkill(got *catalog.SkillSchema, want catalog.SkillSchema) bool {
	return got.ID == want.ID && got.Name == want.Name && got.Prompt == want.Prompt && got.Slug == want.Slug &&
		got.Description == want.Description && got.Apply == want.Apply && got.RecommendedSize == want.RecommendedSize
}
