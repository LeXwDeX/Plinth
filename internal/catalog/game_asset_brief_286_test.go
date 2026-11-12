package catalog_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func shippedApplication(t *testing.T) catalog.ApplicationSchema {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return product.Application()
}

func briefField(t *testing.T, fields []catalog.ChatProductionBriefField, name string) catalog.ChatProductionBriefField {
	t.Helper()
	for _, field := range fields {
		if field.Name == name {
			return field
		}
	}
	t.Fatalf("brief field %q is not declared", name)
	return catalog.ChatProductionBriefField{}
}

func briefToolProperties(t *testing.T, app catalog.ApplicationSchema) map[string]struct {
	Type string   `json:"type"`
	Enum []string `json:"enum"`
} {
	t.Helper()
	for _, tool := range app.Chat.AgentTools {
		if tool.Execution.ImplementationKey != "session.brief" {
			continue
		}
		var parameters struct {
			Properties map[string]struct {
				Type string   `json:"type"`
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
			t.Fatal(err)
		}
		return parameters.Properties
	}
	t.Fatal("the production brief tool is not declared")
	return nil
}

// TestShippedBriefDeclaresEngineAndPlatformSelects is #286: the brief asks for
// the target engine and the target platform as selects with stable ids (the
// ids and the two field names are what the platform budgets and the engine
// skeleton specs key on), and keeps the rigging, animation and budget fields.
func TestShippedBriefDeclaresEngineAndPlatformSelects(t *testing.T) {
	app := shippedApplication(t)
	fields := app.Chat.Model3DUsage.Brief.Fields
	for name, want := range map[string][]string{
		"target_engine":   {"unity", "unreal", "godot", "web", "other"},
		"target_platform": {"mobile", "pc_console", "web", "xr"},
	} {
		field := briefField(t, fields, name)
		if field.Type != "select" {
			t.Fatalf("%s is a %q, want a select", name, field.Type)
		}
		if got := field.OptionIDs(); !slices.Equal(got, want) {
			t.Fatalf("%s options = %v, want %v", name, got, want)
		}
		for _, option := range field.Options {
			if strings.TrimSpace(option.Label) == "" {
				t.Fatalf("%s option %q has no label", name, option.ID)
			}
		}
	}
	for name, kind := range map[string]string{"needs_rig": "boolean", "needs_animation": "boolean", "triangle_budget": "integer"} {
		if field := briefField(t, fields, name); field.Type != kind {
			t.Fatalf("%s is a %q, want %q", name, field.Type, kind)
		}
	}

	// A project's conventions seed new sessions with the same two fields and
	// the same choices.
	for _, name := range []string{"target_engine", "target_platform"} {
		convention := briefField(t, app.Project.Conventions.Fields, name)
		if !slices.Equal(convention.Options, briefField(t, fields, name).Options) {
			t.Fatalf("project convention %s options differ from the brief's", name)
		}
	}

	// The Agent writes both through the brief tool, offered exactly the ids.
	properties := briefToolProperties(t, app)
	for _, name := range []string{"target_engine", "target_platform"} {
		property, declared := properties[name]
		if !declared || property.Type != "string" || !slices.Equal(property.Enum, briefField(t, fields, name).OptionIDs()) {
			t.Fatalf("brief tool property %s = %+v", name, property)
		}
	}
	if _, declared := properties["needs_rig"]; !declared {
		t.Fatal("the brief tool dropped needs_rig")
	}
}

// TestEnglishProjectionTranslatesTheBriefSelects is #286: the browser reads the
// select labels from the public projection, and the English bundle translates
// every one of them, so an English reader sees no Chinese in the brief fields.
func TestEnglishProjectionTranslatesTheBriefSelects(t *testing.T) {
	projections, err := catalog.NewLocalizedPublicSchemas(shippedApplication(t))
	if err != nil {
		t.Fatal(err)
	}
	type publicField struct {
		Name    string `json:"name"`
		Label   string `json:"label"`
		Options []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"options"`
	}
	fields := func(locale string) map[string]publicField {
		var document struct {
			Chat struct {
				Model3DUsage struct {
					Brief struct {
						Fields []publicField `json:"fields"`
					} `json:"brief"`
				} `json:"model3d_usage"`
			} `json:"chat"`
		}
		if err := json.Unmarshal(projections.For(locale).JSON, &document); err != nil {
			t.Fatal(err)
		}
		result := map[string]publicField{}
		for _, field := range document.Chat.Model3DUsage.Brief.Fields {
			result[field.Name] = field
		}
		return result
	}
	english := fields("en")
	want := map[string]map[string]string{
		"target_engine":   {"unity": "Unity", "unreal": "Unreal Engine", "godot": "Godot", "web": "Web / three.js", "other": "Other engine"},
		"target_platform": {"mobile": "Mobile", "pc_console": "PC / console", "web": "Web", "xr": "XR (VR / AR / MR)"},
	}
	for name, labels := range want {
		field := english[name]
		if len(field.Options) != len(labels) {
			t.Fatalf("%s options = %+v", name, field.Options)
		}
		for _, option := range field.Options {
			if option.Label != labels[option.ID] {
				t.Errorf("en %s option %s = %q, want %q", name, option.ID, option.Label, labels[option.ID])
			}
		}
	}
	if english["target_engine"].Label != "Target engine" || english["target_platform"].Label != "Target platform" {
		t.Fatalf("en labels = %q, %q", english["target_engine"].Label, english["target_platform"].Label)
	}
	chinese := fields("zh-CN")
	if chinese["target_platform"].Options[0].ID != "mobile" || chinese["target_platform"].Options[0].Label != "手机" {
		t.Fatalf("zh-CN platform options = %+v", chinese["target_platform"].Options)
	}
}

// TestShippedBriefGuidanceAsksForEngineAndPlatformAndRigging is #286: every
// place that tells the Agent what to ask names the target engine, the target
// platform and the rigging need for a game asset.
func TestShippedBriefGuidanceAsksForEngineAndPlatformAndRigging(t *testing.T) {
	app := shippedApplication(t)
	brief := app.Chat.Model3DUsage.Brief
	var usageCard string
	for _, kind := range app.Chat.Choice.Kinds {
		if kind.OptionSource == "usage_profiles" {
			usageCard = kind.Message
		}
	}
	var tool string
	for _, candidate := range app.Chat.AgentTools {
		if candidate.Execution.ImplementationKey == "session.brief" {
			tool = candidate.Description
		}
	}
	for name, text := range map[string]string{
		"system prompt":          app.Chat.Agent.SystemPrompt,
		"empty brief text":       brief.EmptyText,
		"brief context":          brief.ContextTemplate,
		"usage card message":     usageCard,
		"brief tool description": tool,
	} {
		if strings.TrimSpace(text) == "" {
			t.Fatalf("%s is not declared", name)
		}
		wants := []string{"引擎", "平台", "绑骨"}
		if name == "brief tool description" {
			wants = []string{"target_engine", "target_platform", "needs_rig"}
		}
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not mention %q", name, want)
			}
		}
	}
	// The brief context must not hand the decision to the Agent: engine,
	// platform and rigging are the user's to state.
	if !strings.Contains(brief.ContextTemplate, "不能替用户决定") {
		t.Error("the brief context lets the Agent decide the engine, platform or rigging for the user")
	}
}

// TestShippedAssetCraftCoversAssetTypesAndStyles is #286: craft knowledge by
// asset type and by style lives in one Schema craft prompt gated like the 3D
// craft, short enough to stay publishable.
func TestShippedAssetCraftCoversAssetTypesAndStyles(t *testing.T) {
	app := shippedApplication(t)
	var assets, model3d *catalog.ChatAgentCraftPrompt
	for index := range app.Chat.Agent.CraftPrompts {
		switch app.Chat.Agent.CraftPrompts[index].ID {
		case "model3d_assets":
			assets = &app.Chat.Agent.CraftPrompts[index]
		case "model3d":
			model3d = &app.Chat.Agent.CraftPrompts[index]
		}
	}
	if assets == nil || model3d == nil {
		t.Fatalf("craft prompts = %+v", app.Chat.Agent.CraftPrompts)
	}
	for _, want := range []string{
		"角色", "武器", "道具", "载具", "建筑与场景件",
		"写实 PBR", "手绘", "卡通渲染", "低模", "像素风",
		"法线", "无缝", "LOD", "A 姿态", "模块",
	} {
		if !strings.Contains(assets.Text, want) {
			t.Errorf("the asset craft omits %q", want)
		}
	}
	// The same gates as the 3D craft: the 3D stage, or a brief headed for 3D.
	if assets.WhenDomain != model3d.WhenDomain || !slices.Equal(assets.WhenBriefAny, model3d.WhenBriefAny) {
		t.Fatalf("asset craft gates = %q %v, 3D craft = %q %v",
			assets.WhenDomain, assets.WhenBriefAny, model3d.WhenDomain, model3d.WhenBriefAny)
	}
	for _, tool := range []string{"create_model_3d", "create_model_3d_from_image", "create_model_3d_from_views"} {
		if !slices.Contains(assets.WhenToolsAny, tool) {
			t.Errorf("the asset craft is not triggered by %s", tool)
		}
	}
	// Compact: the knowledge costs every 3D turn, so it stays small, and far
	// inside the bound a published Agent configuration shares.
	length := utf8.RuneCountInString(assets.Text)
	if length > 1200 || length > app.AgentConfiguration.Limits.MaxPromptCharacters {
		t.Fatalf("the asset craft has %d runes", length)
	}
}

// TestShippedRiggedCharacterRouteIsDeclaredAsGuidance is #286: a character that
// needs rigging defaults to the A-pose three-view then multiview-to-3D route.
// The route is Schema guidance (craft text and the system prompt), and the
// purpose still recommends multiview Offerings to serve it.
func TestShippedRiggedCharacterRouteIsDeclaredAsGuidance(t *testing.T) {
	app := shippedApplication(t)
	var craft string
	for _, prompt := range app.Chat.Agent.CraftPrompts {
		if prompt.ID == "model3d" {
			craft = prompt.Text
		}
	}
	for name, text := range map[string]string{"3D craft": craft, "system prompt": app.Chat.Agent.SystemPrompt} {
		for _, want := range []string{"A 姿态", "create_three_view", "create_model_3d_from_views", "humanoid"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s omits %q from the rigged-character route", name, want)
			}
		}
	}
	for _, profile := range app.Chat.Model3DUsage.Profiles {
		if profile.ID == "game_character" {
			if len(profile.RecommendedOfferings["multiview-to-3d"]) == 0 {
				t.Fatal("the game character purpose recommends no multiview Offering for the route")
			}
			return
		}
	}
	t.Fatal("no game_character purpose")
}

// TestShippedInput3DChecklistDeclaresVisualItems is #286: pose, perspective and
// cast shadows are Agent-judged items of the 3d_input checklist, declared in
// the Schema with the argument that records the verdicts.
func TestShippedInput3DChecklistDeclaresVisualItems(t *testing.T) {
	app := shippedApplication(t)
	review := app.Chat.ImageReview
	var ids []string
	for _, item := range review.Input3DVisualItems {
		ids = append(ids, item.ID)
		if strings.TrimSpace(item.Label) == "" || strings.TrimSpace(item.Instruction) == "" || strings.TrimSpace(item.FixHint) == "" {
			t.Errorf("visual item %q is incomplete: %+v", item.ID, item)
		}
	}
	if !slices.Equal(ids, []string{"pose", "perspective", "cast_shadow"}) {
		t.Fatalf("visual items = %v", ids)
	}
	if review.VisualReviewArgument != "visual_review" || review.VisualNoteMaxCharacters <= 0 {
		t.Fatalf("verdict argument = %q, note bound = %d", review.VisualReviewArgument, review.VisualNoteMaxCharacters)
	}
	for _, item := range review.Input3DVisualItems {
		if item.ID == "pose" && !(strings.Contains(item.Instruction, "A 姿态") && strings.Contains(item.Instruction, "T 姿态") &&
			strings.Contains(item.Instruction, "needs_rig")) {
			t.Errorf("the pose item does not require an A or T pose for a rigged character: %q", item.Instruction)
		}
	}
	for _, want := range []string{"visual_items", "visual_review", "自检记录"} {
		if !strings.Contains(review.Input3DChecklist, want) {
			t.Errorf("the 3d_input checklist text omits %q", want)
		}
	}
	// The deterministic report is unchanged: these are not pixel checks.
	if review.Input3D.RequirePlainBackground != true || review.Input3D.MaxComponents != 1 {
		t.Fatalf("the deterministic 3d_input checklist changed: %+v", review.Input3D)
	}
}

// TestGameAssetDeclarationsAreValidated is #286: a Catalog that breaks one of
// the new declarations does not load.
func TestGameAssetDeclarationsAreValidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *catalog.ApplicationSchema)
		want   string
	}{
		{"a select without options", func(t *testing.T, a *catalog.ApplicationSchema) {
			a.Chat.Model3DUsage.Brief.Fields[fieldIndex(t, a, "target_engine")].Options = nil
		}, "options"},
		{"a duplicate option id", func(t *testing.T, a *catalog.ApplicationSchema) {
			field := &a.Chat.Model3DUsage.Brief.Fields[fieldIndex(t, a, "target_platform")]
			field.Options = append(field.Options, field.Options[0])
		}, "重复"},
		{"options on a boolean", func(t *testing.T, a *catalog.ApplicationSchema) {
			field := &a.Chat.Model3DUsage.Brief.Fields[fieldIndex(t, a, "needs_rig")]
			field.Options = []catalog.ChatProductionBriefOption{{ID: "yes", Label: "是"}}
		}, "不是 select"},
		{"an option the brief tool does not offer", func(t *testing.T, a *catalog.ApplicationSchema) {
			field := &a.Chat.Model3DUsage.Brief.Fields[fieldIndex(t, a, "target_engine")]
			field.Options = append(field.Options, catalog.ChatProductionBriefOption{ID: "cryengine", Label: "CryEngine"})
		}, "enum"},
		{"a refusal without the select wording", func(t *testing.T, a *catalog.ApplicationSchema) {
			a.Chat.Model3DUsage.Brief.Messages.SelectUndeclared = ""
		}, "select_undeclared"},
		{"project conventions with other options", func(t *testing.T, a *catalog.ApplicationSchema) {
			for index := range a.Project.Conventions.Fields {
				if a.Project.Conventions.Fields[index].Name == "target_platform" {
					a.Project.Conventions.Fields[index].Options = a.Project.Conventions.Fields[index].Options[1:]
				}
			}
		}, "options"},
		{"a craft prompt gated by an undeclared brief field", func(t *testing.T, a *catalog.ApplicationSchema) {
			a.Chat.Agent.CraftPrompts[len(a.Chat.Agent.CraftPrompts)-1].WhenBriefAny = []string{"no_such_field"}
		}, "when_brief_any"},
		{"a visual item without a fix hint", func(t *testing.T, a *catalog.ApplicationSchema) {
			a.Chat.ImageReview.Input3DVisualItems[0].FixHint = " "
		}, "fix_hint"},
		{"a duplicate visual item", func(t *testing.T, a *catalog.ApplicationSchema) {
			items := &a.Chat.ImageReview.Input3DVisualItems
			*items = append(*items, (*items)[0])
		}, "重复"},
		{"a visual item id that is a sentence", func(t *testing.T, a *catalog.ApplicationSchema) {
			a.Chat.ImageReview.Input3DVisualItems[0].ID = "Pose Check"
		}, "小写标识"},
		{"a verdict argument that collides", func(t *testing.T, a *catalog.ApplicationSchema) {
			a.Chat.ImageReview.VisualReviewArgument = "checklist"
		}, "重名"},
		{"no note bound", func(t *testing.T, a *catalog.ApplicationSchema) {
			a.Chat.ImageReview.VisualNoteMaxCharacters = 0
		}, "visual_note_max_characters"},
		{"an argument without items", func(t *testing.T, a *catalog.ApplicationSchema) {
			a.Chat.ImageReview.Input3DVisualItems = nil
		}, "input_3d_visual_items"},
		{"a verdict argument the tool does not declare", func(t *testing.T, a *catalog.ApplicationSchema) {
			a.Chat.ImageReview.VisualReviewArgument = "visual_verdicts"
		}, "visual_verdicts"},
		{"a tool item enum that drifted", func(t *testing.T, a *catalog.ApplicationSchema) {
			a.Chat.ImageReview.Input3DVisualItems = a.Chat.ImageReview.Input3DVisualItems[:2]
		}, "item enum"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := shippedApplication(t)
			test.mutate(t, &app)
			err := catalog.ValidateApplicationSchema(app)
			if err == nil {
				t.Fatal("ValidateApplicationSchema accepted the broken declaration")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want mention of %q", err, test.want)
			}
		})
	}
}

func fieldIndex(t *testing.T, app *catalog.ApplicationSchema, name string) int {
	t.Helper()
	for index, field := range app.Chat.Model3DUsage.Brief.Fields {
		if field.Name == name {
			return index
		}
	}
	t.Fatalf("brief field %q is not declared", name)
	return -1
}
