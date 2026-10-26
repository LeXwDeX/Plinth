package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// The public route requires the versions shown when the user clicked Resume.
// Resume itself does not submit a paid Task (the background loop is stopped).
func TestProcessingPlanResumeRouteRequiresCurrentVersions(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "art-ai.db")
	application, err := New(Config{
		Environment:               "development",
		Address:                   "127.0.0.1:0",
		DatabasePath:              databasePath,
		StorageRoot:               filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock",
		AgentMode:                 "mock",
		RuntimeSettings:           map[string]string{"llm_model": "configured-test-model"},
		AuthMode:                  "development",
		DevelopmentUser:           "owner-a",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.generation.Close() })
	handler := application.server.Handler

	schema := appChatRequest(handler, http.MethodGet, "/api/v1/schema", "")
	if schema.Code != http.StatusOK || !strings.Contains(schema.Body.String(), `"processing_plan_resume":"/api/v1/processing-plans/{planID}/resume"`) {
		t.Fatalf("public schema lacks resume route: %d", schema.Code)
	}

	create := appChatRequest(handler, http.MethodPost, "/api/chat/sessions", `{}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create session status=%d body=%s", create.Code, create.Body)
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &session); err != nil || session.ID == "" {
		t.Fatalf("session = %s, %v", create.Body, err)
	}

	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("meshy/processing/remesh")
	if !ok {
		t.Fatal("remesh offering missing from default catalog")
	}
	repository, err := sqlite.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	now := time.Now().UTC()
	if err := repository.CreateProcessingPlan(t.Context(), processing.Plan{
		ID: "paused-plan", OwnerID: "owner-a", SessionID: session.ID, SourceTaskID: "source", SourceAssetID: "asset",
		State: processing.Paused, Failure: processing.ReasonSubmitUnknown, RequestHash: "request",
		Steps: []processing.Step{{Capability: offering.Capability, OfferingID: offering.ID, Input: json.RawMessage(`{}`),
			EstimatedCredits: offering.EstimatedCredits, Revision: offering.Revision, State: processing.Paused, Failure: processing.ReasonSubmitUnknown}},
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	missingVersion := appChatRequest(handler, http.MethodPost, "/api/v1/processing-plans/paused-plan/resume",
		`{"expected_offering_revision":"`+offering.Revision+`"}`)
	if missingVersion.Code != http.StatusBadRequest {
		t.Fatalf("resume without version status=%d body=%s", missingVersion.Code, missingVersion.Body)
	}
	staleOffering := appChatRequest(handler, http.MethodPost, "/api/v1/processing-plans/paused-plan/resume",
		`{"expected_plan_version":1,"expected_offering_revision":"old"}`)
	if staleOffering.Code != http.StatusConflict {
		t.Fatalf("resume with stale offering status=%d body=%s", staleOffering.Code, staleOffering.Body)
	}
	missing := appChatRequest(handler, http.MethodPost, "/api/v1/processing-plans/unknown-plan/resume",
		`{"expected_plan_version":1,"expected_offering_revision":"`+offering.Revision+`"}`)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("resume unknown plan status=%d body=%s", missing.Code, missing.Body)
	}
	resumed := appChatRequest(handler, http.MethodPost, "/api/v1/processing-plans/paused-plan/resume",
		`{"expected_plan_version":1,"expected_offering_revision":"`+offering.Revision+`"}`)
	if resumed.Code != http.StatusAccepted {
		t.Fatalf("resume status=%d body=%s", resumed.Code, resumed.Body)
	}
	var plan processing.Plan
	if err := json.Unmarshal(resumed.Body.Bytes(), &plan); err != nil || plan.State != processing.Pending || plan.Steps[0].State != processing.Pending || plan.Version != 2 {
		t.Fatalf("resumed plan = %s, %v", resumed.Body, err)
	}
	again := appChatRequest(handler, http.MethodPost, "/api/v1/processing-plans/paused-plan/resume",
		`{"expected_plan_version":1,"expected_offering_revision":"`+offering.Revision+`"}`)
	if again.Code != http.StatusConflict {
		t.Fatalf("second resume status=%d body=%s", again.Code, again.Body)
	}
}
