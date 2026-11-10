package generationtool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func maskOffering(id, model string, mask bool, defaultOffering bool) generation.Offering {
	parameters := []generation.Parameter{
		{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true},
		{Name: "reference", Type: "asset-array", Semantic: generation.SemanticReferenceImage},
	}
	if mask {
		parameters = append(parameters, generation.Parameter{Name: "mask", Type: "asset", Semantic: generation.SemanticMask})
	}
	return generation.Offering{
		ID: generation.OfferingID(id), ModelID: model, ModelName: model,
		Capability: generation.ImageToImage, Enabled: true, Default: defaultOffering,
		PollInterval: time.Second, Timeout: 5 * time.Second,
		Parameters: parameters,
	}
}

// maskRig prepares the default refine_image tool over Offerings of the image
// workspace, with one history picture so the call is an image edit.
func maskRig(t *testing.T, offerings []generation.Offering, completed generation.Offering) (*generationtool.Executor, catalog.ChatToolSchema, *fakeGateway) {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	tool := application.Chat.Tools[0]
	if tool.Name != "refine_image" || tool.SelectionMask != "bind" {
		t.Fatalf("refine_image mask declaration = %q/%q", tool.Name, tool.SelectionMask)
	}
	gateway := &fakeGateway{
		offerings: offerings,
		tasks: []generation.Task{
			{ID: "configured-task", OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskRunning},
			{ID: "configured-task", OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskSucceeded,
				Offering: completed, Assets: []generation.Asset{{ID: "configured-asset", Role: "image"}}},
			{ID: "configured-task", OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskSucceeded,
				Offering: completed, Assets: []generation.Asset{{ID: "configured-asset", Role: "image"}}},
		},
	}
	now := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: &fakeReferences{}, Chat: application.Chat,
		TaskStates: application.TaskStates,
		Clock:      func() time.Time { return now },
		Sleep: func(_ context.Context, duration time.Duration) error {
			now = now.Add(duration)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor, tool, gateway
}

func maskCommand(tool catalog.ChatToolSchema, options chat.TurnOptions, arguments string) chat.ToolCommand {
	return chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a",
		History: []chat.Message{{Role: "user", ImageURLs: []string{"upload-url"}}},
		Call:    agents.ToolCall{ID: "call-mask", Name: tool.Name, Arguments: []byte(arguments)},
		Tool:    tool, Options: options,
	}
}

