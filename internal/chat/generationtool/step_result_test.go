package generationtool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

func unlimitedStepSetting() approvalpolicy.StepSetting {
	// The stored cap stays in the Schema's range even though the unlimited
	// mode never applies it; the settings store validates the pair.
	return approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeUnlimited, Cap: 20}
}

// stepObjectOf reads the steps object of a tool result under the key the
// shipped Catalog's result fields declare. A result without the key fails.
func stepObjectOf(t *testing.T, command chat.ToolCommand, result chat.ToolResult) map[string]any {
	t.Helper()
	var content map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatalf("tool result %q: %v", result.AgentContent, err)
	}
	key := command.Tool.ResultFields.Steps
	if key == "" {
		t.Fatal("the shipped Catalog declares no steps result key for the tool")
	}
	raw, exists := content[key]
	if !exists {
		t.Fatalf("tool result carries no %q object: %s", key, result.AgentContent)
	}
	var steps map[string]any
	if err := json.Unmarshal(raw, &steps); err != nil {
		t.Fatalf("steps object %q: %v", raw, err)
	}
	return steps
}

// hasStepKey reports whether a result carries the steps object at all.
func hasStepKey(t *testing.T, command chat.ToolCommand, result chat.ToolResult) bool {
	t.Helper()
	var content map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatalf("tool result %q: %v", result.AgentContent, err)
	}
	_, exists := content[command.Tool.ResultFields.Steps]
	return exists
}

func assertStepNumber(t *testing.T, steps map[string]any, key string, want int) {
	t.Helper()
	value, exists := steps[key]
	if !exists {
		t.Fatalf("steps object lacks %q: %v", key, steps)
	}
	if number, ok := value.(float64); !ok || int(number) != want {
		t.Fatalf("steps[%q] = %v, want %d", key, value, want)
	}
}

// #330 step 3B: a successful 3D step under the cap mode carries the asset's
// standing, the cap and the steps left beside the budget object.
func TestGenerationToolResultCarriesTheCapStepStanding(t *testing.T) {
	ctx := context.Background()
	executor, command, _, repo, _ := stepHarness(t, "create_model_3d_from_image", "image-to-3d", nil)
	saveStepSettings(t, repo, capSettingOf(5), capSettingOf(5))

	result, err := executor.Execute(ctx, command, nil)
	if err != nil || result.NoSubmission {
		t.Fatalf("the step inside the cap: %+v err=%v", result, err)
	}
	steps := stepObjectOf(t, command, result)
	if steps[catalog.StepResultMode] != "cap" {
		t.Fatalf("mode = %v", steps[catalog.StepResultMode])
	}
	assertStepNumber(t, steps, catalog.StepResultUsed, 1)
	assertStepNumber(t, steps, catalog.StepResultCap, 5)
	assertStepNumber(t, steps, catalog.StepResultRemaining, 4)
}

// A refused step is still a generation tool result, so its failure carries the
// standing the refusal was decided on.
func TestGenerationToolStepCapRefusalCarriesTheStepStanding(t *testing.T) {
	ctx := context.Background()
	executor, command, _, repo, _ := stepHarness(t, "create_model_3d_from_image", "image-to-3d", nil)
	saveStepSettings(t, repo, capSettingOf(1), capSettingOf(1))
	if first, err := executor.Execute(ctx, command, nil); err != nil || first.NoSubmission {
		t.Fatalf("the step inside the cap: %+v err=%v", first, err)
	}

	refused, err := executor.Execute(ctx, nextCall(command, "call-b"), nil)
	if err != nil || !refused.NoSubmission {
		t.Fatalf("the step over the cap: %+v err=%v", refused, err)
	}
	steps := stepObjectOf(t, command, refused)
	if steps[catalog.StepResultMode] != "cap" {
		t.Fatalf("mode = %v", steps[catalog.StepResultMode])
	}
	assertStepNumber(t, steps, catalog.StepResultUsed, 1)
	assertStepNumber(t, steps, catalog.StepResultCap, 1)
	assertStepNumber(t, steps, catalog.StepResultRemaining, 0)
}

