package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #410: the plan's per-step standing, the finished state and the continue copy
// are Schema data: the server projects the state ids, and the browser and the
// Agent read the wording from here.
func TestShippedPlanStateDeclaresStepStatesAndContinueCopy(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	for _, key := range []string{
		"ui.studio.plan.state.completed",
		"ui.studio.plan.step_state.pending",
		"ui.studio.plan.step_state.running",
		"ui.studio.plan.step_state.done",
		"ui.studio.plan.step_state.failed",
		"ui.studio.plan.step_state.not_run",
		"ui.studio.plan.step_state.optional_not_run",
		"ui.studio.plan.stopped",
		"ui.studio.plan.stopped_one",
		"ui.studio.plan.continue",
	} {
		if app.Content.Messages[key] == "" {
			t.Errorf("the plan state copy %q is missing", key)
		}
	}
	// The four view states must read differently, or a step that never ran
	// would look like one the person may skip.
	seen := map[string]bool{}
	for _, state := range []string{"pending", "running", "done", "failed", "not_run", "optional_not_run"} {
		text := app.Content.Messages["ui.studio.plan.step_state."+state]
		if seen[text] {
			t.Errorf("the step state %q shares its wording with another state: %q", state, text)
		}
		seen[text] = true
	}
	// The stop rule the Agent reads when it ends a turn with a plan unfinished.
	if !strings.Contains(app.Chat.Agent.SystemPrompt, "停在哪一步") {
		t.Error("the Agent prompt does not say to report where an unfinished plan stopped")
	}
	// A step the person may skip is declared on the plan tool, so the Agent can
	// mark an extra view optional instead of leaving it unrun forever.
	if !planStepDeclaresOptional(t, app) {
		t.Error("the plan tool does not declare an optional step flag")
	}
}

// A turn must be able to self-check a whole group of views (#410 raised the
// bound from three to six), so the declared maximum is at least four.
func TestShippedImageReviewBoundsOneTurnOfFourViews(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if rounds := c.Application().Chat.ImageReview.MaxRounds; rounds < 4 {
		t.Fatalf("image_review.max_rounds = %d; a turn must self-check four views", rounds)
	}
}

func planStepDeclaresOptional(t *testing.T, app catalog.ApplicationSchema) bool {
	t.Helper()
	for _, tool := range app.Chat.AgentTools {
		if tool.Name != "propose_production_plan" {
			continue
		}
		var parameters struct {
			Properties struct {
				Steps struct {
					Items struct {
						Properties map[string]json.RawMessage `json:"properties"`
					} `json:"items"`
				} `json:"steps"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
			t.Fatalf("plan tool parameters: %v", err)
		}
		return parameters.Properties.Steps.Items.Properties["optional"] != nil
	}
	return false
}
