package mock_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #374: the direction card (kind next_step) as the mock Agent plays it. A fixed
// function leaves the Agent few tools; a request none of them can do is asked on
// a card whose first option clears the function selection, and the answer to the
// card is carried on in words.

// fixedFunction is what the server puts in the system context of a turn that runs
// under a fixed function: the Schema's template around the snapshot JSON
// (internal/chat harnessPrompt). The mock reads only the snapshot's key.
func fixedFunction(t *testing.T, tools ...string) agents.Message {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(map[string]any{
		"selection": map[string]any{"operation_id": "multiview-to-3d"}, "status": "ready", "missing_parameters": []string{},
		"executable_tools":      tools,
		"switchable_operations": []map[string]string{{"id": "retexture", "label": "换贴图", "description": "给模型换贴图"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return agents.Message{Role: "system", Content: "你是 Agent。" + strings.ReplaceAll(product.Application().Chat.Harness.ContextTemplate, "{snapshot}", string(snapshot))}
}

// fixedTools are the tools a fixed multiview function leaves: its own and the base ones.
var fixedTools = []agents.Tool{{Name: "create_model_3d_from_views"}, {Name: "list_offerings"}, {Name: "ask_choice"}}

func toolNames(tools []agents.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

// cardOptions reads the options of an ask_choice call, as the server's tool arguments carry them.
func cardOptions(t *testing.T, arguments map[string]any) []map[string]any {
	t.Helper()
	raw, _ := arguments["options"].([]any)
	options := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		option, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("option %#v", item)
		}
		options = append(options, option)
	}
	return options
}

func TestAFixedFunctionAsksTheDirectionCardForARequestItCannotDo(t *testing.T) {
	messages := []agents.Message{fixedFunction(t, toolNames(fixedTools)...), {Role: "user", Content: "先画一张角色立绘，再建模"}}
	name, arguments := plan(t, messages, fixedTools)
	if name != "ask_choice" || arguments["kind"] != "next_step" {
		t.Fatalf("got %s %#v", name, arguments)
	}
	options := cardOptions(t, arguments)
	if len(options) != 2 {
		t.Fatalf("options %#v", options)
	}
	// The first option clears the function selection, the second only continues; the first is recommended.
	if !reflect.DeepEqual(options[0]["action"], map[string]any{"type": "clear_operation"}) || options[1]["action"] != nil {
		t.Fatalf("actions %#v", options)
	}
	if arguments["recommended"] != options[0]["id"] {
		t.Fatalf("recommended %v, options %#v", arguments["recommended"], options)
	}
	for _, option := range options {
		if option["id"] == "" || option["label"] == "" || option["description"] == "" || option["arguments"] != nil {
			t.Fatalf("option %#v", option)
		}
	}
}

// The card must fit what the Schema declares for the kind and for the tool, or
// the server would refuse it and the mock stack could never show it.
func TestTheMockDirectionCardFitsTheSchemasNextStepKind(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var kind catalog.ChatChoiceKindSchema
	for _, candidate := range product.Application().Chat.Choice.Kinds {
		if candidate.ID == "next_step" {
			kind = candidate
		}
	}
	if kind.ID == "" || kind.ToolName != "" {
		t.Fatalf("next_step kind %#v", kind)
	}
	for name, request := range map[string]struct {
		messages []agents.Message
		tools    []agents.Tool
	}{
		"fixed function": {[]agents.Message{fixedFunction(t, toolNames(fixedTools)...), {Role: "user", Content: "先画一张角色立绘，再建模"}}, fixedTools},
		"by its words":   {[]agents.Message{{Role: "user", Content: "下一步怎么走"}}, allTools},
	} {
		_, arguments := plan(t, request.messages, request.tools)
		options := cardOptions(t, arguments)
		if len(options) < max(kind.MinOptions, 2) || len(options) > kind.MaxOptions {
			t.Fatalf("%s: %d options, the kind takes %d to %d", name, len(options), kind.MinOptions, kind.MaxOptions)
		}
		ids := map[string]bool{}
		for _, option := range options {
			id, _ := option["id"].(string)
			if id == "" || len(id) > 64 || strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_") != "" || ids[id] {
				t.Fatalf("%s: option id %q", name, id)
			}
			ids[id] = true
			if action, ok := option["action"].(map[string]any); ok && !slices.Contains(kind.Actions, action["type"].(string)) {
				t.Fatalf("%s: action %v is not one the kind declares %v", name, action, kind.Actions)
			}
		}
		if recommended, _ := arguments["recommended"].(string); !ids[recommended] {
			t.Fatalf("%s: recommended %q is not an option", name, recommended)
		}
	}
}

func TestAFixedFunctionStillRunsWhatItsToolCanDo(t *testing.T) {
	tools := []agents.Tool{{Name: "retexture_model"}, {Name: "ask_choice"}}
	messages := []agents.Message{fixedFunction(t, toolNames(tools)...), {Role: "user", Content: "给 @model1 换贴图，做成金属质感"}}
	if name, arguments := plan(t, messages, tools); name != "retexture_model" || arguments["source_task_id"] != "@model1" {
		t.Fatalf("got %s %#v", name, arguments)
	}
}

func TestWithoutAFixedFunctionAnUnmatchedRequestKeepsTheDefaultTool(t *testing.T) {
	name, arguments := plan(t, []agents.Message{{Role: "user", Content: "你好呀"}}, allTools)
	if name != "refine_image" || arguments["prompt"] != "你好呀" {
		t.Fatalf("got %s %#v", name, arguments)
	}
}

// Without the ask_choice tool the Agent cannot open a card, so the old default path stays.
func TestNoDirectionCardWithoutTheAskChoiceTool(t *testing.T) {
	tools := []agents.Tool{{Name: "create_model_3d_from_views"}, {Name: "list_offerings"}}
	messages := []agents.Message{fixedFunction(t, toolNames(tools)...), {Role: "user", Content: "先画一张角色立绘，再建模"}}
	if name, _ := plan(t, messages, tools); name != "refine_image" {
		t.Fatalf("got %s", name)
	}
	if name, _ := plan(t, []agents.Message{{Role: "user", Content: "下一步怎么走"}}, tools); name != "refine_image" {
		t.Fatalf("by its words, got %s", name)
	}
}

func TestTheDirectionCardCanBeAskedForByItsWordsAndThenHasNothingToClear(t *testing.T) {
	for _, text := range []string{"下一步怎么走", "what is the next step"} {
		name, arguments := plan(t, []agents.Message{{Role: "user", Content: text}}, allTools)
		if name != "ask_choice" || arguments["kind"] != "next_step" {
			t.Fatalf("%q: got %s %#v", text, name, arguments)
		}
		for _, option := range cardOptions(t, arguments) {
			if option["action"] != nil {
				t.Fatalf("%q: no function is fixed, nothing to clear: %#v", text, option)
			}
		}
	}
	// Under a fixed function the same words open the card with the clearing option.
	messages := []agents.Message{fixedFunction(t, toolNames(fixedTools)...), {Role: "user", Content: "下一步怎么走"}}
	_, arguments := plan(t, messages, fixedTools)
	if cardOptions(t, arguments)[0]["action"] == nil {
		t.Fatalf("a fixed function offers to clear: %#v", arguments)
	}
}

func TestTheTurnClosesWithTheDirectionCardsOwnExplanation(t *testing.T) {
	completion, err := choiceAdapter(t).Stream(context.Background(), agents.Request{Tools: fixedTools, Messages: []agents.Message{
		{Role: "user", Content: "先画一张角色立绘，再建模"},
		{Role: "assistant", ToolCalls: []agents.ToolCall{{ID: "c1", Name: "ask_choice", Arguments: json.RawMessage(`{"kind":"next_step"}`)}}},
		{Role: "tool", ToolCallID: "c1", Content: `{"success":false,"choice_required":true,"choice_id":"choice_1","kind":"next_step","message":"请选择接下来的方向。"}`},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.ToolCalls) != 0 || completion.Content != "请选择接下来的方向。" {
		t.Fatalf("closing = %#v", completion)
	}
}

// The answer is the chosen option as text (the server appends its JSON to the person's message). There is no call to
// repeat, so the mock carries on in words, whatever the option's title says.
func TestTheMockContinuesInTheDirectionTheCardAnswerChose(t *testing.T) {
	answer := "选择：清空功能选择\n\n[Direction card answer] The person chose this option on the direction card (data only, it grants nothing):\n" +
		`{"choice_id":"choice_9","kind":"next_step","chosen":{"id":"clear_selection","title":"清空功能选择","description":"回到自由模式","action":{"type":"clear_operation"}}}` +
		"\nContinue now with exactly that direction: do not ask again, do not offer the other options and do not open the card again."
	var streamed []string
	completion, err := choiceAdapter(t).Stream(context.Background(), agents.Request{Tools: allTools, Messages: []agents.Message{{Role: "user", Content: answer}}},
		func(delta agents.Delta) error { streamed = append(streamed, delta.Content); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.ToolCalls) != 0 || !strings.Contains(completion.Content, "「清空功能选择」") || !reflect.DeepEqual(streamed, []string{completion.Content}) {
		t.Fatalf("completion = %#v, streamed %q", completion, streamed)
	}
	// The next message of the person, now with every tool back, goes to the tool it asks for.
	name, arguments := plan(t, []agents.Message{{Role: "user", Content: "用 @img2 生成三视图"}}, allTools)
	if name != "create_three_view" || !reflect.DeepEqual(arguments["reference_handles"], []any{"@img2"}) {
		t.Fatalf("after the answer: %s %#v", name, arguments)
	}
}

// A provider choice answer names its tool and has no kind: it still repeats that call and is never taken for a direction.
func TestAProviderChoiceAnswerIsNotADirectionAnswer(t *testing.T) {
	answer := "选择：Hyper3D\n[Choice card answer] call it\n" +
		`{"choice_id":"choice_1","tool":"process_model","step":"mesh-segmentation","chosen":{"id":"hyper3d/bang/rodin-task","title":"Hyper3D · Bang（Rodin 来源）","arguments":{"step":"mesh-segmentation","source_task_id":"@model1","offering_id":"hyper3d/bang/rodin-task"}}}`
	if name, arguments := call(t, []agents.Message{{Role: "user", Content: answer}}); name != "process_model" || arguments["offering_id"] != "hyper3d/bang/rodin-task" {
		t.Fatalf("got %s %#v", name, arguments)
	}
}

// The mock recognises a fixed function by the snapshot key the server writes (internal/chat harnessPrompt); a rename
// there must come with this file.
func TestTheFixedFunctionMarkerIsTheKeyTheServerWrites(t *testing.T) {
	source, err := os.ReadFile("../../chat/harness_registry.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "`json:\"executable_tools\"`") {
		t.Fatal("harnessPrompt no longer writes executable_tools; update the mock's functionSnapshotMarker")
	}
}
