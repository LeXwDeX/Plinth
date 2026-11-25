package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #548: the standard name follows the Agent Skills rule.
func TestSkillSlugFollowsTheAgentSkillsNameRule(t *testing.T) {
	for slug, want := range map[string]bool{
		"three-view": true, "a": true, "x9-y8": true, strings.Repeat("a", 64): true,
		"": false, strings.Repeat("a", 65): false, "-start": false, "end-": false, "double--hyphen": false,
		"Upper": false, "under_score": false, "中文": false, "my-claude-skill": false, "anthropic": false,
	} {
		if got := catalog.ValidSkillSlug(slug); got != want {
			t.Errorf("ValidSkillSlug(%q) = %v, want %v", slug, got, want)
		}
	}
}

func TestSkillDescriptionIsOneBoundedLine(t *testing.T) {
	if err := catalog.ValidateSkillDescription("画风参考。用户要水彩时使用。"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "  ", "两行\n描述", "含 <tag>", strings.Repeat("字", 1025)} {
		if catalog.ValidateSkillDescription(bad) == nil {
			t.Errorf("description %q was accepted", bad)
		}
	}
}

// A Skill stored before #548 is read without a migration: a slug derived
// from its ID, a description derived from the Schema template and the first
// sentence of its prompt, and the style kind.
func TestLegacySkillReadsDerivedSlugDescriptionAndStyleKind(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	directory := product.Application().Chat.Skills.Directory
	legacy := catalog.SkillSchema{ID: "personal-0123456789abcdef0123456789abcdef", Name: "水彩",
		Prompt: "柔和的水彩笔触<淡彩>。纸张纹理明显。\n第二行"}
	slug := catalog.SkillSlug(legacy)
	if !strings.HasPrefix(slug, "skill-") || len(slug) != len("skill-")+8 || !catalog.ValidSkillSlug(slug) {
		t.Fatalf("derived slug = %q", slug)
	}
	if catalog.SkillSlug(legacy) != slug {
		t.Fatal("the derived slug is not stable")
	}
	description := catalog.SkillDescriptionFor(legacy, directory)
	if err := catalog.ValidateSkillDescription(description); err != nil {
		t.Fatalf("derived description %q: %v", description, err)
	}
	if !strings.Contains(description, "水彩") || !strings.Contains(description, "柔和的水彩笔触淡彩。") || strings.Contains(description, "纸张") {
		t.Fatalf("derived description = %q", description)
	}
	if legacy.Kind() != catalog.SkillKindStyle {
		t.Fatalf("legacy kind = %q", legacy.Kind())
	}
	if (catalog.SkillSchema{Apply: catalog.SkillApplyInstruction}).Kind() != catalog.SkillKindInstruction ||
		(catalog.SkillSchema{Tool: "create_three_view"}).Kind() != catalog.SkillKindTool {
		t.Fatal("kind does not follow apply and tool")
	}
	line := catalog.SkillDirectoryEntry(legacy, directory)
	if !strings.HasPrefix(line, "- "+slug+"（水彩）：") || strings.Count(line, "\n") != 1 {
		t.Fatalf("directory line = %q", line)
	}
}

// The directory refuses a system Skill without a valid standard name or a
// description, and system lines that overflow their part.
func TestSkillDirectoryValidatesSystemSkills(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	baseline := product.Application()
	baseline.Localization = nil
	if err := catalog.ValidateApplicationSchema(baseline); err != nil {
		t.Fatalf("test setup: the shipped Schema is refused: %v", err)
	}
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		"bad slug": func(app *catalog.ApplicationSchema) { app.Chat.Skills.SystemSkills[0].Slug = "Three_View" },
		"duplicate slug": func(app *catalog.ApplicationSchema) {
			app.Chat.Skills.SystemSkills[1].Slug = app.Chat.Skills.SystemSkills[0].Slug
		},
		"no description": func(app *catalog.ApplicationSchema) { app.Chat.Skills.SystemSkills[0].Description = "" },
		"system part":    func(app *catalog.ApplicationSchema) { app.Chat.Skills.Directory.MaxSystemCharacters = 10 },
		"unknown tool":   func(app *catalog.ApplicationSchema) { app.Chat.Skills.Directory.Tool = "read_skill" },
		"no slug slot": func(app *catalog.ApplicationSchema) {
			app.Chat.Skills.Directory.EntryTemplate = "- {name}：{description}"
		},
	} {
		app := product.Application()
		app.Localization = nil
		mutate(&app)
		if err := catalog.ValidateApplicationSchema(app); err == nil {
			t.Errorf("%s: the Schema was accepted", name)
		}
	}
}
