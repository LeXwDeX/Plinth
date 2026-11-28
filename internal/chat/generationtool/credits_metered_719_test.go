package generationtool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #719: an Offering that does not bill in credits (GPT Image, Gemini, Jimeng,
// matting) keeps its internal estimate for approval, but neither the Agent's
// tool result nor the chat observation (the result card's 本步消耗) states it
// as credits; a credits-metered Offering keeps both.
func TestAgentToolStatesCreditsOnlyForCreditsMeteredOfferings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		metered bool
		want    int
	}{
		{name: "metered", metered: true, want: 7},
		{name: "unmetered", metered: false, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor, command, gateway, _ := policyHarness(t, sessionMode{mode: "budget"}, "refine_image", "text-to-image")
			gateway.offerings[0].CreditsMetered = tc.metered
			var observed []chat.ToolObservation
			result, err := executor.Execute(context.Background(), command, func(o chat.ToolObservation) error {
				observed = append(observed, o)
				return nil
			})
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if gateway.submitCount != 1 || len(observed) == 0 || observed[0].EstimatedCredits != tc.want {
				t.Fatalf("submits=%d observation=%+v; want credits %d", gateway.submitCount, observed, tc.want)
			}
			var content map[string]any
			if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
				t.Fatalf("agent content %q: %v", result.AgentContent, err)
			}
			credits, ok := content["estimated_credits"]
			if ok != tc.metered || (ok && credits != float64(tc.want)) {
				t.Fatalf("estimated_credits = (%v, %v); want present=%v value=%d: %s", credits, ok, tc.metered, tc.want, result.AgentContent)
			}
		})
	}
}
