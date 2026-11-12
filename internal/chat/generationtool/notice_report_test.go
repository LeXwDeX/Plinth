package generationtool_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #284: a tool result tells the Agent that a succeeded Task did not deliver
// everything it was asked for, with the Schema sentence in the turn's locale
// and the positions it names, so the Agent reports "pictures 2 and 4 were not
// matted" instead of a finished batch. The key is the tool's declared one.

func noticeResult(t *testing.T, toolName, locale string, notices []generation.TaskNotice, withTexts bool) map[string]any {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Name == toolName {
			tool = candidate
		}
	}
	if tool.Name == "" {
		t.Fatalf("tool %q is not declared", toolName)
	}
	offering := generation.Offering{
		ID: "configured/offering", ModelID: "configured-model", ModelName: "Configured Model",
		Capability: tool.FallbackCapability, Enabled: true, Timeout: time.Second,
	}
	task := generation.Task{
		ID: "task-a", OwnerID: "owner-a", SessionID: "session-a", Capability: tool.FallbackCapability,
		State: generation.TaskSucceeded, Progress: 100, Offering: offering, Notices: notices,
	}
	config := generationtool.Config{
		Gateway:    &fakeGateway{offerings: []generation.Offering{offering}, tasks: []generation.Task{task, task, task}},
		References: &fakeReferences{}, Chat: application.Chat, TaskStates: application.TaskStates,
	}
	if withTexts {
		texts, err := catalog.NewTextLocalizer(application)
		if err != nil {
			t.Fatal(err)
		}
		config.Texts = texts
	}
	executor, err := generationtool.New(config)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", BoundTaskID: "task-a",
		Call:    agents.ToolCall{ID: "call-a", Name: tool.Name, Arguments: json.RawMessage(`{}`)},
		Tool:    tool,
		Options: chat.TurnOptions{Locale: locale},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatalf("agent content %q: %v", result.AgentContent, err)
	}
	if content["success"] != true {
		t.Fatalf("result = %v", content)
	}
	return content
}

func TestToolResultReportsTheNoticesOfASucceededTask(t *testing.T) {
	partial := []generation.TaskNotice{{Code: generation.NoticePartialInputs, Inputs: []int{2, 4}}}
	for _, test := range []struct {
		locale, message string
	}{
		{"", "第 2 张、第 4 张没有处理成功，其余已完成。"},
		{"en", "Not processed: picture 2, picture 4. The rest are done."},
	} {
		content := noticeResult(t, "remove_background", test.locale, partial, true)
		want := []any{map[string]any{"code": "partial_inputs", "message": test.message, "inputs": []any{float64(2), float64(4)}}}
		if !reflect.DeepEqual(content["notices"], want) {
			t.Fatalf("locale %q: notices = %#v, want %#v", test.locale, content["notices"], want)
		}
	}
}

// Without a localizer the Agent still gets the code and the positions.
func TestToolResultReportsNoticeCodesWithoutALocalizer(t *testing.T) {
	content := noticeResult(t, "remove_background", "", []generation.TaskNotice{{Code: generation.NoticePartialInputs, Inputs: []int{3}}}, false)
	want := []any{map[string]any{"code": "partial_inputs", "inputs": []any{float64(3)}}}
	if !reflect.DeepEqual(content["notices"], want) {
		t.Fatalf("notices = %#v, want %#v", content["notices"], want)
	}
}

// A Task that delivered everything leaves the key out, and a tool that
// declares no notices key does not report them.
func TestToolResultLeavesTheNoticesKeyOutWhenThereAreNone(t *testing.T) {
	if content := noticeResult(t, "remove_background", "", nil, true); content["notices"] != nil {
		t.Fatalf("a Task without notices reported %#v", content["notices"])
	}
	partial := []generation.TaskNotice{{Code: generation.NoticePartialInputs, Inputs: []int{1}}}
	if content := noticeResult(t, "refine_image", "", partial, true); content["notices"] != nil {
		t.Fatalf("a tool that declares no notices key reported %#v", content["notices"])
	}
}
