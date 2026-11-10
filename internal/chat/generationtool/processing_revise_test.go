package generationtool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func reviseCommand(arguments string) chat.ToolCommand {
	return chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-revise",
		Call: agents.ToolCall{ID: "revise-call", Name: "revise_processing_plan", Arguments: []byte(arguments)}}
}

func TestReviseProcessingPlanSkipsAnUnstartedStep(t *testing.T) {
	h := newAgentPlanHarness(t, "auto", false)
	_, plan := h.propose(agentPlanArguments)
	arguments := `{"plan_id":"` + plan.ID + `","expected_plan_version":` +
		jsonNumber(plan.Version) + `,"operation":"skip","step_index":2,"reason":"用户不需要动画"}`
	result, err := h.executor.ReviseProcessingPlan(context.Background(), reviseCommand(arguments))
	if err != nil {
		t.Fatal(err)
	}
	var content map[string]any
	if json.Unmarshal([]byte(result.AgentContent), &content) != nil {
		t.Fatalf("result = %s", result.AgentContent)
	}
	if content["success"] != true || content["message"] == "" {
		t.Fatalf("revision result = %v", content)
	}
	revised := h.get(plan.ID)
	if revised.Steps[2].State != "SKIPPED" || revised.Steps[2].RevisionOperation != "skip" ||
		revised.Steps[2].RevisionReason != "用户不需要动画" {
		t.Fatalf("revised step = %+v", revised.Steps[2])
	}
}

func TestReviseProcessingPlanRefusesStaleAndUnsupportedRequests(t *testing.T) {
	h := newAgentPlanHarness(t, "auto", false)
	_, plan := h.propose(agentPlanArguments)
	cases := map[string]string{
		"unknown plan":  `{"plan_id":"missing","expected_plan_version":1,"operation":"skip","step_index":0,"reason":"x"}`,
		"stale version": `{"plan_id":"` + plan.ID + `","expected_plan_version":99,"operation":"skip","step_index":2,"reason":"x"}`,
		"insert without an approved contingency": `{"plan_id":"` + plan.ID + `","expected_plan_version":` +
			jsonNumber(plan.Version) + `,"operation":"insert","step_index":0,"contingency_id":"pivot","parameters":{},"reason":"x"}`,
		"reason required": `{"plan_id":"` + plan.ID + `","expected_plan_version":` +
			jsonNumber(plan.Version) + `,"operation":"skip","step_index":2,"reason":" "}`,
	}
	for name, arguments := range cases {
		result, err := h.executor.ReviseProcessingPlan(context.Background(), reviseCommand(arguments))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if result.Error == "" {
			t.Fatalf("%s accepted: %+v", name, result)
		}
	}
}

func TestReviseProcessingPlanRefusesAnUnstartedStepWithFrozenInput(t *testing.T) {
	h := newAgentPlanHarness(t, "auto", false)
	_, plan := h.propose(agentPlanArguments)
	// Simulate a prepared step: replace/skip may not move it.
	stored, err := h.processingHarness.gateway.repo.GetProcessingPlan(context.Background(), "owner-a", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Steps[2].PreparedInputID = "prepared-1"
	stored.Steps[2].AuthorizationRef = "grant-1"
	if err := h.processingHarness.gateway.repo.SaveProcessingPlan(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	plan.Version = stored.Version
	arguments := `{"plan_id":"` + plan.ID + `","expected_plan_version":` +
		jsonNumber(plan.Version) + `,"operation":"skip","step_index":2,"reason":"x"}`
	result, _ := h.executor.ReviseProcessingPlan(context.Background(), reviseCommand(arguments))
	if !strings.Contains(result.AgentContent, `"success":false`) {
		t.Fatalf("skip with frozen input = %+v", result)
	}
}

func jsonNumber(value int64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
