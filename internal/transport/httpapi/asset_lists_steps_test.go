package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/project"
)

const assetListStepsRoute = "/api/v1/projects/{projectID}/asset-lists/{listID}/items/{itemID}/steps"

func assetListItemStepsRoute(projectID, listID, itemID string) string {
	path := assetListRoute(assetListStepsRoute, projectID, listID)
	return strings.ReplaceAll(path, "{itemID}", itemID)
}

type assetListStepsWire struct {
	Block *string `json:"block"`
	Steps *struct {
		Mode  string `json:"mode"`
		Used  int    `json:"used"`
		Cap   int    `json:"cap"`
		Extra int    `json:"extra"`
	} `json:"steps"`
	Version int64 `json:"version"`
}

// bindAssetListItemSession binds the item's execution row to a real chat
// session the way the runner's CreateItemSession does, and stores one counted
// 3D Task in that session so the asset reads as used.
func bindAssetListItemSession(t *testing.T, harness *assetListHTTPHarness, list assetlist.List, sessionID string) {
	t.Helper()
	if _, err := harness.repository.AssetListRunRepository().SetItemSession(
		context.Background(), "owner-a", list.ID, list.Items[0].ID, sessionID,
		assetlist.VersionUnchecked, time.Now().UTC()); err != nil {
		t.Fatalf("bind item session: %v", err)
	}
	database, err := sql.Open("sqlite", "file:"+harness.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO generation_tasks
 (id, owner_id, session_id, capability, offering_json, input_json, request_hash, idempotency_key,
  state, progress, current_attempt_id, materialize_failures, version, created_at, updated_at)
 VALUES ('step-task-1','owner-a',?,'text-to-3d','{}','{}','hash-step','key-step',
  'SUCCEEDED',0,'attempt-step',0,1,'2026-10-22T00:00:00Z','2026-10-22T00:00:00Z')`, sessionID); err != nil {
		t.Fatalf("insert step task: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO generation_attempts
 (id, task_id, ordinal, offering_json, state, submission_key, started_at, updated_at, last_observation_phase)
 VALUES ('attempt-step','step-task-1',1,'{}','SUCCEEDED','submission-step',
  '2026-10-22T00:00:00Z','2026-10-22T00:00:00Z','COMPLETED')`); err != nil {
		t.Fatalf("insert step attempt: %v", err)
	}
}

func readAssetListItem(t *testing.T, harness *assetListHTTPHarness, listID, itemID string) map[string]any {
	t.Helper()
	response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListItemRoute, harness.projectID, listID), "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("read list = %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, item := range body.Items {
		if item["id"] == itemID {
			return item
		}
	}
	t.Fatalf("item %s not in %v", itemID, body.Items)
	return nil
}

func saveListStepCap(t *testing.T, harness *assetListHTTPHarness, limit int) {
	t.Helper()
	body := `{"expected_version":0,"list":{"mode":"cap","cap":` + strconv.Itoa(limit) + `},"mcp":{"mode":"agent","cap":20}}`
	response := putStepPolicy(t, harness.handler, harness.cookie, body)
	if response.Code != http.StatusOK {
		t.Fatalf("save step policy = %d %s", response.Code, response.Body.String())
	}
}

