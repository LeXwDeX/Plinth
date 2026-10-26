package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestThreeViewDeclarationsAreValidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"recipe must exist", func(a *catalog.ApplicationSchema) { a.Chat.ThreeView.RecipeID = "no-such-recipe" }},
		{"model source must be a step of the recipe", func(a *catalog.ApplicationSchema) { a.Chat.ThreeView.ModelSourceStepKey = "top" }},
		{"list limit must be positive", func(a *catalog.ApplicationSchema) { a.Chat.ThreeView.MaximumListed = 0 }},
		{"label is required", func(a *catalog.ApplicationSchema) { a.Chat.ThreeView.Label = " " }},
		{"model source note is required", func(a *catalog.ApplicationSchema) { a.Chat.ThreeView.ModelSourceNote = "" }},
		{"front-unavailable text is required", func(a *catalog.ApplicationSchema) { a.Chat.ThreeView.Messages.ModelSourceNotYet = "" }},
		{"reference-selection text is required", func(a *catalog.ApplicationSchema) { a.Chat.ThreeView.Messages.ReferenceRequired = " " }},
		{"agent text names the task", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Messages.AgentStarted = strings.ReplaceAll(a.Chat.ThreeView.Messages.AgentStarted, "{task}", "")
		}},
		{"a view may not depend on another", func(a *catalog.ApplicationSchema) {
			for i := range a.Chat.Skills.Recipes {
				if a.Chat.Skills.Recipes[i].ID == a.Chat.ThreeView.RecipeID {
					a.Chat.Skills.Recipes[i].Steps[1].SourceStepKey = a.Chat.Skills.Recipes[i].Steps[0].Key
				}
			}
		}},
		{"view route needs the run and view parameters", func(a *catalog.ApplicationSchema) {
			a.Runtime.HTTP.Routes.ChatSessionThreeViewItem = "/api/v1/chat/sessions/{sessionID}/three-views/view"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			tt.mutate(&app)
			if err := catalog.ValidateApplicationSchema(app); err == nil {
				t.Fatal("ValidateApplicationSchema accepted an invalid declaration")
			}
		})
	}
}

func TestDefaultThreeViewUsesTheFrontViewForModels(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	three := c.Application().Chat.ThreeView
	if three.RecipeID != "image-three-view" || three.ModelSourceStepKey != "front" || three.ModelSourceNote == "" {
		t.Fatalf("three_view = %+v", three)
	}
}

func TestThreeViewIsASchemaSystemSkill(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	skills := c.Application().Chat.Skills
	if len(skills.SystemSkills) != 1 {
		t.Fatalf("system skills = %+v", skills.SystemSkills)
	}
	skill := skills.SystemSkills[0]
	if skill.Tool != "create_three_view" || !skill.RequiresImage || skill.DefaultMessage == "" || !skill.OneShot {
		t.Fatalf("three-view skill = %+v", skill)
	}
	for _, tool := range c.Application().Chat.AgentTools {
		if tool.Name == skill.Tool && tool.Execution.AgentScope != "2d" {
			t.Fatalf("bound tool scope = %q, want 2d", tool.Execution.AgentScope)
		}
	}
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		"unknown tool":      func(a *catalog.ApplicationSchema) { a.Chat.Skills.SystemSkills[0].Tool = "no_such_tool" },
		"no default text":   func(a *catalog.ApplicationSchema) { a.Chat.Skills.SystemSkills[0].DefaultMessage = "" },
		"style fields":      func(a *catalog.ApplicationSchema) { a.Chat.Skills.SystemSkills[0].ThumbnailPath = "/x.webp" },
		"directive no tool": func(a *catalog.ApplicationSchema) { a.Chat.Skills.ToolDirectiveTemplate = "{name}{prompt}" },
	} {
		app := c.Application()
		app.Chat.Skills.SystemSkills = append([]catalog.SkillSchema(nil), skills.SystemSkills...)
		mutate(&app)
		if err := catalog.ValidateApplicationSchema(app); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}
