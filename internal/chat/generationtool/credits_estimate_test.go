package generationtool

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestSuccessReportsEstimatedCreditsByMeteringState(t *testing.T) {
	tool := catalog.ChatToolSchema{ResultFields: catalog.ChatToolResultFields{
		Success: "success", TaskID: "task_id", ImageURLs: "image_urls",
		EstimatedCredits: "estimated_credits",
	}}
	for _, tc := range []struct {
		name      string
		info      stepInfo
		wantValue float64
		wantKey   bool
	}{
		{name: "known metered zero", info: stepInfo{Credits: 0, CreditsMetered: true}, wantKey: true},
		{name: "positive estimate", info: stepInfo{Credits: 7, CreditsMetered: true}, wantValue: 7, wantKey: true},
		{name: "unmetered zero", info: stepInfo{Credits: 0}},
		{name: "unmetered negative", info: stepInfo{Credits: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := (&Executor{}).success(tool, "task-a", nil, tc.info)
			var content map[string]any
			if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
				t.Fatalf("decode success result JSON: %v", err)
			}
			credits, ok := content[tool.ResultFields.EstimatedCredits]
			if ok != tc.wantKey || (ok && credits != tc.wantValue) {
				t.Fatalf("estimated credits presence/value = (%v, %v); want (%v, %v): %s",
					ok, credits, tc.wantKey, tc.wantValue, result.AgentContent)
			}
		})
	}
}

func TestTaskStepPreservesFrozenEstimateAndMetering(t *testing.T) {
	for _, tc := range []struct {
		name      string
		estimate  int
		metered   bool
		wantValue int
	}{
		{name: "known metered zero", metered: true},
		{name: "positive estimate", estimate: 7, metered: true, wantValue: 7},
		{name: "unmetered zero"},
		{name: "unmetered negative", estimate: -1, wantValue: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := generation.Task{Offering: generation.Offering{EstimatedCredits: tc.estimate, CreditsMetered: tc.metered}, Input: json.RawMessage(`{}`)}
			info := taskStep(task, "")
			if info.Credits != tc.wantValue || info.CreditsMetered != tc.metered {
				t.Fatalf("taskStep = %+v; want credits=%d metered=%v", info, tc.wantValue, tc.metered)
			}
		})
	}
}
