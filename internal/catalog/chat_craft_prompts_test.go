package catalog_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// The one shipped craft brief must cover exactly the 3D toolbox: every tool
// the Agent scope classifies as 3d, and no 2D or utility tool that would make
// a 2D turn pay for it.
func TestShippedCraftPromptCoversThe3DToolbox(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	prompts := app.Chat.Agent.CraftPrompts
	if len(prompts) != 1 || prompts[0].ID != "model3d" {
		t.Fatalf("craft prompts = %#v", prompts)
	}
	var declared []string
	for _, tool := range app.Chat.Tools {
		if tool.Execution.AgentScope == "3d" {
			declared = append(declared, tool.Name)
		}
	}
	for _, tool := range app.Chat.AgentTools {
		if tool.Execution.AgentScope == "3d" {
			declared = append(declared, tool.Name)
		}
	}
	triggers := slices.Clone(prompts[0].WhenToolsAny)
	slices.Sort(declared)
	slices.Sort(triggers)
	if !slices.Equal(triggers, declared) {
		t.Fatalf("model3d triggers = %v, 3d tools = %v", triggers, declared)
	}
	if !strings.HasPrefix(prompts[0].Text, "【3D 制作要领】") || !strings.Contains(prompts[0].Text, "自检要点") {
		t.Fatalf("model3d text = %q", prompts[0].Text)
	}
	// The brief names the fields inspect_model really returns, not a summary
	// that the Agent would have to map by hand. The actual JSON names come from
	// modelinspect's topology report, so a rename there breaks this assertion.
	topology := reflect.TypeOf(modelinspect.TopologyReport{})
	jsonFields := make(map[string]struct{}, topology.NumField())
	for index := 0; index < topology.NumField(); index++ {
		name, _, _ := strings.Cut(topology.Field(index).Tag.Get("json"), ",")
		jsonFields[name] = struct{}{}
	}
	for _, field := range []string{"connected_components", "debris_components", "debris_triangles"} {
		if _, ok := jsonFields[field]; !ok {
			t.Fatalf("modelinspect.TopologyReport no longer returns %q", field)
		}
		if !strings.Contains(prompts[0].Text, field) {
			t.Fatalf("model3d text omits inspect field %q", field)
		}
	}
	if strings.Contains(prompts[0].Text, "connected_components 和 debris") {
		t.Fatal("model3d text still uses the old debris field summary")
	}
}

// The facts #120 added to inspect_model reach the Agent: the winding and
// inside-out topology fields, the bind-pose warning and the strip triangle
// count are explained both in the tool description and in the 3D craft
// brief. Field names come from modelinspect's report, so a rename there
// breaks this test instead of leaving a stale explanation.
func TestInspectFactsAreExplainedToTheAgent(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	topology := reflect.TypeOf(modelinspect.TopologyReport{})
	var fields []string
	for _, name := range []string{"InconsistentWindingEdges", "InsideOut"} {
		field, ok := topology.FieldByName(name)
		if !ok {
			t.Fatalf("modelinspect.TopologyReport has no field %s", name)
		}
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		fields = append(fields, "topology."+tag)
	}
	var description string
	for _, tool := range app.Chat.AgentTools {
		if tool.Name == app.Chat.ModelReview.Tools.Inspect {
			description = tool.Description
		}
	}
	if description == "" {
		t.Fatal("inspect_model is not declared")
	}
	craft := app.Chat.Agent.CraftPrompts[0].Text
	for _, want := range append(fields, "skin_bind_pose", "strip") {
		if !strings.Contains(description, want) {
			t.Fatalf("inspect_model description omits %q", want)
		}
		if !strings.Contains(craft, want) {
			t.Fatalf("model3d craft brief omits %q", want)
		}
	}
}

func TestCraftPromptDeclarationsAreValidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
		want   string
	}{
		{"empty id", func(a *catalog.ApplicationSchema) {
			a.Chat.Agent.CraftPrompts[0].ID = " "
		}, "craft_prompts"},
		{"duplicate id", func(a *catalog.ApplicationSchema) {
			a.Chat.Agent.CraftPrompts = append(a.Chat.Agent.CraftPrompts, a.Chat.Agent.CraftPrompts[0])
		}, "重复"},
		{"duplicate id after trimming", func(a *catalog.ApplicationSchema) {
			a.Chat.Agent.CraftPrompts = append(a.Chat.Agent.CraftPrompts, a.Chat.Agent.CraftPrompts[0])
			a.Chat.Agent.CraftPrompts[1].ID = " model3d "
		}, "重复"},
		{"no trigger tools", func(a *catalog.ApplicationSchema) {
			a.Chat.Agent.CraftPrompts[0].WhenToolsAny = nil
		}, "when_tools_any"},
		{"unregistered tool", func(a *catalog.ApplicationSchema) {
			a.Chat.Agent.CraftPrompts[0].WhenToolsAny = append(a.Chat.Agent.CraftPrompts[0].WhenToolsAny, "no_such_tool")
		}, "未注册工具"},
		{"empty text", func(a *catalog.ApplicationSchema) {
			a.Chat.Agent.CraftPrompts[0].Text = " "
		}, "text 不能为空"},
		{"text over the limit", func(a *catalog.ApplicationSchema) {
			a.AgentConfiguration.Limits.MaxPromptCharacters = 1
		}, "超过"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			testCase.mutate(&app)
			err = catalog.ValidateApplicationSchema(app)
			if err == nil {
				t.Fatal("ValidateApplicationSchema accepted an invalid craft prompt")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want mention of %q", err, testCase.want)
			}
		})
	}
}

