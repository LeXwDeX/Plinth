package chat

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func projectPromptOrchestrator(t *testing.T) *Orchestrator {
	t.Helper()
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := loaded.Application()
	return &Orchestrator{schema: application.Chat, project: application.Project}
}

func TestProjectPromptNamesTheFrozenGuideConventionsAndHandles(t *testing.T) {
	orchestrator := projectPromptOrchestrator(t)
	options := TurnOptions{
		ProjectID: "prj_1",
		StyleLock: StyleLock{Source: StyleLockSourceProject},
		ProjectContext: &ProjectContext{
			ID: "prj_1", Name: "Star Harbor",
			StyleGuide: ProjectStyleGuideContext{Enabled: true, Text: "warm ink"},
			Conventions: ProjectConventionsContext{
				Brief: ProductionBrief{"purpose": mustJSON("character")},
				Texts: map[string]string{"naming": "hero_"},
			},
			Assets: []ProjectAssetContext{
				{Handle: "@project1", Kind: "character", Name: "Role A", Description: "red coat"},
				{Handle: "@project2", Kind: "prop", Name: "Sword"},
			},
		},
	}
	prompt := orchestrator.projectPrompt(options)
	for _, want := range []string{"Star Harbor", "warm ink", "hero_", "@project1", "Role A", "@project2", "list_project_assets"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("project prompt lacks %q: %s", want, prompt)
		}
	}
	if !strings.Contains(prompt, "默认继承") {
		t.Errorf("project source must say the guide is inherited: %s", prompt)
	}
	for _, source := range []string{StyleLockSourceSession, StyleLockSourceOff} {
		options.StyleLock.Source = source
		changed := orchestrator.projectPrompt(options)
		if source == StyleLockSourceSession && !strings.Contains(changed, "覆盖") {
			t.Errorf("session source copy missing: %s", changed)
		}
		if source == StyleLockSourceOff && !strings.Contains(changed, "不注入") {
			t.Errorf("off source copy missing: %s", changed)
		}
	}
	if prompt := orchestrator.projectPrompt(TurnOptions{}); prompt != "" {
		t.Fatalf("a turn outside a project rendered a project block: %s", prompt)
	}
}

func TestProjectPromptFillsEmptyValuesFromTheSchemaCopy(t *testing.T) {
	orchestrator := projectPromptOrchestrator(t)
	messages := &orchestrator.project.Messages
	messages.StyleGuideEmpty = "STYLE-EMPTY"
	messages.StyleGuideReferencesOnly = "STYLE-PICTURES"
	messages.ConventionsEmpty = "CONVENTIONS-NONE"
	messages.AssetsEmpty = "ASSETS-NONE"
	options := TurnOptions{
		StyleLock: StyleLock{Source: StyleLockSourceProject},
		ProjectContext: &ProjectContext{
			ID: "prj_1", Name: "P",
			StyleGuide: ProjectStyleGuideContext{Enabled: true, References: []string{"/api/v1/tasks/t1/assets/a1"}},
		},
	}
	prompt := orchestrator.projectPrompt(options)
	for _, want := range []string{"STYLE-PICTURES", "CONVENTIONS-NONE", "ASSETS-NONE"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("project prompt lacks Schema copy %q: %s", want, prompt)
		}
	}
	options.ProjectContext.StyleGuide = ProjectStyleGuideContext{}
	prompt = orchestrator.projectPrompt(options)
	if !strings.Contains(prompt, "STYLE-EMPTY") {
		t.Errorf("disabled guide must use the Schema's empty copy: %s", prompt)
	}
	if strings.Contains(prompt, "STYLE-PICTURES") {
		t.Errorf("an empty guide used the references-only copy: %s", prompt)
	}
}

func TestProjectPromptListsOnlyTheSchemaLimitAndSaysWhenTruncated(t *testing.T) {
	orchestrator := projectPromptOrchestrator(t)
	orchestrator.project.Agent.ListLimit = 1
	options := TurnOptions{
		StyleLock: StyleLock{Source: StyleLockSourceProject},
		ProjectContext: &ProjectContext{
			ID: "prj_1", Name: "P",
			Assets: []ProjectAssetContext{
				{Handle: "@project1", Kind: "character", Name: "Role A"},
				{Handle: "@project2", Kind: "prop", Name: "Sword"},
			},
		},
	}
	prompt := orchestrator.projectPrompt(options)
	if !strings.Contains(prompt, "@project1") || strings.Contains(prompt, "@project2") {
		t.Fatalf("limit not applied: %s", prompt)
	}
	if !strings.Contains(prompt, "最多列出") {
		t.Fatalf("limit note missing: %s", prompt)
	}
}

// TestProjectPromptShowsTheGuidePaletteAndFreezesItWithTheLock is #288: the
// enabled guide's palette reaches the Agent's project block and the frozen
// project-source lock; a disabled guide or a session source carries none.
func TestProjectPromptShowsTheGuidePaletteAndFreezesItWithTheLock(t *testing.T) {
	orchestrator := projectPromptOrchestrator(t)
	if !strings.Contains(orchestrator.project.Messages.StyleGuidePalette, "{palette}") {
		t.Fatal("default catalog must declare project.messages.style_guide_palette with {palette}")
	}
	context := &ProjectContext{
		ID: "prj_1", Name: "Star Harbor",
		StyleGuide: ProjectStyleGuideContext{Enabled: true, Text: "warm ink", Palette: []string{"#112233", "#AABBCC"}},
	}
	options := TurnOptions{StyleLock: StyleLock{Source: StyleLockSourceProject}, ProjectContext: context}
	prompt := orchestrator.projectPrompt(options)
	if !strings.Contains(prompt, "warm ink") || !strings.Contains(prompt, "#112233, #AABBCC") {
		t.Fatalf("project block lacks the palette: %s", prompt)
	}
	// A palette-only guide replaces the empty copy instead of joining it.
	context.StyleGuide.Text = ""
	prompt = orchestrator.projectPrompt(options)
	if !strings.Contains(prompt, "#112233") || strings.Contains(prompt, orchestrator.project.Messages.StyleGuideEmpty+"\n") {
		t.Fatalf("palette-only project block = %s", prompt)
	}
	if !context.ProjectStyleSource() {
		t.Fatal("a palette-only guide is a project style source")
	}

	lock := effectiveStyleLock(StyleLock{Source: StyleLockSourceProject}, context)
	if len(lock.ProjectPalette) != 2 || lock.ProjectPalette[0] != "#112233" {
		t.Fatalf("frozen lock palette = %v", lock.ProjectPalette)
	}
	cloned := cloneProjectContext(context)
	cloned.StyleGuide.Palette[0] = "#000000"
	if context.StyleGuide.Palette[0] != "#112233" {
		t.Fatal("cloning the project context shares the palette")
	}
	if lock := effectiveStyleLock(StyleLock{Source: StyleLockSourceSession, Enabled: true, Text: "s"}, context); len(lock.ProjectPalette) != 0 {
		t.Fatalf("session source carries the project palette: %v", lock.ProjectPalette)
	}
	context.StyleGuide.Enabled = false
	if lock := effectiveStyleLock(StyleLock{Source: StyleLockSourceProject}, context); len(lock.ProjectPalette) != 0 || lock.Enabled {
		t.Fatalf("a disabled guide froze %+v", lock)
	}
	if prompt := orchestrator.projectPrompt(options); strings.Contains(prompt, "#112233") {
		t.Fatalf("a disabled guide showed its palette: %s", prompt)
	}
}
