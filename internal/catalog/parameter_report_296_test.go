package catalog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// #296: a generation tool result lists the frozen parameters that differ from
// the Offering's default under result_fields.parameters. The Catalog declares
// the key only where the executor writes it, and bounds one reported value.

func parameterReportCatalog(t *testing.T, mutate func(chat map[string]any)) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(defaultCatalog, &document); err != nil {
		t.Fatal(err)
	}
	mutate(document["application"].(map[string]any)["chat"].(map[string]any))
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func parameterReportTool(t *testing.T, chat map[string]any, name string) map[string]any {
	t.Helper()
	for _, candidate := range chat["tools"].([]any) {
		if tool := candidate.(map[string]any); tool["name"] == name {
			return tool
		}
	}
	t.Fatalf("tool %q is not declared", name)
	return nil
}

func TestDefaultCatalogDeclaresTheParametersKeyOnEveryTaskTool(t *testing.T) {
	loaded, err := Load(bytes.NewReader(defaultCatalog))
	if err != nil {
		t.Fatal(err)
	}
	chat := loaded.Application().Chat
	if chat.Agent.ParameterReport.MaximumValueCharacters <= 0 {
		t.Fatalf("parameter_report = %+v, want a positive value bound", chat.Agent.ParameterReport)
	}
	submitting := 0
	for _, tool := range chat.Tools {
		isPlan := tool.Processing != nil && tool.Processing.Plan != nil
		switch {
		case isPlan && tool.ResultFields.Parameters != "":
			t.Errorf("%s builds a plan, not a Task, yet declares result_fields.parameters", tool.Name)
		case !isPlan && tool.ResultFields.Parameters != "parameters":
			t.Errorf("%s submits a Task but its result_fields.parameters = %q", tool.Name, tool.ResultFields.Parameters)
		case !isPlan:
			submitting++
		}
	}
	if submitting == 0 {
		t.Fatal("no tool declares result_fields.parameters")
	}
}

func TestCatalogRefusesTheParametersKeyWhereTheExecutorDoesNotWriteIt(t *testing.T) {
	cases := map[string]struct {
		mutate func(chat map[string]any)
		want   string
	}{
		"on the processing plan tool": {func(chat map[string]any) {
			parameterReportTool(t, chat, "plan_model_processing")["result_fields"].(map[string]any)["parameters"] = "parameters"
		}, "不适用于加工计划工具"},
		"under the key of another result field": {func(chat map[string]any) {
			parameterReportTool(t, chat, "create_model_3d")["result_fields"].(map[string]any)["parameters"] = "task_id"
		}, "使用了同一个键"},
		"under a key the executor writes itself": {func(chat map[string]any) {
			parameterReportTool(t, chat, "create_model_3d")["result_fields"].(map[string]any)["parameters"] = "native_quote"
		}, "保留键"},
		"without a value bound": {func(chat map[string]any) {
			delete(chat["agent"].(map[string]any), "parameter_report")
		}, "必须大于 0"},
		"with a negative value bound": {func(chat map[string]any) {
			chat["agent"].(map[string]any)["parameter_report"] = map[string]any{"maximum_value_characters": -1}
		}, "不能为负数"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(bytes.NewReader(parameterReportCatalog(t, test.mutate)))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("an unsafe parameters declaration got %v, want an error containing %q", err, test.want)
			}
		})
	}
}

func TestCatalogWithoutTheParametersKeyNeedsNoBound(t *testing.T) {
	raw := parameterReportCatalog(t, func(chat map[string]any) {
		for _, candidate := range chat["tools"].([]any) {
			delete(candidate.(map[string]any)["result_fields"].(map[string]any), "parameters")
		}
		delete(chat["agent"].(map[string]any), "parameter_report")
	})
	if _, err := Load(bytes.NewReader(raw)); err != nil {
		t.Fatalf("a Catalog that predates the report must still load: %v", err)
	}
}

// The Agent is told once in the system prompt and in the 3D and 2D craft texts
// to report model parameters only as the tool result lists them.
func TestAgentTextsTellItToReportParametersOnlyFromTheToolResult(t *testing.T) {
	loaded, err := Load(bytes.NewReader(defaultCatalog))
	if err != nil {
		t.Fatal(err)
	}
	agent := loaded.Application().Chat.Agent
	texts := map[string]string{"system_prompt": agent.SystemPrompt}
	for _, craft := range agent.CraftPrompts {
		if craft.ID == "model3d" || craft.ID == "image2d_production" {
			texts["craft "+craft.ID] = craft.Text
		}
	}
	if len(texts) != 3 {
		t.Fatalf("texts found: %d", len(texts))
	}
	for name, text := range texts {
		for _, want := range []string{"parameters", "没有列出的参数就是默认值", "不得说成设置过"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
		if count := strings.Count(text, "没有列出的参数就是默认值"); count != 1 {
			t.Errorf("%s states the rule %d times, want once", name, count)
		}
	}
}
