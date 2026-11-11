package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func rerollRoute(taskID string) string { return "/api/v1/tasks/" + taskID + "/reroll" }

// TestTaskRerollRequiresAnIdempotencyKeyAndScopesTheTask covers the public
// edges of #242: no request starts without a key, and an unknown or foreign
// task is a 404 rather than a new paid task. The happy path (a succeeded task
// with a frozen input) runs end-to-end on the fake stack.
func TestTaskRerollRequiresAnIdempotencyKeyAndScopesTheTask(t *testing.T) {
	t.Parallel()
	handler, cookie := retryBrowser(t)

	missingKey := grantTestRequest(t, handler, cookie, http.MethodPost, rerollRoute("failed-retryable"), "", "")
	if missingKey.Code != http.StatusBadRequest {
		t.Fatalf("without a key status=%d body=%s", missingKey.Code, missingKey.Body.String())
	}
	var problem struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(missingKey.Body.Bytes(), &problem); err != nil ||
		problem.Code != "idempotency_key_required" || problem.Detail == "" {
		t.Fatalf("without a key body=%s err=%v", missingKey.Body.String(), err)
	}

	unknown := grantTestRequest(t, handler, cookie, http.MethodPost, rerollRoute("does-not-exist"), "", "client-1")
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown task status=%d body=%s", unknown.Code, unknown.Body.String())
	}

	// A failed task is not rerollable (that is retry's job).
	failed := grantTestRequest(t, handler, cookie, http.MethodPost, rerollRoute("failed-retryable"), "", "client-2")
	if failed.Code != http.StatusConflict {
		t.Fatalf("failed task status=%d body=%s", failed.Code, failed.Body.String())
	}
	if err := json.Unmarshal(failed.Body.Bytes(), &problem); err != nil || problem.Code != "task_not_rerollable" {
		t.Fatalf("failed task body=%s err=%v", failed.Body.String(), err)
	}
}
