package mock_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/mock"
)

func projectAdapter(t *testing.T) *mock.Adapter {
	t.Helper()
	adapter, err := mock.New(mock.Config{
		ToolName: "refine_image", PromptArgument: "prompt", Summary: "图片任务已完成。",
		CitationTemplate: "引用项目资产：{assets}",
		UserRole:         "user", ToolRole: "tool",
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func TestMockRoutesASeriesRequestToRefineImageWithoutAHandle(t *testing.T) {
	for _, request := range []string{"做同系列的角色 B", "沿用项目角色 A 的设定，做角色 B", "按项目风格做一张新图"} {
		completion, err := projectAdapter(t).Stream(context.Background(), agents.Request{
			Messages: []agents.Message{{Role: "user", Content: request}},
			Tools:    []agents.Tool{{Name: "refine_image"}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(completion.ToolCalls) != 1 || completion.ToolCalls[0].Name != "refine_image" {
			t.Fatalf("%q tool calls = %+v", request, completion.ToolCalls)
		}
		var arguments map[string]any
		if err := json.Unmarshal(completion.ToolCalls[0].Arguments, &arguments); err != nil {
			t.Fatal(err)
		}
		if arguments["prompt"] != request {
			t.Fatalf("%q arguments = %#v", request, arguments)
		}
		if _, sent := arguments["reference_handles"]; sent {
			t.Fatalf("%q must let the server attach the project style, got %#v", request, arguments)
		}
	}
}

func TestMockClosesATurnWithTheProjectAssetCitation(t *testing.T) {
	completion, err := projectAdapter(t).Stream(context.Background(), agents.Request{
		Messages: []agents.Message{{
			Role: "tool",
			Content: `{"success":true,"task_id":"task-1","image_urls":["/api/v1/tasks/task-1/assets/a1"],` +
				`"style_sources":["角色 A"]}`,
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Content != "图片任务已完成。\n引用项目资产：角色 A" {
		t.Fatalf("summary = %q", completion.Content)
	}
	// A result without project assets keeps the plain summary.
	completion, err = projectAdapter(t).Stream(context.Background(), agents.Request{
		Messages: []agents.Message{{
			Role: "tool", Content: `{"success":true,"task_id":"task-1","image_urls":[]}`,
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(completion.Content, "引用项目资产") {
		t.Fatalf("plain summary = %q", completion.Content)
	}
}
