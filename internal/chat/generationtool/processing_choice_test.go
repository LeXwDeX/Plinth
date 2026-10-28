package generationtool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// rodinTask is a finished Hyper3D Rodin text-to-3D result: a 1 MiB GLB that
// Tripo Smart Segmentation, both Bang paths (local model and Rodin source) and
// both the Meshy and Hyper3D retexture services accept.
func rodinTask(h *processingHarness) generation.Task {
	offering, _ := h.gateway.product.Get("hyper3d/rodin-gen-2.5/text-to-3d")
	task := modelTask("rodin-1", "owner-a", "session-a", offering.ProviderID, "text-to-3d", "glb")
	task.Offering = offering
	task.Assets[0].MediaType, task.Assets[0].Size = "model/gltf-binary", 1<<20
	return task
}

func choiceHarness(t *testing.T, mode string) (*processingHarness, *sqlite.Repository) {
	h := newProcessingHarness(t, mode, func(h *processingHarness) []generation.Task {
		return append(meshyCatalogTasks(h), rodinTask(h))
	})
	repo, err := sqlite.Open(h.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	h.config.Choices = repo
	h.executor, err = generationtool.New(h.config)
	if err != nil {
		t.Fatal(err)
	}
	return h, repo
}

func (h *processingHarness) cards(repo *sqlite.Repository) []chat.Choice {
	h.t.Helper()
	cards, err := repo.ListChoices(context.Background(), "owner-a", "session-a", 20)
	if err != nil {
		h.t.Fatal(err)
	}
	return cards
}

func TestTwoCompatibleServicesOpenAChoiceCardInsteadOfChoosing(t *testing.T) {
	h, repo := choiceHarness(t, "auto")
	result, _ := h.run("process_model", `{"step":"mesh-segmentation","source_task_id":"rodin-1"}`)
	if !result.NoSubmission || result.Suspend || len(h.gateway.frozen) != 0 || h.gateway.submitCount != 0 {
		t.Fatalf("a call with two compatible services ran one: %+v frozen=%d", result, len(h.gateway.frozen))
	}
	content := h.content(result)
	if content["choice_required"] != true || content["success"] != false || content["error"] != nil {
		t.Fatalf("result = %v", content)
	}
	options, _ := content["offering_choices"].([]any)
	// Schema order: Tripo (priority 100, credits), then both Bang paths (native quotes).
	if len(options) != 3 {
		t.Fatalf("offering_choices = %v", content["offering_choices"])
	}
	first, second := options[0].(map[string]any), options[1].(map[string]any)
	if first["offering_id"] != "tripo/geometry/smartsegment-model" || first["estimated_credits"] == nil ||
		second["offering_id"] != "hyper3d/bang/local-model" || second["provider"] == "" || second["native_quote"] == nil {
		t.Fatalf("options = %v", options)
	}
	if strings.Contains(result.AgentContent, "adapter") || strings.Contains(result.AgentContent, "UPSTREAM-SECRET") {
		t.Fatalf("the choice leaked private fields: %s", result.AgentContent)
	}
	cards := h.cards(repo)
	if len(cards) != 1 || cards[0].State != chat.ChoicePending || cards[0].ToolName != "process_model" ||
		cards[0].Capability != "mesh-segmentation" || cards[0].DefaultOptionID != "tripo/geometry/smartsegment-model" || !cards[0].AllowDefault {
		t.Fatalf("stored card = %+v", cards)
	}
	if content["choice_id"] != cards[0].ID {
		t.Fatalf("result names card %v, stored %s", content["choice_id"], cards[0].ID)
	}
	var next map[string]any
	if err := json.Unmarshal(cards[0].Options[2].Arguments, &next); err != nil ||
		next["offering_id"] != "hyper3d/bang/rodin-task" || next["step"] != "mesh-segmentation" || next["source_task_id"] != "rodin-1" {
		t.Fatalf("second option arguments = %s", cards[0].Options[1].Arguments)
	}
	var fallback map[string]any
	_ = json.Unmarshal(cards[0].DefaultArguments, &fallback)
	if fallback["use_default_offering"] != true || fallback["offering_id"] != nil {
		t.Fatalf("default arguments = %s", cards[0].DefaultArguments)
	}

	// The person picked the Rodin-source path (by typing it): the Agent passes
	// its offering_id, exactly that service runs and the card shows the answer.
	result, _ = h.run("process_model", `{"step":"mesh-segmentation","source_task_id":"rodin-1","offering_id":"hyper3d/bang/rodin-task"}`)
	if result.Error != "" || len(h.gateway.frozen) != 1 || h.gateway.frozen[0].OfferingID != "hyper3d/bang/rodin-task" {
		t.Fatalf("chosen service did not run: %+v %+v", result, h.gateway.frozen)
	}
	if input := h.frozenInput(0); input["offering_id"] != nil || input["source_task"] != "rodin-1" {
		t.Fatalf("frozen input = %v", input)
	}
	cards = h.cards(repo)
	if cards[0].State != chat.ChoiceAnswered || cards[0].AnswerOptionID != "hyper3d/bang/rodin-task" || cards[0].AnswerSource != chat.ChoiceAnsweredByAgent {
		t.Fatalf("card after the typed answer = %+v", cards[0])
	}
}

func TestDefaultAndUnknownServiceAndSingleCompatibleService(t *testing.T) {
	h, repo := choiceHarness(t, "auto")
	// "Any is fine": the first compatible Offering in Schema order runs.
	result, _ := h.run("process_model", `{"step":"mesh-segmentation","source_task_id":"rodin-1","use_default_offering":true}`)
	if result.Error != "" || len(h.gateway.frozen) != 1 || h.gateway.frozen[0].OfferingID != "tripo/geometry/smartsegment-model" {
		t.Fatalf("default did not run the first compatible service: %+v %+v", result, h.gateway.frozen)
	}
	if input := h.frozenInput(0); input["use_default_offering"] != nil {
		t.Fatalf("the default flag reached the provider input: %v", input)
	}
	// A service that does not accept the source is refused, never swapped.
	// #321: the text names it by provider and model with the reason; its ID
	// is only in the result's own field.
	result, _ = h.run("process_model", `{"step":"mesh-segmentation","source_task_id":"model-1","offering_id":"hyper3d/bang/rodin-task"}`)
	content := h.content(result)
	message, _ := content["error"].(string)
	requested, _ := content["requested_services"].([]any)
	if content["success"] != false || strings.Contains(message, "hyper3d/") || !strings.Contains(message, "Bang") ||
		len(requested) != 1 || requested[0].(map[string]any)["offering_id"] != "hyper3d/bang/rodin-task" || len(h.gateway.frozen) != 1 {
		t.Fatalf("incompatible service = %v", content)
	}
	// One compatible service runs directly, without a card.
	result, _ = h.run("process_model", `{"step":"rigging","source_task_id":"model-1"}`)
	if result.Error != "" || len(h.gateway.frozen) != 2 || len(h.cards(repo)) != 0 {
		t.Fatalf("a single compatible service did not run directly: %+v cards=%v", result, h.cards(repo))
	}
}

func TestManualModeAsksForTheServiceFirstAndTheApprovalAfterwards(t *testing.T) {
	h, repo := choiceHarness(t, "manual")
	result, _ := h.run("retexture_model", `{"source_task_id":"rodin-1","prompt":"mossy stone"}`)
	if !result.NoSubmission || result.Suspend || len(h.gateway.frozen) != 0 || h.content(result)["choice_required"] != true {
		t.Fatalf("manual retexture with two services did not ask first: %+v", result)
	}
	cards := h.cards(repo)
	if len(cards) != 1 || len(cards[0].Options) != 2 || !strings.HasPrefix(cards[0].Options[0].ID, "meshy/") || !strings.HasPrefix(cards[0].Options[1].ID, "hyper3d/") {
		t.Fatalf("retexture card = %+v", cards)
	}
	// After the answer the paid step still waits for the person's approval.
	turn := []chat.Message{{Role: "user", ImageURLs: []string{"/api/v1/input-assets/style"}}}
	result, _ = h.runAs("owner-a", "session-a", "retexture_model",
		`{"source_task_id":"rodin-1","prompt":"mossy stone","offering_id":"hyper3d/texture/local-model"}`, turn)
	if !result.NoSubmission || h.gateway.submitCount != 0 || len(h.gateway.frozen) != 1 ||
		h.gateway.frozen[0].OfferingID != "hyper3d/texture/local-model" || strings.Contains(result.AgentContent, "choice_required") ||
		!strings.Contains(result.AgentContent, "approval card") {
		t.Fatalf("the chosen retexture did not wait for approval: %+v frozen=%+v", result, h.gateway.frozen)
	}
}

func TestPlanToolAsksForTheAmbiguousStep(t *testing.T) {
	h := newAgentPlanHarness(t, "auto", false)
	h.gateway.tasks["rodin-1"] = rodinTask(h.processingHarness)
	repo, err := sqlite.Open(h.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	h.executor.SetChoices(repo)
	arguments := `{"source_task_id":"rodin-1","steps":[{"capability":"mesh-segmentation","parameters":{}}]}`
	result, err := h.executor.Execute(context.Background(), h.command(arguments), nil)
	if err != nil {
		t.Fatal(err)
	}
	if content := h.content(result); content["choice_required"] != true || content["plan_id"] != nil {
		t.Fatalf("plan with an ambiguous step = %v", content)
	}
	cards, _ := repo.ListChoices(context.Background(), "owner-a", "session-a", 5)
	if len(cards) != 1 || cards[0].StepIndex != 0 || cards[0].ToolName != "plan_model_processing" {
		t.Fatalf("plan card = %+v", cards)
	}
	var next struct {
		Steps []struct {
			OfferingID string `json:"offering_id"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(cards[0].Options[2].Arguments, &next); err != nil || len(next.Steps) != 1 || next.Steps[0].OfferingID != "hyper3d/bang/rodin-task" {
		t.Fatalf("plan option arguments = %s", cards[0].Options[2].Arguments)
	}
	_, plan := h.propose(string(cards[0].Options[2].Arguments))
	if len(plan.Steps) != 1 || plan.Steps[0].OfferingID != "hyper3d/bang/rodin-task" {
		t.Fatalf("plan = %+v", plan.Steps)
	}
	if cards, _ := repo.ListChoices(context.Background(), "owner-a", "session-a", 5); cards[0].State != chat.ChoiceAnswered {
		t.Fatalf("plan card after the answer = %+v", cards[0])
	}
}

// Hyper3D texture requires its reference image. The one image of the turn is
// bound without the call naming it; none or several are refused before
// anything is frozen, with the Schema text that tells the Agent to ask (#92).
func TestRequiredReferenceImageComesFromTheTurnOrIsAskedFor(t *testing.T) {
	const call = `{"source_task_id":"rodin-1","offering_id":"hyper3d/texture/local-model"}`
	image := func(names ...string) []chat.Message {
		return []chat.Message{{Role: "user", ImageURLs: names}}
	}
	h, _ := choiceHarness(t, "auto")
	result, _ := h.runAs("owner-a", "session-a", "retexture_model", call, image("/api/v1/input-assets/style"))
	if result.Error != "" || h.gateway.submitCount != 1 || h.frozenInput(0)["reference_image"] != "configured-input-asset" {
		t.Fatalf("the turn's image was not bound as the required reference: %+v input=%v", result, h.gateway.frozen)
	}
	for name, history := range map[string][]chat.Message{
		"none":    nil,
		"several": image("/api/v1/input-assets/one", "/api/v1/input-assets/two"),
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := choiceHarness(t, "auto")
			result, _ := h.runAs("owner-a", "session-a", "retexture_model", call, history)
			h.expectRejected(result, "Ask the person to attach exactly one image")
		})
	}
}
