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
		{"the palette distance is a share", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Consistency.Appearance.Palette.MaxDistance = 1.5
		}},
		{"an enabled palette check needs its reading rules", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Consistency.Appearance.Palette.Colours = 0
		}},
		{"the appearance measure needs a subject threshold", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Consistency.Appearance.SubjectThreshold = 0
		}},
		{"the part check needs a minimum share", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Consistency.Appearance.Parts.MinShare = 0
		}},
		{"the part tolerance must not be negative", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Consistency.Appearance.Parts.MaxCountDelta = -1
		}},
		{"a mirror pair names two different views", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Consistency.Appearance.Mirror.Pairs[0].Views = []string{"left", "left"}
		}},
		{"a mirror pair names recipe views", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Consistency.Appearance.Mirror.Pairs[0].Views = []string{"left", "top"}
		}},
		{"a mirror pair declares a bound", func(a *catalog.ApplicationSchema) {
			pair := &a.Chat.ThreeView.Consistency.Appearance.Mirror.Pairs[0]
			pair.MinOverlap, pair.MaxOrientationMargin = 0, 0
		}},
		{"the mirror silhouette needs a side", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Consistency.Appearance.Mirror.NormalizedSide = 0
		}},
		{"the front piece share is a share", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Consistency.Front.MinMainComponentRatio = 2
		}},
		{"the front piece count must not be negative", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Consistency.Front.MaxComponents = -1
		}},
		{"every reported check needs its words", func(a *catalog.ApplicationSchema) {
			delete(a.Chat.ThreeView.Consistency.Reasons, "palette_distance")
		}},
		{"a reason names a known check", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Consistency.Reasons["no_such_check"] = "x {actual}"
		}},
		{"a reason carries the measurement", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Consistency.Reasons["part_count_delta"] = "parts differ"
		}},
		{"reasons need a separator", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Consistency.ReasonSeparator = ""
		}},
		{"an enabled frame needs a subject threshold", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Frame.SubjectThreshold = 0
		}},
		{"a disabled frame declares nothing else", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Frame.Enabled = false
		}},
		{"the frame margin is bounded", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Frame.MarginRatio = 0.9
		}},
		{"a frame minimum may not exceed its maximum", func(a *catalog.ApplicationSchema) {
			for i := range a.Chat.ThreeView.SubjectTypes {
				if a.Chat.ThreeView.SubjectTypes[i].ID == "humanoid" {
					a.Chat.ThreeView.SubjectTypes[i].FrameMinimumRatio = 2
				}
			}
		}},
		{"the checklist reaches the Agent through the completion receipt", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Messages.AgentCompleted =
				strings.ReplaceAll(a.Chat.ThreeView.Messages.AgentCompleted, "{checklist}", "")
		}},
		{"a checklist needs items", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Review.Items = nil
		}},
		{"a checklist template carries the views and the items", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Review.ChecklistTemplate = "{views}"
		}},
		{"checklist items are unique", func(a *catalog.ApplicationSchema) {
			items := a.Chat.ThreeView.Review.Items
			a.Chat.ThreeView.Review.Items = append(items, items[0])
		}},
		{"a view note names a recipe view", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Review.ViewNotes["top"] = "from above"
		}},
		{"a checklist placeholder needs a declared checklist", func(a *catalog.ApplicationSchema) {
			a.Chat.ThreeView.Review = catalog.ChatThreeViewReviewSchema{}
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

// #283: the default Schema compares the look of every later view with the
// front view, lets the front view carry floating accessories, follows the
// source's shape, and gives the Agent a checklist.
func TestDefaultThreeViewComparesAppearanceAndFollowsTheSource(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	three := c.Application().Chat.ThreeView
	consistency := three.Consistency
	appearance := consistency.Appearance
	if appearance.Palette.MaxDistance <= 0 || appearance.Parts.MaxCountDelta <= 0 || len(appearance.Mirror.Pairs) == 0 {
		t.Fatalf("appearance bounds are not declared: %+v", appearance)
	}
	pairs := map[string]bool{}
	for _, pair := range appearance.Mirror.Pairs {
		pairs[strings.Join(pair.Views, "/")] = true
	}
	if !pairs["left/right"] || !pairs["front/back"] {
		t.Fatalf("mirror pairs = %+v", appearance.Mirror.Pairs)
	}
	// A character with a floating weapon or a pet is one subject in pieces.
	if consistency.Front.MaxComponents <= 1 || consistency.Front.MinMainComponentRatio <= 0 {
		t.Fatalf("front acceptance = %+v: the front view must tolerate detached accessories", consistency.Front)
	}
	for _, name := range []string{"nonblank", "background", "subject_size", "edge", "components", "main_component",
		"subject_size_delta", "silhouette_height_delta", "palette_distance", "part_count_delta", "mirror_overlap",
		"mirror_orientation"} {
		if !strings.Contains(consistency.Reasons[name], "{actual}") {
			t.Errorf("check %s has no words in three_view.consistency.reasons", name)
		}
	}
	if !three.Frame.Enabled {
		t.Fatalf("frame = %+v: the views must follow the source's shape", three.Frame)
	}
	bounds := map[string][2]float64{}
	for _, subject := range three.SubjectTypes {
		bounds[subject.ID] = [2]float64{subject.FrameMinimumRatio, subject.FrameMaximumRatio}
	}
	if bounds["humanoid"][0] <= 0 || bounds["quadruped"][0] < 1 {
		t.Fatalf("frame bounds by subject = %+v: a quadruped's side view is wider than its front view", bounds)
	}
	ids := map[string]bool{}
	for _, item := range three.Review.Items {
		ids[item.ID] = true
	}
	for _, id := range []string{"clothing", "colours", "accessories", "sides"} {
		if !ids[id] {
			t.Errorf("the review checklist lacks %q", id)
		}
	}
	if !strings.Contains(three.Messages.AgentCompleted, "{checklist}") {
		t.Fatal("the completion receipt must carry the checklist")
	}
}