// The shipped prompt and the craft brief share the Agent configuration's
// character bound: a reset to Schema defaults must stay publishable.
func TestShippedPromptsFitTheAgentConfigurationLimit(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	limit := app.AgentConfiguration.Limits.MaxPromptCharacters
	systemLength := utf8.RuneCountInString(app.Chat.Agent.SystemPrompt)
	if systemLength > limit {
		t.Fatalf("system prompt has %d runes, limit %d", systemLength, limit)
	}
	for _, prompt := range app.Chat.Agent.CraftPrompts {
		length := utf8.RuneCountInString(prompt.Text)
		if length > limit {
			t.Fatalf("craft prompt %q has %d runes, limit %d", prompt.ID, length, limit)
		}
		t.Logf("craft prompt %q: %d/%d runes", prompt.ID, length, limit)
	}
	t.Logf("system prompt: %d/%d runes", systemLength, limit)
}

// Approval prompt: the Agent-scope explanation is gone, and the budget mode
// names the fields propose_production_plan really declares (#103).
func TestApprovalPromptKeepsTheBudgetFieldsAndDropsTheAgentScope(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	prompt := c.Application().Chat.ApprovalPolicy.Prompt
	if strings.Contains(prompt.ContextTemplate, "Agent 范围") {
		t.Fatal("the approval prompt still explains the Agent scope")
	}
	var budget string
	for _, mode := range prompt.Modes {
		if mode.ID == "budget" {
			budget = mode.Description
		}
	}
	for _, want := range []string{
		"propose_production_plan", "title", "steps", "capability", "purpose", "model",
		"estimated_credits", "budgets", "legacy.credits", "correction_capabilities", "重新估算", "从不相加",
	} {
		if !strings.Contains(budget, want) {
			t.Errorf("budget description omits %q", want)
		}
	}
}

// The input-area scope selector is removed this version; the server-side
// refusal texts must say the step cannot be done instead of naming a control
// the person no longer has.
func TestAgentScopeTextsNoLongerPointAtARemovedSelector(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := c.Application().Chat
	absent := []string{"右下角", "输入框", "切换为合适的一项"}
	for name, text := range map[string]string{
		"context template":   chat.AgentScope.ContextTemplate,
		"rejection template": chat.AgentScope.RejectionTemplate,
		"mock refusal":       chat.AgentScope.MockRefusal,
	} {
		for _, want := range absent {
			if strings.Contains(text, want) {
				t.Errorf("%s still mentions %q", name, want)
			}
		}
	}
	if !strings.Contains(chat.AgentScope.ContextTemplate, "这一步当前不可用") ||
		!strings.Contains(chat.AgentScope.ContextTemplate, "这一步做不了") {
		t.Error("the context template does not say the step is unavailable")
	}
	if !strings.Contains(chat.AgentScope.ContextTemplate, "管理员配置为") ||
		strings.Contains(chat.AgentScope.ContextTemplate, "用户为本会话选择了") {
		t.Error("the context template still attributes the scope to the person")
	}
	if !strings.Contains(chat.AgentScope.RejectionTemplate, "currently unavailable") ||
		!strings.Contains(chat.AgentScope.RejectionTemplate, "cannot be done") {
		t.Error("the rejection template does not say the step is unavailable")
	}
	if !strings.Contains(chat.AgentScope.MockRefusal, "这一步当前不可用") {
		t.Error("the mock refusal does not say the step is unavailable")
	}
	english := c.Application().Localization.Bundles["en"].Fields["chat.agent_scope.mock_refusal"]
	if !strings.Contains(english, "currently unavailable") || strings.Contains(english, "bottom right") ||
		strings.Contains(english, "Switch the mode") {
		t.Errorf("english mock refusal = %q", english)
	}
}

// #130: the craft brief used to say remesh keeps texture detail by baking.
// Only the baked decimation does; remesh loses textures and may change size,
// and the brief sends the Agent to the Offering descriptions for the details.
func TestCraftPromptDoesNotPromiseThatRemeshKeepsTextures(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	text := c.Application().Chat.Agent.CraftPrompts[0].Text
	if strings.Contains(text, "重拓扑（烘焙保留贴图") {
		t.Fatal("craft brief still says remesh keeps textures by baking")
	}
	for _, want := range []string{"list_offerings", "description", "带烘焙的减面", "重拓扑（如 Meshy 重拓扑）不保留贴图", "尺寸也可能改变", "换贴图", "精确尺寸", "不要再叠加重拓扑"} {
		if !strings.Contains(text, want) {
			t.Errorf("craft brief omits %q", want)
		}
	}
}
