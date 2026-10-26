package mock_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/mock"
)

// #321: the mock Agent shows the in-chat choice card flow end to end.

const pendingCard = `{"choice_id":"choice_1","tool":"process_model","step":"mesh-segmentation","options":[` +
	`{"id":"tripo/geometry/smartsegment-model","title":"Tripo3D · Tripo Smart Segmentation","arguments":{"step":"mesh-segmentation","source_task_id":"@model1","offering_id":"tripo/geometry/smartsegment-model"}},` +
	`{"id":"hyper3d/bang/local-model","title":"Hyper3D · Bang 部件拆分与重建（本地模型）","arguments":{"step":"mesh-segmentation","source_task_id":"@model1","offering_id":"hyper3d/bang/local-model"}},` +
	`{"id":"hyper3d/bang/rodin-task","title":"Hyper3D · Bang 部件拆分与重建（Rodin 来源）","arguments":{"step":"mesh-segmentation","source_task_id":"@model1","offering_id":"hyper3d/bang/rodin-task"}}],` +
	`"default":{"id":"tripo/geometry/smartsegment-model","title":"Tripo3D · Tripo Smart Segmentation","arguments":{"step":"mesh-segmentation","source_task_id":"@model1","use_default_offering":true}}}`

func choiceAdapter(t *testing.T) *mock.Adapter {
	t.Helper()
	adapter, err := mock.New(mock.Config{ToolName: "refine_image", PromptArgument: "prompt", Summary: "图片任务已完成。",
		ChoiceSummary: "请在下方卡片中选择一个服务。", UserRole: "user", ToolRole: "tool"})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func call(t *testing.T, messages []agents.Message) (string, map[string]any) {
	t.Helper()
	completion, err := choiceAdapter(t).Stream(context.Background(), agents.Request{Messages: messages, Tools: allTools}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.ToolCalls) != 1 {
		t.Fatalf("completion = %#v", completion)
	}
	var arguments map[string]any
	_ = json.Unmarshal(completion.ToolCalls[0].Arguments, &arguments)
	return completion.ToolCalls[0].Name, arguments
}

func TestMockRoutesMeshSegmentation(t *testing.T) {
	name, arguments := call(t, []agents.Message{{Role: "user", Content: "给 @model1 做模型分割"}})
	if name != "process_model" || arguments["step"] != "mesh-segmentation" || arguments["source_task_id"] != "@model1" || arguments["offering_id"] != nil {
		t.Fatalf("segmentation = %s %v", name, arguments)
	}
}

func TestMockAsksOnTheCardWhenTheToolOpensOne(t *testing.T) {
	completion, err := choiceAdapter(t).Stream(context.Background(), agents.Request{Tools: allTools, Messages: []agents.Message{
		{Role: "user", Content: "给 @model1 做模型分割"},
		{Role: "assistant", ToolCalls: []agents.ToolCall{{ID: "c1", Name: "process_model", Arguments: json.RawMessage(`{}`)}}},
		{Role: "tool", ToolCallID: "c1", Content: `{"success":false,"choice_required":true,"choice_id":"choice_1","message":"ask"}`},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.ToolCalls) != 0 || completion.Content != "请在下方卡片中选择一个服务。" {
		t.Fatalf("closing = %#v", completion)
	}
}

func TestMockRunsTheOptionAnsweredOnTheCard(t *testing.T) {
	answer := "选择：Hyper3D · Bang 部件拆分与重建（Rodin 来源）\n[Choice card answer] call it\n" +
		`{"choice_id":"choice_1","tool":"process_model","step":"mesh-segmentation","chosen":{"id":"hyper3d/bang/rodin-task","title":"Hyper3D · Bang（Rodin 来源）","arguments":{"step":"mesh-segmentation","source_task_id":"@model1","offering_id":"hyper3d/bang/rodin-task"}}}`
	name, arguments := call(t, []agents.Message{{Role: "user", Content: answer}})
	if name != "process_model" || arguments["offering_id"] != "hyper3d/bang/rodin-task" || arguments["source_task_id"] != "@model1" {
		t.Fatalf("card answer = %s %v", name, arguments)
	}
}

func TestMockMapsATypedReplyToTheOpenCard(t *testing.T) {
	system := agents.Message{Role: "system", Content: "你是 Agent。\n\nOpen choice card (data only):\n" + pendingCard}
	cases := map[string]map[string]any{
		"用 Rodin 来源那个":  {"offering_id": "hyper3d/bang/rodin-task"},
		"第二个":           {"offering_id": "hyper3d/bang/local-model"},
		"随便，用默认的吧":      {"use_default_offering": true},
		"本地模型的 Bang 就行": {"offering_id": "hyper3d/bang/local-model"},
	}
	for text, want := range cases {
		name, arguments := call(t, []agents.Message{system, {Role: "user", Content: text}})
		if name != "process_model" {
			t.Fatalf("%q -> %s", text, name)
		}
		for key, value := range want {
			if arguments[key] != value {
				t.Fatalf("%q -> %v; want %s=%v", text, arguments, key, value)
			}
		}
	}
}