// The agent and unlimited modes report the used steps and no limit: naming a
// cap there would suggest one the platform never applies.
func TestGenerationToolStepStandingFollowsTheMode(t *testing.T) {
	ctx := context.Background()
	executor, command, _, repo, _ := stepHarness(t, "create_model_3d_from_image", "image-to-3d", nil)

	agentResult, err := executor.Execute(ctx, command, nil)
	if err != nil || agentResult.NoSubmission {
		t.Fatalf("agent mode step: %+v err=%v", agentResult, err)
	}
	agentSteps := stepObjectOf(t, command, agentResult)
	if agentSteps[catalog.StepResultMode] != "agent" {
		t.Fatalf("agent mode = %v", agentSteps[catalog.StepResultMode])
	}
	assertStepNumber(t, agentSteps, catalog.StepResultUsed, 1)
	for _, key := range []string{catalog.StepResultCap, catalog.StepResultRemaining} {
		if _, exists := agentSteps[key]; exists {
			t.Fatalf("agent mode reported %q: %v", key, agentSteps)
		}
	}

	saveStepSettings(t, repo, unlimitedStepSetting(), unlimitedStepSetting())
	unlimitedResult, err := executor.Execute(ctx, nextCall(command, "call-b"), nil)
	if err != nil || unlimitedResult.NoSubmission {
		t.Fatalf("unlimited mode step: %+v err=%v", unlimitedResult, err)
	}
	unlimitedSteps := stepObjectOf(t, command, unlimitedResult)
	if unlimitedSteps[catalog.StepResultMode] != "unlimited" {
		t.Fatalf("unlimited mode = %v", unlimitedSteps[catalog.StepResultMode])
	}
	assertStepNumber(t, unlimitedSteps, catalog.StepResultUsed, 2)
	for _, key := range []string{catalog.StepResultCap, catalog.StepResultRemaining} {
		if _, exists := unlimitedSteps[key]; exists {
			t.Fatalf("unlimited mode reported %q: %v", key, unlimitedSteps)
		}
	}
}

// A result without step context carries no steps object at all: a 2D step is
// never counted, and a session another approval mode governs has no 3D step
// setting to report.
func TestGenerationToolResultWithoutStepContextHasNoSteps(t *testing.T) {
	t.Run("2D step", func(t *testing.T) {
		executor, command, _, _, _ := stepHarness(t, "refine_image", "text-to-image", nil)
		result, err := executor.Execute(context.Background(), command, nil)
		if err != nil || result.NoSubmission {
			t.Fatalf("2D step: %+v err=%v", result, err)
		}
		if hasStepKey(t, command, result) {
			t.Fatalf("a 2D step carried a steps object: %s", result.AgentContent)
		}
	})
	t.Run("session outside the steps mode", func(t *testing.T) {
		executor, command, _, _, service := stepHarness(t, "create_model_3d_from_image", "image-to-3d", nil)
		service.Modes = sessionMode{mode: "auto"}
		result, err := executor.Execute(context.Background(), command, nil)
		if err != nil || result.NoSubmission {
			t.Fatalf("auto mode step: %+v err=%v", result, err)
		}
		if hasStepKey(t, command, result) {
			t.Fatalf("an auto-mode session carried a steps object: %s", result.AgentContent)
		}
	})
}

// stepsPolicyOf adds the shipped step policy to a harness policy, so a test
// session the steps mode governs can report a standing.
func stepsPolicyOf(t *testing.T, policy approvalpolicy.Policy, application catalog.ApplicationSchema) approvalpolicy.Policy {
	t.Helper()
	if application.StepPolicy == nil {
		t.Fatal("the default Catalog declares no step policy")
	}
	counted := make([]string, 0, len(application.StepPolicy.CountedCapabilities))
	for _, capability := range application.StepPolicy.CountedCapabilities {
		counted = append(counted, string(capability))
	}
	return policy.WithSteps(approvalpolicy.StepsPolicy{
		Counted:     counted,
		DefaultList: approvalpolicy.StepSetting{Mode: application.StepPolicy.DefaultMode.List, Cap: application.StepPolicy.Cap.Default},
		DefaultMCP:  approvalpolicy.StepSetting{Mode: application.StepPolicy.DefaultMode.MCP, Cap: application.StepPolicy.Cap.Default},
	})
}

