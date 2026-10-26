package generationtool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

const tripoModel = "tripo-v3.1-full"

func callOf(id, name, arguments string) agents.ToolCall {
	return agents.ToolCall{ID: id, Name: name, Arguments: []byte(arguments)}
}

// run3D asks for a text-to-3D model on the Tripo model with the given extra
// export formats chosen in the session settings, as the workspace would.
func (h *processingHarness) run3D(call string, formats []string, extraSettings map[string]json.RawMessage) chat.ToolResult {
	h.t.Helper()
	// These approval-lifecycle tests exercise the historical per-format Cost
	// revision with optional exports. Current Tripo H uses NativePricing and
	// requires all six formats; its quote and Grant are tested separately.
	h.gateway.parameterFixture = func(o generation.Offering) generation.Offering {
		if o.ID != "tripo/tripo-v3.1-full/text-to-3d" {
			return o
		}
		o.Revision = "2026-10-16.213.1"
		o.NativePricing = nil
		o.EstimatedCredits = 20
		o.Cost = &generation.CostDeclaration{ExtraCredits: 5, SelectionParameter: "output_formats", ConsentParameter: "authorize_extra_credits", ExtraLabel: "附加导出格式"}
		o.CostPolicy = &generation.CostPolicy{MaxAuthorizedExtraCredits: 30}
		o.Parameters = append([]generation.Parameter(nil), o.Parameters...)
		for i, p := range o.Parameters {
			if p.Name == "output_formats" {
				p.Fixed = false
				p.AdjustmentPolicy = ""
				p.AdjustmentReason = ""
				p.MinItems = 0
				p.Presentation = &generation.ParameterPresentation{Surface: generation.SurfaceProvider, Control: generation.ControlMultiSelect}
				o.Parameters[i] = p
			}
		}
		return o
	}
	h.gateway.grantGateway.offering = func(id generation.OfferingID) (generation.Offering, bool) {
		o, ok := h.gateway.product.Get(id)
		if !ok {
			return o, false
		}
		return h.gateway.parameterFixture(o), true
	}
	encoded, err := json.Marshal(formats)
	if err != nil {
		h.t.Fatal(err)
	}
	parameters := map[string]json.RawMessage{"output_formats": encoded}
	for name, value := range extraSettings {
		parameters[name] = value
	}
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call:    callOf(call, "create_model_3d", `{"prompt":"a simple wooden stool"}`),
		Tool:    h.tool("create_model_3d"),
		Options: chat.TurnOptions{AIModel: tripoModel, CreativeDomain: "model-3d", Parameters: parameters}}
	result, err := h.executor.Execute(context.Background(), command, nil)
	if err != nil {
		h.t.Fatalf("execute: %v", err)
	}
	return result
}

// The user's complaint: extra export formats were ticked, the Agent said the
// step was not submitted and told them to find a consent checkbox. Manual mode
// now ends at an approval card that carries the whole consent.
func TestExtraExportFormatsInManualModeMakeAnApprovalCard(t *testing.T) {
	h := newProcessingHarness(t, "manual", meshyCatalogTasks)
	result := h.run3D("call-1", []string{"FBX", "USDZ"}, nil)
	if result.Suspend || !result.NoSubmission || h.gateway.submitCount != 0 {
		t.Fatalf("manual extra formats must wait on a card, not submit: %+v submits=%d", result, h.gateway.submitCount)
	}
	if awaitedMessage(result.AgentContent) != h.app.Chat.Agent.ConsentWaitingContent {
		t.Fatalf("the Agent must be told the step waits on its card, got %q", result.AgentContent)
	}
	if result.Error != "" {
		t.Fatalf("no checkbox failure may reach the Agent: %q", result.Error)
	}
	prepared, err := h.gateway.repo.GetPreparedInput(context.Background(), "owner-a", "prepared-1")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ExtraCredits != 10 {
		t.Fatalf("two extra formats are 10 extra credits, frozen %d", prepared.ExtraCredits)
	}
	if h.frozenInput(0)["authorize_extra_credits"] != true {
		t.Fatalf("approving the card is the consent, so the frozen input carries it: %v", h.frozenInput(0))
	}
	op, err := h.gateway.repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "call-1")
	if err != nil || !op.Detached {
		t.Fatalf("the card must be detached from the turn: %+v %v", op, err)
	}
	grant, err := h.gateway.repo.GetExecutionGrant(context.Background(), "owner-a", op.GrantRequestID)
	if err != nil || grant.Status != executiongrant.Pending || grant.DecidedBy != "" {
		t.Fatalf("nobody approved yet: %+v %v", grant, err)
	}
}

// A model that is otherwise automatic in manual mode still asks when the step
// spends extra credits, and the policy cannot approve it behind the person's back.
func TestPolicyNeverApprovesExtraCreditsOfAnAgentStepInManualMode(t *testing.T) {
	h := newProcessingHarness(t, "manual", meshyCatalogTasks)
	// Format conversion is an automatic capability in manual mode.
	result, _ := h.run("convert_model_format", `{"source_task_id":"tripo-1","format":"FBX","texture_format":"PNG"}`)
	if !result.NoSubmission || h.gateway.submitCount != 0 {
		t.Fatalf("an automatic capability with extra credits was submitted: %+v", result)
	}
	// ApplyPolicy again, as any recovery path does: still pending.
	op, err := h.gateway.repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "call-1")
	if err != nil {
		t.Fatal(err)
	}
	grant, err := h.gateway.repo.GetExecutionGrant(context.Background(), "owner-a", op.GrantRequestID)
	if err != nil {
		t.Fatal(err)
	}
	service := &executiongrant.Service{Grants: h.gateway.repo, Prepared: h.gateway.repo,
		Policy: h.policy(), Modes: sessionMode{mode: "manual"}}
	after, err := service.ApplyPolicy(context.Background(), grant)
	if err != nil || after.Status != executiongrant.Pending {
		t.Fatalf("policy approved extra credits in manual mode: %+v %v", after, err)
	}
	// Auto mode approves the same frozen input.
	auto := &executiongrant.Service{Grants: h.gateway.repo, Prepared: h.gateway.repo,
		Policy: h.policy(), Modes: sessionMode{mode: "auto"}}
	approved, err := auto.ApplyPolicy(context.Background(), grant)
	if err != nil || approved.Status != executiongrant.Approved || !approved.ApprovedByPolicy() {
		t.Fatalf("auto mode must approve the extra credits itself: %+v %v", approved, err)
	}
}

