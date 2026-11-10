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