// finish consumes the accepted task's wait so the final result (with its
// notes) is available, as the orchestrator would do on the next round.
func finish(t *testing.T, executor *generationtool.Executor, command chat.ToolCommand, result chat.ToolResult) chat.ToolResult {
	t.Helper()
	if !result.AwaitTask {
		return result
	}
	command.BoundTaskID = result.TaskID
	command.TaskWaitDeadline = result.WaitUntil
	next, err := executor.Execute(context.Background(), command, nil)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func submittedInput(t *testing.T, gateway *fakeGateway) map[string]any {
	t.Helper()
	var input map[string]any
	if err := json.Unmarshal(gateway.submitted.Input, &input); err != nil {
		t.Fatal(err)
	}
	return input
}

func resultObject(t *testing.T, content string) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// #244: the frozen mask is written into the Offering's mask parameter by the
// Schema's binding, not guessed by the Agent.
func TestFrozenSelectionMaskIsBoundIntoTheOfferingMask(t *testing.T) {
	executor, tool, gateway := maskRig(t, []generation.Offering{maskOffering("vendor/mask", "mask-model", true, true)},
		maskOffering("vendor/mask", "mask-model", true, true))
	options := chat.TurnOptions{
		AIModel: "mask-model", ReferenceImageIndices: []int{1},
		SelectionMask: &chat.SelectionMask{MaskAssetID: "frozen-mask-asset", Width: 64, Height: 64,
			BBox: [4]int{1, 1, 30, 30}, Coverage: 0.2},
	}
	result, err := executor.Execute(context.Background(), maskCommand(tool, options, `{"prompt":"fill the area"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if gateway.submitted.OfferingID != "vendor/mask" {
		t.Fatalf("offering = %q", gateway.submitted.OfferingID)
	}
	if submittedInput(t, gateway)["mask"] != "frozen-mask-asset" {
		t.Fatalf("mask input = %#v", submittedInput(t, gateway))
	}
	if result.TaskID != "configured-task" {
		t.Fatalf("result = %+v", result)
	}
}

// #244: an Agent-supplied mask that the Offering accepts wins over the frozen
// one.
func TestAgentMaskArgumentWinsOverTheFrozenMask(t *testing.T) {
	executor, tool, gateway := maskRig(t, []generation.Offering{maskOffering("vendor/mask", "mask-model", true, true)},
		maskOffering("vendor/mask", "mask-model", true, true))
	options := chat.TurnOptions{
		AIModel: "mask-model", ReferenceImageIndices: []int{1},
		SelectionMask: &chat.SelectionMask{MaskAssetID: "frozen-mask-asset", Width: 64, Height: 64,
			BBox: [4]int{0, 0, 1, 1}, Coverage: 0.1},
	}
	if _, err := executor.Execute(context.Background(),
		maskCommand(tool, options, `{"prompt":"fill","mask":"/configured/agent-mask.png"}`), nil); err != nil {
		t.Fatal(err)
	}
	if submittedInput(t, gateway)["mask"] != "configured-input-asset" {
		t.Fatalf("mask input = %#v", submittedInput(t, gateway))
	}
}

// #244: a tool that ignores the selection mask never receives it.
func TestMaskIgnoringToolReceivesNoFrozenMask(t *testing.T) {
	executor, tool, gateway := maskRig(t, []generation.Offering{maskOffering("vendor/mask", "mask-model", true, true)},
		maskOffering("vendor/mask", "mask-model", true, true))
	tool.SelectionMask = "ignore"
	options := chat.TurnOptions{
		AIModel: "mask-model", ReferenceImageIndices: []int{1},
		SelectionMask: &chat.SelectionMask{MaskAssetID: "frozen-mask-asset", Width: 64, Height: 64,
			BBox: [4]int{0, 0, 1, 1}, Coverage: 0.1},
	}
	if _, err := executor.Execute(context.Background(), maskCommand(tool, options, `{"prompt":"fill"}`), nil); err != nil {
		t.Fatal(err)
	}
	if _, exists := submittedInput(t, gateway)["mask"]; exists {
		t.Fatalf("ignore tool received a mask: %#v", submittedInput(t, gateway))
	}
}

// #244: the user picked a model without a mask parameter. Nothing is
// submitted: the refusal names the model and the mask-capable choices, so the
// selection never silently becomes a whole-image redraw.
func TestFrozenMaskRefusesAUserChosenModelWithoutMask(t *testing.T) {
	executor, tool, gateway := maskRig(t, []generation.Offering{
		maskOffering("vendor/plain", "plain-model", false, true),
		maskOffering("vendor/mask", "mask-model", true, false),
	}, maskOffering("vendor/plain", "plain-model", false, true))
	options := chat.TurnOptions{
		AIModel: "plain-model", ReferenceImageIndices: []int{1},
		SelectionMask: &chat.SelectionMask{MaskAssetID: "frozen-mask-asset", Width: 64, Height: 64,
			BBox: [4]int{0, 0, 1, 1}, Coverage: 0.1},
	}
	result, err := executor.Execute(context.Background(), maskCommand(tool, options, `{"prompt":"fill"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoSubmission || gateway.submitted.OfferingID != "" {
		t.Fatalf("submitted despite no mask support: %+v %#v", result, gateway.submitted)
	}
	for _, needle := range []string{"plain-model", "mask-model", "不会退化成整图重画"} {
		if !strings.Contains(result.Error, needle) {
			t.Fatalf("refusal %q lacks %q", result.Error, needle)
		}
	}
}

// #244: an Agent-named model without mask support is refused the same way.
func TestFrozenMaskRefusesAnAgentNamedModelWithoutMask(t *testing.T) {
	executor, tool, gateway := maskRig(t, []generation.Offering{
		maskOffering("vendor/plain", "plain-model", false, false),
		maskOffering("vendor/mask", "mask-model", true, false),
	}, maskOffering("vendor/plain", "plain-model", false, false))
	options := chat.TurnOptions{
		ReferenceImageIndices: []int{1},
		SelectionMask: &chat.SelectionMask{MaskAssetID: "frozen-mask-asset", Width: 64, Height: 64,
			BBox: [4]int{0, 0, 1, 1}, Coverage: 0.1},
	}
	result, err := executor.Execute(context.Background(),
		maskCommand(tool, options, `{"prompt":"fill","model":"plain-model"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoSubmission || gateway.submitted.OfferingID != "" {
		t.Fatalf("submitted despite no mask support: %+v %#v", result, gateway.submitted)
	}
}

// #244: with no user model, the step moves to a mask-capable Offering and the
// result says which model ran instead.
func TestFrozenMaskSwitchesToAMaskCapableOfferingWhenNoUserModel(t *testing.T) {
	executor, tool, gateway := maskRig(t, []generation.Offering{
		maskOffering("vendor/plain", "plain-model", false, true),
		maskOffering("vendor/mask", "mask-model", true, false),
	}, maskOffering("vendor/mask", "mask-model", true, false))
	options := chat.TurnOptions{
		ReferenceImageIndices: []int{1},
		SelectionMask: &chat.SelectionMask{MaskAssetID: "frozen-mask-asset", Width: 64, Height: 64,
			BBox: [4]int{0, 0, 1, 1}, Coverage: 0.1},
	}
	command := maskCommand(tool, options, `{"prompt":"fill"}`)
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gateway.submitted.OfferingID != "vendor/mask" || submittedInput(t, gateway)["mask"] != "frozen-mask-asset" {
		t.Fatalf("offering/mask = %q %#v", gateway.submitted.OfferingID, submittedInput(t, gateway))
	}
	result = finish(t, executor, command, result)
	payload := resultObject(t, result.AgentContent)
	note, _ := payload[tool.ResultFields.ModelNote].(string)
	for _, needle := range []string{"plain-model", "mask-model"} {
		if !strings.Contains(note, needle) {
			t.Fatalf("model note %q lacks %q", note, needle)
		}
	}
}

// #244: no mask-capable Offering at all submits nothing.
func TestFrozenMaskWithNoMaskCapableOfferingSubmitsNothing(t *testing.T) {
	executor, tool, gateway := maskRig(t, []generation.Offering{maskOffering("vendor/plain", "plain-model", false, true)},
		maskOffering("vendor/plain", "plain-model", false, true))
	options := chat.TurnOptions{
		ReferenceImageIndices: []int{1},
		SelectionMask: &chat.SelectionMask{MaskAssetID: "frozen-mask-asset", Width: 64, Height: 64,
			BBox: [4]int{0, 0, 1, 1}, Coverage: 0.1},
	}
	result, err := executor.Execute(context.Background(), maskCommand(tool, options, `{"prompt":"fill"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoSubmission || gateway.submitted.OfferingID != "" || result.Error == "" {
		t.Fatalf("result=%+v submitted=%#v", result, gateway.submitted)
	}
}

// #244 + R1-7: an Agent mask with no frozen mask is still dropped, and the
// result explains that it was not applied.
func TestAgentMaskWithoutFrozenMaskIsDroppedWithANote(t *testing.T) {
	executor, tool, gateway := maskRig(t, []generation.Offering{maskOffering("vendor/plain", "plain-model", false, true)},
		maskOffering("vendor/plain", "plain-model", false, true))
	options := chat.TurnOptions{AIModel: "plain-model", ReferenceImageIndices: []int{1}}
	command := maskCommand(tool, options, `{"prompt":"fill","mask":"/configured/agent-mask.png"}`)
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := submittedInput(t, gateway)["mask"]; exists {
		t.Fatalf("undeclared mask was sent: %#v", submittedInput(t, gateway))
	}
	result = finish(t, executor, command, result)
	payload := resultObject(t, result.AgentContent)
	note, _ := payload[tool.ResultFields.MaskNote].(string)
	if note == "" || !strings.Contains(note, "没有用户圈选") {
		t.Fatalf("mask note = %q", note)
	}
}