// #330 step 5C: the list detail and the board carry the asset's live standing,
// and "give it a few more steps" raises the grant, clears a step-cap block and
// audits the change. The write needs the browser session; another owner's list
// is not found.
func TestProjectAssetListItemExtraStepsRaiseTheGrantAndAreAudited(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Steps", defaultListItem())
	harness.confirmAssetList(t, list.ID)
	saveListStepCap(t, harness, 1)
	bindAssetListItemSession(t, harness, list, harness.sessionID)

	item := readAssetListItem(t, harness, list.ID, list.Items[0].ID)
	steps, _ := item["steps"].(map[string]any)
	if steps == nil || steps["mode"] != "cap" || steps["used"] != float64(1) || steps["cap"] != float64(1) {
		t.Fatalf("live steps = %#v", item["steps"])
	}
	version := int64(item["version"].(float64))

	response := grantTestRequest(t, harness.handler,
		harness.cookie, http.MethodPut, assetListItemStepsRoute(harness.projectID, list.ID, list.Items[0].ID),
		`{"extra_steps":2,"expected_version":`+strconv.FormatInt(version, 10)+`}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("extra steps = %d %s", response.Code, response.Body.String())
	}
	var updated assetListStepsWire
	if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Steps == nil || updated.Steps.Extra != 2 || updated.Steps.Cap != 1 ||
		updated.Steps.Used != 1 || updated.Steps.Mode != "cap" || updated.Block != nil {
		t.Fatalf("updated = %+v steps=%+v block=%v", updated, updated.Steps, updated.Block)
	}
	// The list detail reads the same numbers back.
	item = readAssetListItem(t, harness, list.ID, list.Items[0].ID)
	steps, _ = item["steps"].(map[string]any)
	if steps == nil || steps["extra"] != float64(2) || steps["used"] != float64(1) {
		t.Fatalf("reread steps = %#v", item["steps"])
	}

	events, err := harness.repository.ListAdministrationAuditEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	audited := 0
	for _, event := range events {
		if event.Action != "add_step_extra" {
			continue
		}
		audited++
		if event.ActorID != "owner-a" || event.ResourceType != "asset_list_item_steps" ||
			event.ResourceID != list.Items[0].ID {
			t.Fatalf("audit event = %+v", event)
		}
		from := event.Detail["from"].(map[string]any)["extra_steps"]
		to := event.Detail["to"].(map[string]any)["extra_steps"]
		if from != float64(0) || to != float64(2) {
			t.Fatalf("audit detail = %+v", event.Detail)
		}
	}
	if audited != 1 {
		t.Fatalf("audit entries = %d, want one", audited)
	}

	// A stale item version, a decrease and the bounds are refused without a
	// second audit entry.
	for name, body := range map[string]string{
		"stale version": `{"extra_steps":3,"expected_version":` + strconv.FormatInt(version, 10) + `}`,
		"decrease":      `{"extra_steps":1,"expected_version":` + strconv.FormatInt(version+1, 10) + `}`,
		"single bound":  `{"extra_steps":60,"expected_version":` + strconv.FormatInt(version+1, 10) + `}`,
		"total bound":   `{"extra_steps":201,"expected_version":` + strconv.FormatInt(version+1, 10) + `}`,
	} {
		refused := grantTestRequest(t, harness.handler,
			harness.cookie, http.MethodPut, assetListItemStepsRoute(harness.projectID, list.ID, list.Items[0].ID),
			body, "")
		if refused.Code != http.StatusConflict {
			t.Errorf("%s = %d %s", name, refused.Code, refused.Body.String())
		}
	}
	events, err = harness.repository.ListAdministrationAuditEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	audited = 0
	for _, event := range events {
		if event.Action == "add_step_extra" {
			audited++
		}
	}
	if audited != 1 {
		t.Fatalf("refusals wrote an audit entry: %d", audited)
	}

	// A credential header can never raise an asset's cap.
	bearer := httptest.NewRequest(http.MethodPut,
		assetListItemStepsRoute(harness.projectID, list.ID, list.Items[0].ID),
		strings.NewReader(`{"extra_steps":3,"expected_version":`+strconv.FormatInt(version+1, 10)+`}`))
	bearer.Header.Set("Content-Type", "application/json")
	bearer.Header.Set("Origin", "http://example.com")
	bearer.Header.Set("Authorization", "Bearer an-external-mcp-token")
	bearer.AddCookie(harness.cookie)
	response = httptest.NewRecorder()
	harness.handler.ServeHTTP(response, bearer)
	if response.Code != http.StatusForbidden {
		t.Fatalf("credential write = %d %s", response.Code, response.Body.String())
	}

	// Another owner's list is 404 through the same route.
	now := time.Now().UTC()
	if _, err := harness.repository.ProjectRepository().CreateProject(context.Background(), project.Project{
		ID: "prj_owner_b", OwnerID: "owner-b", Name: "B", CreatedAt: now, UpdatedAt: now,
	}, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.repository.CreateSession(context.Background(), chat.Session{
		ID: "session-owner-b", OwnerID: "owner-b", OwnerName: "B", Title: "B",
		ProjectID: "prj_owner_b", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	foreign := harness.propose(t, "owner-b", "prj_owner_b", "session-owner-b", "Foreign", defaultListItem())
	if _, err := harness.repository.AssetListRunRepository().SetItemSession(
		context.Background(), "owner-b", foreign.ID, foreign.Items[0].ID, "session-owner-b",
		assetlist.VersionUnchecked, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	foreignResponse := grantTestRequest(t, harness.handler,
		harness.cookie, http.MethodPut, assetListItemStepsRoute("prj_owner_b", foreign.ID, foreign.Items[0].ID),
		`{"extra_steps":1,"expected_version":0}`, "")
	if foreignResponse.Code != http.StatusNotFound {
		t.Fatalf("foreign list = %d %s", foreignResponse.Code, foreignResponse.Body.String())
	}
}

// An item whose sub-session does not exist yet has no asset to extend; a
// missing or malformed body is invalid, never a silent zero.
func TestProjectAssetListItemExtraStepsRefuseAnUnstartedItemAndBadBodies(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "No session", defaultListItem())
	harness.confirmAssetList(t, list.ID)
	saveListStepCap(t, harness, 1)

	response := grantTestRequest(t, harness.handler,
		harness.cookie, http.MethodPut, assetListItemStepsRoute(harness.projectID, list.ID, list.Items[0].ID),
		`{"extra_steps":1,"expected_version":0}`, "")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "asset_list_step_unavailable") {
		t.Fatalf("unstarted item = %d %s", response.Code, response.Body.String())
	}
	for name, body := range map[string]string{
		"missing amount":   `{"expected_version":0}`,
		"missing version":  `{"extra_steps":1}`,
		"negative amount":  `{"extra_steps":-1,"expected_version":0}`,
		"negative version": `{"extra_steps":1,"expected_version":-1}`,
		"unknown field":    `{"extra_steps":1,"expected_version":0,"web":1}`,
	} {
		refused := grantTestRequest(t, harness.handler,
			harness.cookie, http.MethodPut, assetListItemStepsRoute(harness.projectID, list.ID, list.Items[0].ID),
			body, "")
		if refused.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, refused.Code, refused.Body.String())
		}
	}
}

// A Catalog that does not declare the per-item step route keeps it
// unregistered, like every optional route (#330).
func TestProjectAssetListItemExtraStepsRouteNeedsItsSchemaRoute(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarnessWith(t, func(application *catalog.ApplicationSchema) {
		application.Runtime.HTTP.Routes.ProjectAssetListItemSteps = ""
	})
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "No route", defaultListItem())
	response := grantTestRequest(t, harness.handler,
		harness.cookie, http.MethodPut, assetListItemStepsRoute(harness.projectID, list.ID, list.Items[0].ID),
		`{"extra_steps":1,"expected_version":0}`, "")
	if response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("route without the Schema key = %d %s", response.Code, response.Body.String())
	}
}
