package httpapi_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

const projectPresenceRoute = "/api/v1/projects/{projectID}/presence"

type presenceHTTPHarness struct {
	handler      http.Handler
	cookie       *http.Cookie
	databasePath string
	projectID    string
}

// newPresenceHTTPHarness starts the full HTTP stack with the default Catalog
// (which declares asset_list.execution) and creates the owner's project.
func newPresenceHTTPHarness(t *testing.T) *presenceHTTPHarness {
	t.Helper()
	return newPresenceHTTPHarnessWith(t, nil)
}

func newPresenceHTTPHarnessWith(t *testing.T, customize func(*catalog.ApplicationSchema)) *presenceHTTPHarness {
	t.Helper()
	module, handler, cookie, _ := grantTestBrowserWithApplication(t, customize, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	state := value.(*fixtureExecutionState)
	created := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/projects",
		`{"name":"Presence"}`, "")
	var project struct {
		ID string `json:"id"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &project) != nil ||
		project.ID == "" {
		t.Fatalf("create project = %d %s", created.Code, created.Body.String())
	}
	return &presenceHTTPHarness{
		handler: handler, cookie: cookie, databasePath: state.databasePath, projectID: project.ID,
	}
}

func presenceRequest(t *testing.T, handler http.Handler, cookie *http.Cookie,
	projectID, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	path := strings.ReplaceAll(projectPresenceRoute, "{projectID}", projectID)
	return grantTestRequest(t, handler, cookie, http.MethodPost, path, body, "")
}

func presenceBody(tabID, state string) string {
	return `{"tab_id":"` + tabID + `","state":"` + state + `"}`
}

type presenceProblem struct {
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	RequestID string `json:"request_id"`
}

func decodePresenceProblem(t *testing.T, response *httptest.ResponseRecorder) presenceProblem {
	t.Helper()
	var problem presenceProblem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("presence problem %q: %v", response.Body.String(), err)
	}
	if problem.RequestID == "" {
		t.Fatalf("problem without request_id: %s", response.Body.String())
	}
	return problem
}

func openPresenceTestDB(t *testing.T, databasePath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// presenceRows returns tab_id -> last_seen_at for the project.
func presenceRows(t *testing.T, databasePath, projectID string) map[string]string {
	t.Helper()
	rows, err := openPresenceTestDB(t, databasePath).Query(
		`SELECT tab_id, last_seen_at FROM asset_list_presence WHERE project_id = ?`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := make(map[string]string)
	for rows.Next() {
		var tabID, at string
		if err := rows.Scan(&tabID, &at); err != nil {
			t.Fatal(err)
		}
		seen[tabID] = at
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return seen
}

func TestProjectPresenceHeartbeatIsIdempotentAndRefreshes(t *testing.T) {
	t.Parallel()
	harness := newPresenceHTTPHarness(t)

	present := presenceRequest(t, harness.handler, harness.cookie, harness.projectID,
		presenceBody("tab-a", "present"))
	if present.Code != http.StatusNoContent || present.Body.Len() != 0 {
		t.Fatalf("present = %d %q", present.Code, present.Body.String())
	}
	seen := presenceRows(t, harness.databasePath, harness.projectID)
	if len(seen) != 1 || seen["tab-a"] == "" {
		t.Fatalf("rows after present = %v", seen)
	}

	repeated := presenceRequest(t, harness.handler, harness.cookie, harness.projectID,
		presenceBody("tab-a", "present"))
	if repeated.Code != http.StatusNoContent {
		t.Fatalf("repeated present = %d %s", repeated.Code, repeated.Body.String())
	}
	seen = presenceRows(t, harness.databasePath, harness.projectID)
	if len(seen) != 1 || seen["tab-a"] == "" {
		t.Fatalf("repeated present rows = %v", seen)
	}

	// A heartbeat moves last_seen_at forward without adding a row.
	const old = "2000-01-01T00:00:00.000000000Z"
	db := openPresenceTestDB(t, harness.databasePath)
	if _, err := db.Exec(`UPDATE asset_list_presence SET last_seen_at = ? WHERE project_id = ?`,
		old, harness.projectID); err != nil {
		t.Fatal(err)
	}
	refreshed := presenceRequest(t, harness.handler, harness.cookie, harness.projectID,
		presenceBody("tab-a", "present"))
	if refreshed.Code != http.StatusNoContent {
		t.Fatalf("refresh = %d %s", refreshed.Code, refreshed.Body.String())
	}
	seen = presenceRows(t, harness.databasePath, harness.projectID)
	if len(seen) != 1 || seen["tab-a"] == old || seen["tab-a"] == "" {
		t.Fatalf("refresh rows = %v", seen)
	}
}

func TestProjectPresenceLeaveRemovesOnlyThatTab(t *testing.T) {
	t.Parallel()
	harness := newPresenceHTTPHarness(t)
	for _, tab := range []string{"tab-a", "tab-b"} {
		response := presenceRequest(t, harness.handler, harness.cookie, harness.projectID,
			presenceBody(tab, "present"))
		if response.Code != http.StatusNoContent {
			t.Fatalf("present %s = %d %s", tab, response.Code, response.Body.String())
		}
	}
	if seen := presenceRows(t, harness.databasePath, harness.projectID); len(seen) != 2 {
		t.Fatalf("rows with two tabs = %v", seen)
	}

	leave := presenceRequest(t, harness.handler, harness.cookie, harness.projectID,
		presenceBody("tab-a", "leave"))
	if leave.Code != http.StatusNoContent || leave.Body.Len() != 0 {
		t.Fatalf("leave = %d %q", leave.Code, leave.Body.String())
	}
	seen := presenceRows(t, harness.databasePath, harness.projectID)
	if _, present := seen["tab-a"]; present || len(seen) != 1 || seen["tab-b"] == "" {
		t.Fatalf("rows after leave tab-a = %v", seen)
	}
	// A repeated leave is a no-op, not an error.
	if again := presenceRequest(t, harness.handler, harness.cookie, harness.projectID,
		presenceBody("tab-a", "leave")); again.Code != http.StatusNoContent {
		t.Fatalf("repeated leave = %d %s", again.Code, again.Body.String())
	}
	if seen := presenceRows(t, harness.databasePath, harness.projectID); len(seen) != 1 {
		t.Fatalf("rows after repeated leave = %v", seen)
	}
}

func TestProjectPresenceRequiresARealBrowserSession(t *testing.T) {
	t.Parallel()
	handler, _, _, _ := bearerKeyHarness(t)

	anonymous := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost,
		strings.ReplaceAll(projectPresenceRoute, "{projectID}", "prj_any"),
		strings.NewReader(presenceBody("tab-a", "present")))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(anonymous, request)
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d %s", anonymous.Code, anonymous.Body.String())
	}

	bearer := httptest.NewRequest(http.MethodPost,
		strings.ReplaceAll(projectPresenceRoute, "{projectID}", "prj_any"),
		strings.NewReader(presenceBody("tab-a", "present")))
	bearer.Header.Set("Content-Type", "application/json")
	bearer.Header.Set("Authorization", "Bearer "+deployToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, bearer)
	if response.Code != http.StatusForbidden {
		t.Fatalf("bearer = %d %s", response.Code, response.Body.String())
	}
}

func TestProjectPresenceValidatesTabAndState(t *testing.T) {
	t.Parallel()
	harness := newPresenceHTTPHarness(t)
	cases := []struct {
		name string
		body string
	}{
		{"unknown state", presenceBody("tab-a", "paused")},
		{"empty state", `{"tab_id":"tab-a"}`},
		{"empty tab", presenceBody("", "present")},
		{"blank tab", presenceBody("   ", "present")},
		{"overlong tab", presenceBody(strings.Repeat("t", 129), "present")},
		{"malformed json", `{"tab_id":`},
	}
	for _, test := range cases {
		response := presenceRequest(t, harness.handler, harness.cookie, harness.projectID, test.body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s", test.name, response.Code, response.Body.String())
		}
		problem := decodePresenceProblem(t, response)
		if problem.Code != "invalid_input" && problem.Code != "invalid_json" {
			t.Fatalf("%s code = %q", test.name, problem.Code)
		}
	}
	if seen := presenceRows(t, harness.databasePath, harness.projectID); len(seen) != 0 {
		t.Fatalf("invalid requests stored rows: %v", seen)
	}
	// The maximum length is accepted.
	bounded := presenceRequest(t, harness.handler, harness.cookie, harness.projectID,
		presenceBody(strings.Repeat("t", 128), "present"))
	if bounded.Code != http.StatusNoContent {
		t.Fatalf("128-byte tab = %d %s", bounded.Code, bounded.Body.String())
	}
}

func TestProjectPresenceIsOwnerScopedAndHidesUnknownProjects(t *testing.T) {
	t.Parallel()
	harness := newPresenceHTTPHarness(t)
	const foreignProject = "prj_presence_foreign"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := openPresenceTestDB(t, harness.databasePath).Exec(
		`INSERT INTO projects (id, owner_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		foreignProject, "owner-b", "Foreign", now, now); err != nil {
		t.Fatal(err)
	}
	for _, projectID := range []string{foreignProject, "prj_presence_missing"} {
		response := presenceRequest(t, harness.handler, harness.cookie, projectID,
			presenceBody("tab-a", "present"))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s = %d %s", projectID, response.Code, response.Body.String())
		}
		if problem := decodePresenceProblem(t, response); problem.Code != "not_found" {
			t.Fatalf("%s code = %q", projectID, problem.Code)
		}
		if seen := presenceRows(t, harness.databasePath, projectID); len(seen) != 0 {
			t.Fatalf("%s stored rows: %v", projectID, seen)
		}
	}
}

func TestProjectPresenceRouteIsAbsentWithoutExecution(t *testing.T) {
	t.Parallel()
	harness := newPresenceHTTPHarnessWith(t, func(application *catalog.ApplicationSchema) {
		application.AssetList.Execution = nil
	})
	response := presenceRequest(t, harness.handler, harness.cookie, harness.projectID,
		presenceBody("tab-a", "present"))
	if response.Code != http.StatusNotFound {
		t.Fatalf("presence without execution = %d %s", response.Code, response.Body.String())
	}
}
