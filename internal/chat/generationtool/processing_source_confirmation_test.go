package generationtool_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #321 D (notes 98949, 98950): the person's current selection is used
// silently. When the Agent switches to another model for a paid step, the
// tool asks first; it does not let the Agent pay for the other model on its
// own, whatever the prompt says.

const againArguments = `{"step":"mesh-segmentation","source_task_id":"@model1","offering_id":"hyper3d/bang/rodin-task"}`

// bangResult is the previous Bang result: the selected model, which no
// segmentation service accepts (its only saved file is an FBX).
func bangResult(h *processingHarness) generation.Task {
	offering, _ := h.gateway.product.Get("hyper3d/bang/rodin-task")
	task := modelTask("bang-1", "owner-a", "session-a", offering.ProviderID, "mesh-segmentation", "fbx")
	task.Offering = offering
	return task
}

func againHarness(t *testing.T) *processingHarness {
	return newProcessingHarness(t, "auto", func(h *processingHarness) []generation.Task {
		return []generation.Task{rodinTask(h), bangResult(h)}
	})
}

func againHistory(userText string) []chat.Message {
	return []chat.Message{
		{Role: "assistant", Attachments: []chat.Attachment{
			{Handle: "@model1", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "rodin-1"}}},
		{Role: "assistant", Attachments: []chat.Attachment{
			{Handle: "@model2", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "bang-1"}}},
		{Role: "user", Content: userText},
	}
}

var bangSelected = chat.TurnOptions{Selection: []chat.Attachment{
	{Handle: "@model2", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "bang-1"}}}

func TestAgentAsksBeforePayingForAnotherModelThanTheSelectedOne(t *testing.T) {
	h := againHarness(t)
	result, _ := h.runWith("owner-a", "session-a", "process_model", againArguments, againHistory("再把这个模型分割成部件一次"), bangSelected)
	if !result.NoSubmission || len(h.gateway.frozen) != 0 || h.gateway.submitCount != 0 {
		t.Fatalf("the Agent paid for another model without asking: %+v frozen=%d", result, len(h.gateway.frozen))
	}
	content := h.content(result)
	message, _ := content["error"].(string)
	if content["source_confirmation_required"] != true || content["success"] != false ||
		!strings.Contains(message, "@model2") || !strings.Contains(message, "@model1") {
		t.Fatalf("result = %v", content)
	}
	if strings.Contains(message, "hyper3d/") || strings.Contains(result.AgentContent, "UPSTREAM-SECRET") {
		t.Fatalf("the question leaks internal IDs: %s", result.AgentContent)
	}
}

func TestAgentRunsTheOtherModelAfterThePersonAnswered(t *testing.T) {
	h := againHarness(t)
	history := againHistory("再把这个模型分割成部件一次")
	history = append(history,
		chat.Message{Role: "assistant", Content: "当前选中的 @model2 不能再分割，要改用 @model1 吗？",
			ToolCalls: []chat.ToolCall{{ID: "earlier", Name: "process_model", Arguments: []byte(againArguments)}}},
		chat.Message{Role: "user", Content: "好的"})
	result, _ := h.runWith("owner-a", "session-a", "process_model", againArguments, history, bangSelected)
	if result.NoSubmission || len(h.gateway.frozen) != 1 || h.gateway.frozen[0].OfferingID != "hyper3d/bang/rodin-task" ||
		h.frozenInput(0)["source_task"] != "rodin-1" {
		t.Fatalf("the confirmed model did not run: %s", result.AgentContent)
	}
}

func TestAModelThePersonNamedNeedsNoQuestion(t *testing.T) {
	h := againHarness(t)
	result, _ := h.runWith("owner-a", "session-a", "process_model", againArguments, againHistory("把 @model1 再分割一次"), bangSelected)
	if result.NoSubmission || len(h.gateway.frozen) != 1 {
		t.Fatalf("a model the person named was questioned: %s", result.AgentContent)
	}
	// Without a selection there is nothing to switch away from.
	h = againHarness(t)
	result, _ = h.runWith("owner-a", "session-a", "process_model", againArguments, againHistory("再分割一次"), chat.TurnOptions{})
	if result.NoSubmission || len(h.gateway.frozen) != 1 {
		t.Fatalf("no selection, still questioned: %s", result.AgentContent)
	}
	// The selected model itself runs silently.
	h = againHarness(t)
	selected := chat.TurnOptions{Selection: []chat.Attachment{
		{Handle: "@model1", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "rodin-1"}}}
	result, _ = h.runWith("owner-a", "session-a", "process_model", againArguments, againHistory("再分割一次"), selected)
	if result.NoSubmission || len(h.gateway.frozen) != 1 {
		t.Fatalf("the selected model was questioned: %s", result.AgentContent)
	}
}

// The plan tool follows the same rule: a plan on another model than the
// selected one waits for the person.
func TestPlanOnAnotherModelThanTheSelectedOneAsksFirst(t *testing.T) {
	h := newAgentPlanHarness(t, "auto", false)
	h.gateway.tasks["rodin-1"] = rodinTask(h.processingHarness)
	h.gateway.tasks["bang-1"] = bangResult(h.processingHarness)
	command := h.command(`{"source_task_id":"@model1","steps":[{"capability":"mesh-segmentation","offering_id":"hyper3d/bang/rodin-task","parameters":{}}]}`)
	command.History, command.Options = againHistory("再分割一次"), bangSelected
	result, err := h.executor.Execute(t.Context(), command, nil)
	if err != nil {
		t.Fatal(err)
	}
	if content := h.content(result); content["source_confirmation_required"] != true || content["plan_id"] != nil {
		t.Fatalf("plan on another model = %v", content)
	}
}