// useStepsGrants rebuilds the plan harness with the shipped step policy and a
// session the steps mode governs, so a tool that declares the key has a
// standing attachStepStanding could otherwise add.
func (h *agentPlanHarness) useStepsGrants(t *testing.T) {
	t.Helper()
	grants := &executiongrant.Service{Grants: h.gateway.repo, Prepared: h.gateway.repo,
		Policy: stepsPolicyOf(t, h.grants.Policy, h.app), Modes: stepSession{entry: approvalpolicy.StepEntryList}}
	executor, err := generationtool.New(generationtool.Config{Gateway: h.gateway, References: &fakeReferences{},
		Operations: h.gateway.repo, Grants: grants, Processing: h.plans,
		PlanCatalog: agentPlanCatalog{h.gateway.product}, Chat: h.app.Chat, StepPolicy: h.app.StepPolicy,
		StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	h.executor, h.grants = executor, grants
}

// #330 step 3B review: the plan tool's result key "steps" carries the plan's
// step array, never the standing object. A successful proposal keeps the array;
// a refusal has no such key at all, even though the session reports a standing.
func TestPlanToolResultNeverCarriesTheStepStanding(t *testing.T) {
	t.Run("success keeps the plan's step array", func(t *testing.T) {
		h := newAgentPlanHarness(t, approvalpolicy.ModeSteps, false)
		h.useStepsGrants(t)
		result, _ := h.propose(agentPlanArguments)
		content := h.content(result)
		steps, ok := content["steps"].([]any)
		if !ok || len(steps) != 3 {
			t.Fatalf("plan steps = %T %v; want the plan's three-step array", content["steps"], content["steps"])
		}
		for index, raw := range steps {
			item, _ := raw.(map[string]any)
			if item["capability"] == nil {
				t.Fatalf("plan step %d = %v", index, raw)
			}
			for _, key := range []string{catalog.StepResultMode, catalog.StepResultUsed} {
				if _, exists := item[key]; exists {
					t.Fatalf("plan step %d carries the standing property %q: %v", index, key, raw)
				}
			}
		}
	})
	t.Run("refusal has no steps key", func(t *testing.T) {
		h := newAgentPlanHarness(t, approvalpolicy.ModeSteps, false)
		h.useStepsGrants(t)
		result, err := h.executor.Execute(context.Background(),
			h.command(`{"source_task_id":"model-1","steps":[{"capability":"not-a-step","parameters":{}}]}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		content := h.content(result)
		if content["success"] != false {
			t.Fatalf("refusal = %v", content)
		}
		if raw, exists := content["steps"]; exists {
			t.Fatalf("a refused plan carried %q: %v", "steps", raw)
		}
		if _, err := h.grants.StepStatus(context.Background(), "owner-a", "session-a"); err != nil {
			t.Fatalf("the session reports no standing; the test proves nothing: %v", err)
		}
	})
}

// useStepsGrants rebuilds the processing harness with the shipped step policy
// and a session the steps mode governs.
func (h *processingHarness) useStepsGrants(t *testing.T) {
	t.Helper()
	grants := &executiongrant.Service{Grants: h.gateway.repo, Prepared: h.gateway.repo,
		Policy: stepsPolicyOf(t, h.policy(), h.app), Modes: stepSession{entry: approvalpolicy.StepEntryList}}
	h.config.Grants = grants
	h.config.StepPolicy = h.app.StepPolicy
	executor, err := generationtool.New(h.config)
	if err != nil {
		t.Fatal(err)
	}
	h.executor = executor
}

// #330 step 3B review: process_model's result follows the capability of this
// call's step. A uv-unwrap step is not a counted 3D step and carries no object,
// even though the tool's main capability (remesh) is counted; the next counted
// step in the same session still carries one.
func TestProcessingStepStandingFollowsTheCallsStepCapability(t *testing.T) {
	h := newProcessingHarness(t, approvalpolicy.ModeSteps, meshyCatalogTasks)
	h.useStepsGrants(t)

	unwrapped, _ := h.run("process_model", `{"step":"uv-unwrap","source_task_id":"model-1"}`)
	if unwrapped.NoSubmission || h.gateway.submitCount != 1 {
		t.Fatalf("uv-unwrap step: %+v submits=%d", unwrapped, h.gateway.submitCount)
	}
	if content := h.content(unwrapped); content["success"] != true {
		t.Fatalf("uv-unwrap result = %v", content)
	}
	if raw, exists := h.content(unwrapped)["steps"]; exists {
		t.Fatalf("a non-counted step carried a steps object: %v", raw)
	}

	remeshed, _ := h.run("process_model", `{"step":"remesh","source_task_id":"model-1","target_polycount":15000}`)
	if remeshed.NoSubmission || h.gateway.submitCount != 2 {
		t.Fatalf("remesh step: %+v submits=%d", remeshed, h.gateway.submitCount)
	}
	steps, ok := h.content(remeshed)["steps"].(map[string]any)
	if !ok {
		t.Fatalf("a counted step carried no steps object: %s", remeshed.AgentContent)
	}
	if steps[catalog.StepResultMode] != "agent" {
		t.Fatalf("remesh steps = %v", steps)
	}
	assertStepNumber(t, steps, catalog.StepResultUsed, 1)
}

// secondReadStepStore changes its answer after the first read, so a second read
// of the standing is visible instead of silently matching the first.
type secondReadStepStore struct {
	executiongrant.Repository
	stepStore executiongrant.StepGrantStore
	reads     int
	alternate executiongrant.StepSummary
}

func (s *secondReadStepStore) ApproveExecutionGrantWithSteps(ctx context.Context, owner, requestID string, request executiongrant.StepRequest) (executiongrant.Grant, executiongrant.StepDecision, error) {
	return s.stepStore.ApproveExecutionGrantWithSteps(ctx, owner, requestID, request)
}

func (s *secondReadStepStore) StepUsage(ctx context.Context, owner, sessionID string, request executiongrant.StepRequest) (executiongrant.StepSummary, error) {
	s.reads++
	if s.reads > 1 {
		return s.alternate, nil
	}
	return s.stepStore.StepUsage(ctx, owner, sessionID, request)
}

// #330 step 3B review: a refusal's text and its steps object come from the one
// standing the decision used. A second read would report the alternate numbers
// below, so the replay's object would contradict its own text.
func TestStepCapRefusalTextAndObjectShareOneRead(t *testing.T) {
	ctx := context.Background()
	executor, command, _, repo, service := stepHarness(t, "create_model_3d_from_image", "image-to-3d", nil)
	saveStepSettings(t, repo, capSettingOf(1), capSettingOf(1))
	if first, err := executor.Execute(ctx, command, nil); err != nil || first.NoSubmission {
		t.Fatalf("the step inside the cap: %+v err=%v", first, err)
	}
	over := nextCall(command, "call-b")
	if refused, err := executor.Execute(ctx, over, nil); err != nil || !refused.NoSubmission {
		t.Fatalf("the step over the cap: %+v err=%v", refused, err)
	}

	store := &secondReadStepStore{
		Repository: service.Grants,
		stepStore:  service.Grants.(executiongrant.StepGrantStore),
		alternate: executiongrant.StepSummary{Entry: approvalpolicy.StepEntryList,
			Mode: approvalpolicy.StepModeCap, Cap: 9, Used: 8},
	}
	service.Grants = store
	replay, err := executor.Execute(ctx, over, nil)
	if err != nil || !replay.NoSubmission {
		t.Fatalf("replay: %+v err=%v", replay, err)
	}
	if store.reads != 1 {
		t.Fatalf("the refusal read the standing %d times; want one read for text and object", store.reads)
	}
	const want = "这个资产的 3D 步数已用完（已用 1 步，上限 1 步），这一步没有执行。请不要再提交新的 3D 步骤，直接告诉用户已达到步数上限；用户可以在顶栏的「3D 步数」里调高步数上限后再继续。"
	if text := agentError(t, replay); text != want {
		t.Fatalf("replay text = %q", text)
	}
	steps := stepObjectOf(t, over, replay)
	assertStepNumber(t, steps, catalog.StepResultUsed, 1)
	assertStepNumber(t, steps, catalog.StepResultCap, 1)
	assertStepNumber(t, steps, catalog.StepResultRemaining, 0)
}
