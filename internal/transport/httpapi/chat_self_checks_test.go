package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func seedSelfCheckTask(t *testing.T, repository *sqlite.Repository, taskID string) {
	t.Helper()
	now := time.Now().UTC()
	if err := repository.ImportGenerationTasks(context.Background(), []sqlite.GenerationTaskImport{{
		Task: generation.Task{
			ID: generation.TaskID(taskID), OwnerID: "owner-a", Capability: "image-to-3d",
			Offering: generation.Offering{ID: "meshy/image-to-3d", ProviderID: "meshy", ProviderName: "Meshy", ModelName: "meshy-6"},
			Input:    json.RawMessage(`{}`), RequestHash: "hash", State: generation.TaskSucceeded,
			CreatedAt: now, UpdatedAt: now,
		},
		Attempts: []sqlite.GenerationAttemptImport{{
			ID: generation.AttemptID(taskID + "-attempt"), Ordinal: 1,
			Offering:      generation.Offering{ID: "meshy/image-to-3d", ProviderID: "meshy", ProviderName: "Meshy"},
			State:         generation.AttemptUpstreamSucceeded,
			SubmissionKey: taskID + "-submission", StartedAt: now, UpdatedAt: now,
		}},
	}}); err != nil {
		t.Fatal(err)
	}
}

func selfChecksPath(sessionID string) string {
	return "/api/v1/chat/sessions/" + sessionID + "/self-checks"
}

func TestChatSessionSelfChecksReturnsTheOwnerRecordsAndKeepsInternalsServerSide(t *testing.T) {
	t.Parallel()
	module, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	seedSelfCheckTask(t, repository, "task-a")
	now := time.Now().UTC()
	record := chat.SelfCheckRecord{
		TaskID: "task-a", ModelAssetID: "asset-a", CreatedAt: now, UpdatedAt: now,
		Checks: []chat.SelfCheckCheck{
			{Round: 1, Kind: chat.SelfCheckKindInspect, Tool: "inspect_model", OK: true, At: now},
			{Round: 1, Kind: chat.SelfCheckKindReference, Tool: "compare_model_reference",
				Views: []string{"front"}, Handles: []string{"@img1"}, OK: true, At: now,
				Metrics: &chat.SelfCheckMetrics{SilhouetteIOU: 0.75, BBoxAspectDelta: 0.1,
					DominantColorDistance: 12, DominantReference: "#112233", DominantRender: "#445566",
					ReferenceSubjectRatio: 0.2, RenderSubjectRatio: 0.3, Method: "normalized_silhouette_iou_v1"}},
		},
		Conclusion: &chat.SelfCheckConclusion{Status: "passed", Summary: "matches", Findings: []string{"outline ok"}, At: now},
	}
	stored, conflict, err := repository.SaveSelfCheck(context.Background(), "owner-a", sessionID, record, 0)
	if err != nil || conflict || stored.Revision != 1 {
		t.Fatalf("seed = %+v conflict=%v err=%v", stored, conflict, err)
	}
	if listed, err := repository.ListSelfChecks(context.Background(), "owner-a", sessionID); err != nil || len(listed) != 1 {
		t.Fatalf("listed = %v err=%v", listed, err)
	}
	response := grantTestRequest(t, handler, cookie, http.MethodGet, selfChecksPath(sessionID), "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	body := response.Body.String()
	for _, needle := range []string{`"selfChecks"`, `"taskId":"task-a"`, `"status":"passed"`, `"roundsUsed":1`,
		`"rendersInRound":1`, `"correctionsUsed":0`, `"kind":"reference"`, `"silhouette_iou":0.75`,
		`"@img1"`, `"summary":"matches"`} {
		if !strings.Contains(body, needle) {
			t.Fatalf("body lacks %s: %s", needle, body)
		}
	}
	for _, leaked := range []string{"subject_threshold", "normalized_side", "record_json", "revision",
		"unchecked_rule_text", "method"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("body leaks %q: %s", leaked, body)
		}
	}

	// A foreign or missing session is the same 404 and its records never leak.
	foreign := chat.SelfCheckRecord{TaskID: "task-foreign", UpdatedAt: now}
	if _, err := repository.CreateSession(context.Background(), chat.Session{ID: "foreign-session", OwnerID: "owner-b",
		OwnerName: "Other", Title: "other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := repository.ImportGenerationTasks(context.Background(), []sqlite.GenerationTaskImport{{
		Task: generation.Task{ID: "task-foreign", OwnerID: "owner-b", Capability: "image-to-3d",
			Offering: generation.Offering{ID: "meshy/image-to-3d", ProviderID: "meshy", ProviderName: "Meshy"},
			Input:    json.RawMessage(`{}`), RequestHash: "hash", State: generation.TaskSucceeded,
			CreatedAt: now, UpdatedAt: now},
		Attempts: []sqlite.GenerationAttemptImport{{
			ID: "task-foreign-attempt", Ordinal: 1,
			Offering:      generation.Offering{ID: "meshy/image-to-3d", ProviderID: "meshy", ProviderName: "Meshy"},
			State:         generation.AttemptUpstreamSucceeded,
			SubmissionKey: "task-foreign-submission", StartedAt: now, UpdatedAt: now,
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.SaveSelfCheck(context.Background(), "owner-b", "foreign-session", foreign, 0); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{selfChecksPath("foreign-session"), selfChecksPath("no-such-session")} {
		response := grantTestRequest(t, handler, cookie, http.MethodGet, path, "", "")
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "task-foreign") {
			t.Fatalf("foreign record leaked: %s", response.Body.String())
		}
	}
}

func TestChatSessionSelfChecksReturnsAnEmptyListForANewSession(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	response := grantTestRequest(t, handler, cookie, http.MethodGet, selfChecksPath(sessionID), "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		SelfChecks []any `json:"selfChecks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SelfChecks == nil || len(body.SelfChecks) != 0 {
		t.Fatalf("selfChecks = %#v (want an empty array)", body.SelfChecks)
	}
}
