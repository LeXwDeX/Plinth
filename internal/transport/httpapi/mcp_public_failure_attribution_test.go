package httpapi_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #517: a credit-metered Task whose failure class the Schema declares a
// no-auto-retry text for may already have been charged. The external MCP tells
// the caller what the platform Agent is told: ask the person, no retry, and
// user_decision_required, both in the task and in the status block.
func TestMCPMeteredFailedTaskSendsTheCallerToThePerson(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read")
	charged := f.succeededTask("owner-a", "", "failed-metered")
	f.exec(`UPDATE generation_tasks SET state = 'FAILED', failure_json = ?,
		offering_json = json_set(offering_json, '$.credits_metered', json('true')) WHERE id = ?`,
		`{"class":"integrity_failure","retryable":true,"detail":"stored bytes rejected"}`, string(charged.ID))
	task, err := f.module.Get(context.Background(), "owner-a", charged.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !task.Offering.CreditsMetered {
		t.Fatalf("the fixture task is not metered: %+v", task.Offering)
	}
	report, reason := generationtool.TaskFailureReport(f.application.Chat, task)
	if reason == "" || report.Action != generation.ActionAskUser || report.Retryable || !report.UserDecisionRequired {
		t.Fatalf("the attribution of a metered integrity failure = %+v %q", report, reason)
	}

	result := f.call(key, "get_task", map[string]any{"task_id": string(charged.ID)})
	failure := result.structured["task"].(map[string]any)["failure"].(map[string]any)
	block := result.status(t)["failure"].(map[string]any)
	for name, got := range map[string]map[string]any{"task": failure, "status block": block} {
		if got["category"] != string(report.Category) || got["next_action"] != string(report.Action) ||
			got["retryable"] != report.Retryable || got["user_decision_required"] != report.UserDecisionRequired {
			t.Errorf("%s failure = %v; want the attribution %+v", name, got, report)
		}
	}
	if listed := f.call(key, "get_task_assets", map[string]any{"task_id": string(charged.ID)}); listed.status(t)["failure"].(map[string]any)["next_action"] != "ask_user" {
		t.Errorf("get_task_assets of a metered failure = %s", listed.raw)
	}
	// list_tasks reads the same summary projection and says the same.
	page := f.call(key, "list_tasks", map[string]any{})
	found := false
	for _, raw := range page.structured["tasks"].([]any) {
		listed := raw.(map[string]any)
		if listed["id"] != string(charged.ID) {
			continue
		}
		found = true
		got, _ := listed["failure"].(map[string]any)
		if got["next_action"] != string(report.Action) || got["retryable"] != report.Retryable ||
			got["user_decision_required"] != report.UserDecisionRequired {
			t.Errorf("list_tasks failure = %v; want the attribution %+v", got, report)
		}
	}
	if !found {
		t.Errorf("list_tasks did not list the failed task: %s", page.raw)
	}
}
