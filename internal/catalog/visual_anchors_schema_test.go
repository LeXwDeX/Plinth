package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func loadChatApplication(t *testing.T) catalog.ApplicationSchema {
	t.Helper()
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return loaded.Application()
}

func agentTool(app *catalog.ApplicationSchema, name string) *catalog.ChatAgentToolSchema {
	for index := range app.Chat.AgentTools {
		if app.Chat.AgentTools[index].Name == name {
			return &app.Chat.AgentTools[index]
		}
	}
	return nil
}

func generationTool(app *catalog.ApplicationSchema, name string) *catalog.ChatToolSchema {
	for index := range app.Chat.Tools {
		if app.Chat.Tools[index].Name == name {
			return &app.Chat.Tools[index]
		}
	}
	return nil
}

// #250: the shipped anchors block, tool and confirmation card are declared and
// validated together.
func TestDefaultVisualAnchorsDeclarationIsValid(t *testing.T) {
	app := loadChatApplication(t)
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatal(err)
	}
	anchors := app.Chat.VisualAnchors
	if anchors.MaximumAnchors <= 0 || anchors.MaximumReferences <= 0 ||
		len(anchors.Kinds) != 2 || anchors.OpenMarker == "" || anchors.CloseMarker == "" {
		t.Fatalf("visual_anchors = %+v", anchors)
	}
	tool := agentTool(&app, "set_visual_anchor")
	if tool == nil || tool.Execution.ImplementationKey != "anchor.write" ||
		tool.Execution.AgentScope != "utility" || len(tool.Execution.Effects) != 1 ||
		tool.Execution.Effects[0] != "SESSION_WRITE" {
		t.Fatalf("set_visual_anchor = %+v", tool)
	}
	refine := generationTool(&app, "refine_image")
	model := generationTool(&app, "create_model_3d_from_image")
	if refine == nil || refine.AnchorArgument != "anchors" {
		t.Fatalf("refine_image anchor argument = %+v", refine)
	}
	if model == nil || model.AnchorArgument != "anchors" {
		t.Fatalf("create_model_3d_from_image anchor argument = %+v", model)
	}
}

func TestVisualAnchorsDeclarationIsValidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
		want   string
	}{
		{"an anchors block without the tool", func(app *catalog.ApplicationSchema) {
			app.Chat.VisualAnchors = catalog.ChatVisualAnchorsSchema{}
			removeVisualAnchorChoice(t, app)
		}, ""},
		{"an anchors block whose tool is gone", func(app *catalog.ApplicationSchema) {
			removeVisualAnchorChoice(t, app)
		}, "需要已声明的 set_visual_anchor 工具"},
		{"a zero bound", func(app *catalog.ApplicationSchema) {
			app.Chat.VisualAnchors.MaximumAnchors = 0
		}, "上限必须为正数"},
		{"no kinds", func(app *catalog.ApplicationSchema) {
			app.Chat.VisualAnchors.Kinds = nil
		}, "kinds 不能为空"},
		{"an unknown kind", func(app *catalog.ApplicationSchema) {
			app.Chat.VisualAnchors.Kinds[0].ID = "costume"
		}, "id 无效"},
		{"a duplicate kind", func(app *catalog.ApplicationSchema) {
			app.Chat.VisualAnchors.Kinds[1].ID = "identity"
		}, "id 重复"},
		{"a kind without a purpose", func(app *catalog.ApplicationSchema) {
			app.Chat.VisualAnchors.Kinds[0].ReferencePurposeTemplate = ""
		}, "不完整"},
		{"a missing context template", func(app *catalog.ApplicationSchema) {
			app.Chat.VisualAnchors.ContextTemplate = ""
		}, "context_template 不能为空"},
		{"a context template without precedence", func(app *catalog.ApplicationSchema) {
			app.Chat.VisualAnchors.ContextTemplate = "{open}{anchors}{style_lock}{close}"
		}, "context_template 缺少占位符"},
		{"an item template without the description", func(app *catalog.ApplicationSchema) {
			app.Chat.VisualAnchors.ItemTemplate = "{kind}{name}{references}"
		}, "item_template 缺少占位符"},
		{"a prompt hint without the argument", func(app *catalog.ApplicationSchema) {
			app.Chat.VisualAnchors.PromptHint = "anchors: {names}"
		}, "模板缺少占位符"},
		{"one marker", func(app *catalog.ApplicationSchema) {
			app.Chat.VisualAnchors.CloseMarker = app.Chat.VisualAnchors.OpenMarker
		}, "open_marker"},
		{"an action enum without decline", func(app *catalog.ApplicationSchema) {
			tool := agentTool(app, "set_visual_anchor")
			tool.Parameters = replaceEnum(t, tool.Parameters, "action", []string{"create", "update", "delete"})
		}, "enum 必须覆盖"},
		{"a kind enum that disagrees", func(app *catalog.ApplicationSchema) {
			tool := agentTool(app, "set_visual_anchor")
			tool.Parameters = replaceEnum(t, tool.Parameters, "kind", []string{"identity"})
		}, "kind 的 enum"},
		{"no required action", func(app *catalog.ApplicationSchema) {
			tool := agentTool(app, "set_visual_anchor")
			tool.Parameters = json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["create","update","delete","decline"]},"kind":{"type":"string","enum":["identity","style"]}},"required":[],"additionalProperties":false}`)
		}, "required 必须包含 action"},
		{"no confirmation card", func(app *catalog.ApplicationSchema) {
			app.Chat.Choice.Kinds = withoutChoiceKind(app.Chat.Choice.Kinds, "visual_anchor")
			ask := agentTool(app, "ask_choice")
			values := make([]string, 0, len(app.Chat.Choice.Kinds))
			for _, kind := range app.Chat.Choice.Kinds {
				values = append(values, kind.ID)
			}
			ask.Parameters = replaceEnum(t, ask.Parameters, "kind", values)
		}, "必须声明 visual_anchor 确认卡"},
		{"no trimmed message", func(app *catalog.ApplicationSchema) {
			app.Chat.Presentation.Messages.ReferencePriorityTrimmed = ""
		}, "reference_priority_trimmed 不能为空"},
		{"a trimmed message without anchors", func(app *catalog.ApplicationSchema) {
			app.Chat.Presentation.Messages.ReferencePriorityTrimmed = "max {maximum}: {dropped}"
		}, "缺少占位符"},
		{"an unknown anchor argument", func(app *catalog.ApplicationSchema) {
			generationTool(app, "refine_image").AnchorArgument = "missing"
		}, "不是工具参数"},
		{"a non-array anchor argument", func(app *catalog.ApplicationSchema) {
			generationTool(app, "refine_image").AnchorArgument = "prompt"
		}, "必须是字符串数组参数"},
		{"an anchor argument on a text-only tool", func(app *catalog.ApplicationSchema) {
			generationTool(app, "create_motion_clip").AnchorArgument = "prompt"
		}, "只适用于读取参考图的生成工具"},
		{"a style lock without its Agent context", func(app *catalog.ApplicationSchema) {
			app.Chat.StyleLock.AgentContextTemplate = ""
		}, "style_lock 的上限、字段与错误文案必须完整"},
		{"a style lock without its reference capabilities", func(app *catalog.ApplicationSchema) {
			app.Chat.StyleLock.ReferenceCapabilities = nil
		}, "style_lock 的上限、字段与错误文案必须完整"},
		{"a style reference on a 3D capability", func(app *catalog.ApplicationSchema) {
			app.Chat.StyleLock.ReferenceCapabilities = []schema.Capability{schema.ImageTo3D}
		}, "只能声明图片或处理能力"},
		{"a style lock context without the enabled state", func(app *catalog.ApplicationSchema) {
			app.Chat.StyleLock.AgentContextTemplate = "{text}{references}"
		}, "style_lock 的上限、字段与错误文案必须完整"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := loadChatApplication(t)
			test.mutate(&app)
			err := catalog.ValidateApplicationSchema(app)
			if test.want == "" {
				if err != nil {
					t.Fatalf("validation error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func withoutTool(tools []catalog.ChatAgentToolSchema, name string) []catalog.ChatAgentToolSchema {
	result := make([]catalog.ChatAgentToolSchema, 0, len(tools))
	for _, tool := range tools {
		if tool.Name != name {
			result = append(result, tool)
		}
	}
	return result
}

// removeVisualAnchorChoice drops the anchor write tool while keeping the
// Schema's other declarations self-consistent: the visual_anchor kind is
// repointed at the usage-profile tool, so only the anchors block itself is
// left to validate.
func removeVisualAnchorChoice(t *testing.T, app *catalog.ApplicationSchema) {
	t.Helper()
	app.Chat.AgentTools = withoutTool(app.Chat.AgentTools, "set_visual_anchor")
	for index := range app.Chat.Choice.Kinds {
		if app.Chat.Choice.Kinds[index].ID != "visual_anchor" {
			continue
		}
		app.Chat.Choice.Kinds[index].ToolName = "update_production_brief"
		app.Chat.Choice.Kinds[index].Argument = "purpose"
		app.Chat.Choice.Kinds[index].OptionSource = "usage_profiles"
	}
}

func withoutChoiceKind(kinds []catalog.ChatChoiceKindSchema, id string) []catalog.ChatChoiceKindSchema {
	result := make([]catalog.ChatChoiceKindSchema, 0, len(kinds))
	for _, kind := range kinds {
		if kind.ID != id {
			result = append(result, kind)
		}
	}
	return result
}

func replaceEnum(t *testing.T, raw json.RawMessage, property string, values []string) json.RawMessage {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	properties, _ := schema["properties"].(map[string]any)
	field, _ := properties[property].(map[string]any)
	if field == nil {
		t.Fatalf("property %q missing", property)
	}
	items := make([]any, 0, len(values))
	for _, value := range values {
		items = append(items, value)
	}
	field["enum"] = items
	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