// Nothing the session settings or the model say can stand in for the card: a
// ticked consent setting still ends at a card in manual mode.
func TestConsentSettingInTheSessionDoesNotSkipTheCard(t *testing.T) {
	h := newProcessingHarness(t, "manual", meshyCatalogTasks)
	result := h.run3D("call-1", []string{"FBX"}, map[string]json.RawMessage{"authorize_extra_credits": json.RawMessage(`true`)})
	if !result.NoSubmission || h.gateway.submitCount != 0 || awaitedMessage(result.AgentContent) != h.app.Chat.Agent.ConsentWaitingContent {
		t.Fatalf("a ticked setting skipped the approval card: %+v submits=%d", result, h.gateway.submitCount)
	}
	// And an unticked one is not a reason to fail the step: the card decides.
	h = newProcessingHarness(t, "manual", meshyCatalogTasks)
	result = h.run3D("call-1", []string{"FBX"}, map[string]json.RawMessage{"authorize_extra_credits": json.RawMessage(`false`)})
	if result.Error != "" || awaitedMessage(result.AgentContent) != h.app.Chat.Agent.ConsentWaitingContent {
		t.Fatalf("an unticked setting must not fail the step: %+v", result)
	}
	if h.frozenInput(0)["authorize_extra_credits"] != true {
		t.Fatalf("frozen input = %v", h.frozenInput(0))
	}
}

func TestExtraExportFormatsInAutoModeAreApprovedAndShownInTheCredits(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	result := h.run3D("call-1", []string{"FBX", "USDZ", "OBJ"}, nil)
	if result.Suspend || h.gateway.submitCount != 1 {
		t.Fatalf("auto mode must submit at once: %+v submits=%d", result, h.gateway.submitCount)
	}
	content := h.content(result)
	if content["approval"] != "policy" || content["extra_credits"] != float64(15) {
		t.Fatalf("auto mode result = %v", content)
	}
	offering, _ := h.gateway.grantGateway.offering("tripo/tripo-v3.1-full/text-to-3d")
	if content["estimated_credits"] != float64(offering.EstimatedCredits+15) {
		t.Fatalf("estimated credits %v must be the base %d plus 15 extra", content["estimated_credits"], offering.EstimatedCredits)
	}
	if h.frozenInput(0)["authorize_extra_credits"] != true {
		t.Fatalf("frozen input = %v", h.frozenInput(0))
	}
}

// The same prompt with and without extra export formats are two requests: one
// card each. Asking for the same formats again while the card waits does not
// make a second card.
func TestExtraCreditChoiceSeparatesCardsAndMergesTrueDuplicates(t *testing.T) {
	h := newProcessingHarness(t, "manual", meshyCatalogTasks)
	first := h.run3D("call-1", []string{"FBX", "USDZ"}, nil)
	if awaitedMessage(first.AgentContent) != h.app.Chat.Agent.ConsentWaitingContent {
		t.Fatalf("first card: %+v", first)
	}
	again := h.run3D("call-2", []string{"FBX", "USDZ"}, nil)
	if again.AgentContent != h.app.Chat.Agent.ConsentDuplicateContent || !again.NoSubmission {
		t.Fatalf("the same request must reuse the waiting card: %+v", again)
	}
	other := h.run3D("call-3", []string{"FBX"}, nil)
	if awaitedMessage(other.AgentContent) != h.app.Chat.Agent.ConsentWaitingContent {
		t.Fatalf("a different extra-credit choice is its own card: %+v", other)
	}
}

func TestApprovalRequestKeySeparatesTheExtraCreditChoice(t *testing.T) {
	base := generation.PreparedInput{Capability: generation.TextTo3D, OfferingID: "tripo/x", OfferingRevision: "r1",
		NormalizedInput: json.RawMessage(`{"prompt":"stool","output_formats":["FBX"],"authorize_extra_credits":true}`),
		ExtraCredits:    5}
	same := base
	if generationtool.ApprovalRequestKey("create_model_3d", base) != generationtool.ApprovalRequestKey("create_model_3d", same) {
		t.Fatal("one request must have one key")
	}
	// The choice alone is enough: identical input text, different extra credits.
	noExtra := base
	noExtra.ExtraCredits = 0
	if generationtool.ApprovalRequestKey("create_model_3d", base) == generationtool.ApprovalRequestKey("create_model_3d", noExtra) {
		t.Fatal("a request with extra credits and one without must be different cards")
	}
	moreFormats := base
	moreFormats.NormalizedInput = json.RawMessage(`{"prompt":"stool","output_formats":["FBX","USDZ"],"authorize_extra_credits":true}`)
	moreFormats.ExtraCredits = 10
	if generationtool.ApprovalRequestKey("create_model_3d", base) == generationtool.ApprovalRequestKey("create_model_3d", moreFormats) {
		t.Fatal("another set of extra formats must be another card")
	}
}
