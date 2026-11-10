package catalog_test

import (
	"encoding/json"
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
		{"a later view must use the model-source view", func(a *catalog.ApplicationSchema) {
			for i := range a.Chat.Skills.Recipes {
				if a.Chat.Skills.Recipes[i].ID == a.Chat.ThreeView.RecipeID {
					a.Chat.Skills.Recipes[i].Steps[1].SourceStepKey = ""
				}
			}
		}},
		{"the first view must not depend on another", func(a *catalog.ApplicationSchema) {
			for i := range a.Chat.Skills.Recipes {
				if a.Chat.Skills.Recipes[i].ID == a.Chat.ThreeView.RecipeID {
					a.Chat.Skills.Recipes[i].Steps[0].SourceStepKey = a.Chat.Skills.Recipes[i].Steps[1].Key
				}
			}
		}},
		{"subject types need the default", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.DefaultSubjectType = "no-such-subject"
		}},
		{"the subject tool enum must match the declared types", func(a *catalog.ApplicationSchema) {
			for i := range a.Chat.AgentTools {
				if a.Chat.AgentTools[i].Name != "create_three_view" {
					continue
				}
				var parameters struct {
					Properties map[string]struct {
						Enum []string `json:"enum"`
					} `json:"properties"`
				}
				if err := json.Unmarshal(a.Chat.AgentTools[i].Parameters, &parameters); err != nil {
					t.Fatal(err)
				}
				property := parameters.Properties["subject_type"]
				property.Enum = property.Enum[:len(property.Enum)-1]
				encoded, err := json.Marshal(map[string]any{"properties": map[string]any{"subject_type": property}})
				if err != nil {
					t.Fatal(err)
				}
				a.Chat.AgentTools[i].Parameters = encoded
			}
		}},
		{"the front prompt needs its subject constraints", func(a *catalog.ApplicationSchema) {
			for i := range a.Chat.Skills.Recipes {
				if a.Chat.Skills.Recipes[i].ID == a.Chat.ThreeView.RecipeID {
					a.Chat.Skills.Recipes[i].Steps[0].PromptTemplate =
						strings.ReplaceAll(a.Chat.Skills.Recipes[i].Steps[0].PromptTemplate, "{subject_front_constraints}", "")
				}
			}
		}},
		{"a later view prompt needs the view constraints", func(a *catalog.ApplicationSchema) {
			for i := range a.Chat.Skills.Recipes {
				if a.Chat.Skills.Recipes[i].ID == a.Chat.ThreeView.RecipeID {
					a.Chat.Skills.Recipes[i].Steps[2].PromptTemplate =
						strings.ReplaceAll(a.Chat.Skills.Recipes[i].Steps[2].PromptTemplate, "{subject_view_constraints}", "")
				}
			}
		}},
		{"consistency thresholds must not be negative", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Consistency.MaxSubjectRatioDelta = -0.1
		}},
		{"view_order must start with the required view", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Multiview.ViewOrder = []string{"left", "front", "back", "right"}
		}},
		{"view_order must name recipe steps", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Multiview.ViewOrder = []string{"front", "top"}
		}},
		{"the completion receipt needs its placeholders", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Messages.AgentCompleted = "done"
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
