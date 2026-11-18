package generationtool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
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
