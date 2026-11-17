package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/project"
)

const (
	assetListProjectRoute  = "/api/v1/projects/{projectID}/asset-lists"
	assetListItemRoute     = "/api/v1/projects/{projectID}/asset-lists/{listID}"
	assetListDecisionRoute = "/api/v1/projects/{projectID}/asset-lists/{listID}/decision"
	assetListSessionRoute  = "/api/v1/chat/sessions/{sessionID}/asset-lists"
)

type assetListHTTPHarness struct {
	handler      http.Handler
	cookie       *http.Cookie
	repository   *sqlite.Repository
	databasePath string
	seed         *assetlist.Service
	product      catalog.ApplicationSchema
	projectID    string
	sessionID    string
	proposals    int
}

func assetListRoute(template, projectID, listID string) string {
	path := strings.ReplaceAll(template, "{projectID}", projectID)
	return strings.ReplaceAll(path, "{listID}", listID)
}

func assetListSeedService(t *testing.T, repository *sqlite.Repository, clock func() time.Time) *assetlist.Service {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	counted := make(map[generation.Capability]bool, len(application.StepPolicy.CountedCapabilities))
	for _, capability := range application.StepPolicy.CountedCapabilities {
		counted[generation.Capability(capability)] = true
	}
	service, err := assetlist.New(assetlist.Config{
		Repository:         repository.AssetListRepository(),
		Schema:             *application.AssetList,
		ProjectMaxItems:    application.Project.Limits.MaximumListItems,
		MaxListsPerProject: application.AssetList.Limits.MaxListsPerProject,
		Counted:            counted,
		ResolveStep: func(capability string) (assetlist.RouteStep, error) {
			return assetlist.RouteStep{Capability: capability, OfferingID: "seed-" + capability, Available: true}, nil
		},
		Now: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func newAssetListHTTPHarness(t *testing.T) *assetListHTTPHarness {
	t.Helper()
	module, handler, cookie, _ := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	state := value.(*fixtureExecutionState)

	created := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/projects",
		`{"name":"Asset Lists"}`, "")
	var createdProject struct {
		ID string `json:"id"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &createdProject) != nil ||
		createdProject.ID == "" {
		t.Fatalf("create project = %d %s", created.Code, created.Body.String())
	}
	session := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/chat/sessions",
		`{"title":"asset list chat","project_id":"`+createdProject.ID+`"}`, "")
	var createdSession struct {
		ID string `json:"id"`
	}
	if session.Code != http.StatusCreated || json.Unmarshal(session.Body.Bytes(), &createdSession) != nil ||
		createdSession.ID == "" {
		t.Fatalf("create session = %d %s", session.Code, session.Body.String())
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return &assetListHTTPHarness{
		handler: handler, cookie: cookie, repository: state.repository, databasePath: state.databasePath,
		seed: assetListSeedService(t, state.repository, nil), product: product.Application(),
		projectID: createdProject.ID, sessionID: createdSession.ID,
	}
}

func (harness *assetListHTTPHarness) propose(t *testing.T, owner, projectID, sessionID, title string,
	items []assetlist.ItemInput) assetlist.List {
	t.Helper()
	harness.proposals++
	list, err := harness.seed.Propose(context.Background(), assetlist.ProposeInput{
		Owner: owner, ProjectID: projectID, SessionID: sessionID,
		RequestKey: fmt.Sprintf("asset-list-test-%s-%d", t.Name(), harness.proposals),
		Title:      title, Items: items,
	})
	if err != nil {
		t.Fatalf("propose %q: %v", title, err)
	}
	return list
}

func defaultListItem() []assetlist.ItemInput {
	return []assetlist.ItemInput{{Kind: "character", Name: "Hero"}}
}

type assetListTestWire struct {
	ID                      string          `json:"id"`
	ProjectID               string          `json:"project_id"`
	SourceSessionID         string          `json:"source_session_id"`
	Title                   string          `json:"title"`
	Status                  string          `json:"status"`
	EffectiveStatus         string          `json:"effective_status"`
	ItemCount               int             `json:"item_count"`
	PlannedStepCount        int             `json:"planned_step_count"`
	PlannedModel3DStepCount int             `json:"planned_model3d_step_count"`
	Version                 int64           `json:"version"`
	Brief                   json.RawMessage `json:"brief"`
	Items                   []struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Name    string `json:"name"`
		RouteID string `json:"route_id"`
		Route   []struct {
			Capability string `json:"capability"`
			OfferingID string `json:"offering_id"`
			Available  bool   `json:"available"`
			Reason     string `json:"reason"`
		} `json:"route"`
		PlannedStepCount        int    `json:"planned_step_count"`
		PlannedModel3DStepCount int    `json:"planned_model3d_step_count"`
		State                   string `json:"state"`
		References              []struct {
			TaskID       string `json:"task_id"`
			AssetID      string `json:"asset_id"`
			InputAssetID string `json:"input_asset_id"`
		} `json:"references"`
	} `json:"items"`
}

type assetListTestProblem struct {
	Code            string `json:"code"`
	Detail          string `json:"detail"`
	RequestID       string `json:"request_id"`
	ActiveListID    string `json:"active_list_id"`
	ActiveListTitle string `json:"active_list_title"`
}

func decodeAssetListTestWire(t *testing.T, response *httptest.ResponseRecorder) assetListTestWire {
	t.Helper()
	var wire assetListTestWire
	if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
		t.Fatalf("asset list body %q: %v", response.Body.String(), err)
	}
	return wire
}

func decodeAssetListTestProblem(t *testing.T, response *httptest.ResponseRecorder) assetListTestProblem {
	t.Helper()
	var problem assetListTestProblem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("asset list problem %q: %v", response.Body.String(), err)
	}
	if problem.RequestID == "" {
		t.Fatalf("problem without request_id: %s", response.Body.String())
	}
	return problem
}

func decodeAssetListSummaries(t *testing.T, response *httptest.ResponseRecorder) []assetListTestWire {
	t.Helper()
	var body struct {
		Data []assetListTestWire `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("asset list summaries %q: %v", response.Body.String(), err)
	}
	return body.Data
}

func TestAssetListHTTPRequiresARealBrowserSession(t *testing.T) {
	t.Parallel()
	handler, _, _, _ := bearerKeyHarness(t)

	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet,
		assetListRoute(assetListProjectRoute, "prj_any", ""), nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d %s", anonymous.Code, anonymous.Body.String())
	}

	bearer := httptest.NewRequest(http.MethodGet, assetListRoute(assetListProjectRoute, "prj_any", ""), nil)
	bearer.Header.Set("Authorization", "Bearer "+deployToken)
	bearerResponse := httptest.NewRecorder()
	handler.ServeHTTP(bearerResponse, bearer)
	if bearerResponse.Code != http.StatusForbidden {
		t.Fatalf("bearer list = %d %s", bearerResponse.Code, bearerResponse.Body.String())
	}
	bearerDecision := httptest.NewRequest(http.MethodPut,
		assetListRoute(assetListDecisionRoute, "prj_any", "alist_any"), strings.NewReader(`{"decision":"confirm"}`))
	bearerDecision.Header.Set("Authorization", "Bearer "+deployToken)
	bearerDecision.Header.Set("Content-Type", "application/json")
	bearerDecisionResponse := httptest.NewRecorder()
	handler.ServeHTTP(bearerDecisionResponse, bearerDecision)
	if bearerDecisionResponse.Code != http.StatusForbidden {
		t.Fatalf("bearer decision = %d %s", bearerDecisionResponse.Code, bearerDecisionResponse.Body.String())
	}
}

func TestProjectAssetListsAreOwnerScopedAndCounted(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Hero Pack", defaultListItem())

	response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListProjectRoute, harness.projectID, ""), "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list = %d %s", response.Code, response.Body.String())
	}
	summaries := decodeAssetListSummaries(t, response)
	if len(summaries) != 1 || summaries[0].ID != list.ID || summaries[0].Title != "Hero Pack" ||
		summaries[0].Status != assetlist.StatusDraft || summaries[0].EffectiveStatus != assetlist.StatusDraft ||
		summaries[0].ItemCount != 1 || summaries[0].PlannedStepCount != 1 ||
		summaries[0].PlannedModel3DStepCount != 1 {
		t.Fatalf("summaries = %+v", summaries)
	}

	read := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListItemRoute, harness.projectID, list.ID), "", "")
	if read.Code != http.StatusOK {
		t.Fatalf("read = %d %s", read.Code, read.Body.String())
	}
	wire := decodeAssetListTestWire(t, read)
	if wire.ID != list.ID || wire.ItemCount != 1 || len(wire.Items) != 1 ||
		wire.Items[0].Route[0].Available != true || wire.Items[0].Route[0].OfferingID != "seed-text-to-3d" {
		t.Fatalf("read wire = %+v", wire)
	}

	// A second owner's project, session and list are 404 through every route.
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

	for _, path := range []string{
		assetListRoute(assetListProjectRoute, "prj_owner_b", ""),
		assetListRoute(assetListItemRoute, "prj_owner_b", foreign.ID),
		assetListRoute(assetListDecisionRoute, "prj_owner_b", foreign.ID),
	} {
		method := http.MethodGet
		if strings.HasSuffix(path, "/decision") {
			method = http.MethodPut
		}
		denied := grantTestRequest(t, harness.handler, harness.cookie, method, path, `{"decision":"confirm"}`, "")
		if denied.Code != http.StatusNotFound {
			t.Fatalf("foreign %s %s = %d %s", method, path, denied.Code, denied.Body.String())
		}
	}
	foreignSession := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		strings.ReplaceAll(assetListSessionRoute, "{sessionID}", "session-owner-b"), "", "")
	if foreignSession.Code != http.StatusNotFound {
		t.Fatalf("foreign session = %d %s", foreignSession.Code, foreignSession.Body.String())
	}

	missing := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListProjectRoute, "prj_missing", ""), "", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing project = %d %s", missing.Code, missing.Body.String())
	}
}

func TestChatSessionAssetListsReturnOnlyThatSessionsProposals(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	first := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "First", defaultListItem())

	second := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPost, "/api/chat/sessions",
		`{"title":"second","project_id":"`+harness.projectID+`"}`, "")
	var secondSession struct {
		ID string `json:"id"`
	}
	if second.Code != http.StatusCreated || json.Unmarshal(second.Body.Bytes(), &secondSession) != nil ||
		secondSession.ID == "" {
		t.Fatalf("second session = %d %s", second.Code, second.Body.String())
	}
	secondList := harness.propose(t, "owner-a", harness.projectID, secondSession.ID, "Second", defaultListItem())

	readSession := func(sessionID string) []assetListTestWire {
		t.Helper()
		response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
			strings.ReplaceAll(assetListSessionRoute, "{sessionID}", sessionID), "", "")
		if response.Code != http.StatusOK {
			t.Fatalf("session %s = %d %s", sessionID, response.Code, response.Body.String())
		}
		return decodeAssetListSummaries(t, response)
	}
	firstLists := readSession(harness.sessionID)
	if len(firstLists) != 1 || firstLists[0].ID != first.ID || firstLists[0].SourceSessionID != harness.sessionID {
		t.Fatalf("first session lists = %+v", firstLists)
	}
	secondLists := readSession(secondSession.ID)
	if len(secondLists) != 1 || secondLists[0].ID != secondList.ID || secondLists[0].SourceSessionID != secondSession.ID {
		t.Fatalf("second session lists = %+v", secondLists)
	}
}

func TestProjectAssetListEditReplacesDraftUnderVersion(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Draft", defaultListItem())
	path := assetListRoute(assetListItemRoute, harness.projectID, list.ID)

	edit := `{"title":"Edited","expected_version":0,"brief":{"purpose":"render"},"items":[` +
		`{"kind":"character","name":"Knight","route_id":"text-to-3d-remesh",` +
		`"references":[{"input_asset_id":"in_1"},{"task_id":"task_1","asset_id":"asset_1"}]},` +
		`{"kind":"image","name":"Icon","route_id":"image-edit"}]}`
	response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, path, edit, "")
	if response.Code != http.StatusOK {
		t.Fatalf("edit = %d %s", response.Code, response.Body.String())
	}
	edited := decodeAssetListTestWire(t, response)
	if edited.Title != "Edited" || edited.Version != 1 || edited.ItemCount != 2 ||
		edited.PlannedStepCount != 3 || edited.PlannedModel3DStepCount != 2 ||
		string(edited.Brief) != `{"purpose":"render"}` {
		t.Fatalf("edited = %+v", edited)
	}
	if len(edited.Items) != 2 || edited.Items[0].Name != "Knight" || len(edited.Items[0].References) != 2 ||
		edited.Items[0].References[0].InputAssetID != "in_1" ||
		edited.Items[0].References[1].TaskID != "task_1" || edited.Items[0].References[1].AssetID != "asset_1" {
		t.Fatalf("edited items = %+v", edited.Items)
	}
	// Every step snapshots availability and the runtime offering; an
	// unavailable one is kept, never swapped.
	steps := edited.Items[0].Route
	if len(steps) != 2 || !steps[0].Available || steps[0].OfferingID != "harness-text-to-3d" ||
		steps[1].Capability != "remesh" || steps[1].Available ||
		!strings.Contains(steps[1].Reason, "disabled in the harness") {
		t.Fatalf("edited route = %+v", steps)
	}

	stale := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, path,
		`{"title":"Stale","expected_version":0,"items":[{"kind":"character","name":"Hero"}]}`, "")
	if stale.Code != http.StatusConflict || decodeAssetListTestProblem(t, stale).Code != "conflict" {
		t.Fatalf("stale = %d %s", stale.Code, stale.Body.String())
	}
	// References are explicit project references; an @handle is not parsed here.
	handle := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, path,
		`{"title":"Handle","expected_version":1,"items":[{"kind":"character","name":"Hero","references":["@img1"]}]}`, "")
	if handle.Code != http.StatusBadRequest {
		t.Fatalf("handle = %d %s", handle.Code, handle.Body.String())
	}
	tooMany := make([]string, 0, harness.product.Project.Limits.MaximumListItems+1)
	for index := 0; index <= harness.product.Project.Limits.MaximumListItems; index++ {
		tooMany = append(tooMany, fmt.Sprintf(`{"kind":"image","name":"Icon %d","route_id":"text-to-image"}`, index))
	}
	limit := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, path,
		`{"title":"Too many","expected_version":1,"items":[`+strings.Join(tooMany, ",")+`]}`, "")
	if limit.Code != http.StatusConflict || decodeAssetListTestProblem(t, limit).Code != "asset_list_limit" {
		t.Fatalf("limit = %d %s", limit.Code, limit.Body.String())
	}

	confirm := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, path+"/decision",
		`{"decision":"confirm"}`, "")
	if confirm.Code != http.StatusOK || decodeAssetListTestWire(t, confirm).Status != assetlist.StatusConfirmed {
		t.Fatalf("confirm = %d %s", confirm.Code, confirm.Body.String())
	}
	afterConfirm := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, path,
		`{"title":"Again","expected_version":2,"items":[{"kind":"character","name":"Hero"}]}`, "")
	if afterConfirm.Code != http.StatusConflict {
		t.Fatalf("confirmed edit = %d %s", afterConfirm.Code, afterConfirm.Body.String())
	}
}

func TestProjectAssetListDecisionFlow(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	first := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "First", defaultListItem())
	firstPath := assetListRoute(assetListDecisionRoute, harness.projectID, first.ID)

	confirm := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		return grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, path, `{"decision":"confirm"}`, "")
	}
	if response := confirm(firstPath); response.Code != http.StatusOK {
		t.Fatalf("confirm first = %d %s", response.Code, response.Body.String())
	}
	// A double click decides once and answers the current list.
	repeated := confirm(firstPath)
	if repeated.Code != http.StatusOK || decodeAssetListTestWire(t, repeated).Status != assetlist.StatusConfirmed {
		t.Fatalf("repeated confirm = %d %s", repeated.Code, repeated.Body.String())
	}

	second := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Second", defaultListItem())
	secondPath := assetListRoute(assetListDecisionRoute, harness.projectID, second.ID)
	refused := confirm(secondPath)
	if refused.Code != http.StatusConflict {
		t.Fatalf("second confirm = %d %s", refused.Code, refused.Body.String())
	}
	problem := decodeAssetListTestProblem(t, refused)
	if problem.Code != "asset_list_active_conflict" || problem.ActiveListID != first.ID ||
		problem.ActiveListTitle != "First" {
		t.Fatalf("active conflict = %+v", problem)
	}

	replace := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, secondPath,
		`{"decision":"replace","reason":"new plan"}`, "")
	if replace.Code != http.StatusOK || decodeAssetListTestWire(t, replace).Status != assetlist.StatusConfirmed {
		t.Fatalf("replace = %d %s", replace.Code, replace.Body.String())
	}
	old := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListItemRoute, harness.projectID, first.ID), "", "")
	if old.Code != http.StatusOK || decodeAssetListTestWire(t, old).Status != assetlist.StatusSuperseded {
		t.Fatalf("superseded = %d %s", old.Code, old.Body.String())
	}

	// Replacing a confirmed list whose item is working would strand it.
	third := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Third", defaultListItem())
	if response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut,
		assetListRoute(assetListDecisionRoute, harness.projectID, third.ID),
		`{"decision":"replace"}`, ""); response.Code != http.StatusOK {
		t.Fatalf("replace third = %d %s", response.Code, response.Body.String())
	}
	setAssetListItemWorking(t, harness.databasePath, third.ID)
	fourth := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Fourth", defaultListItem())
	working := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut,
		assetListRoute(assetListDecisionRoute, harness.projectID, fourth.ID), `{"decision":"replace"}`, "")
	if working.Code != http.StatusConflict {
		t.Fatalf("working replace = %d %s", working.Code, working.Body.String())
	}
	workingProblem := decodeAssetListTestProblem(t, working)
	if workingProblem.Code != "asset_list_active_working" || workingProblem.ActiveListID != third.ID {
		t.Fatalf("working conflict = %+v", workingProblem)
	}

	fifth := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Fifth", defaultListItem())
	rejectPath := assetListRoute(assetListDecisionRoute, harness.projectID, fifth.ID)
	if response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, rejectPath,
		`{"decision":"reject","reason":"not now"}`, ""); response.Code != http.StatusOK ||
		decodeAssetListTestWire(t, response).Status != assetlist.StatusRejected {
		t.Fatalf("reject = %d %s", response.Code, response.Body.String())
	}
	if response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, rejectPath,
		`{"decision":"reject"}`, ""); response.Code != http.StatusOK ||
		decodeAssetListTestWire(t, response).Status != assetlist.StatusRejected {
		t.Fatalf("repeated reject = %d %s", response.Code, response.Body.String())
	}

	// An expired draft is stored as expired and refused with 409.
	expiredService := assetListSeedService(t, harness.repository,
		func() time.Time { return time.Now().UTC().Add(-8 * 24 * time.Hour) })
	expired, err := expiredService.Propose(context.Background(), assetlist.ProposeInput{
		Owner: "owner-a", ProjectID: harness.projectID, SessionID: harness.sessionID,
		RequestKey: "asset-list-test-expired", Title: "Expired", Items: defaultListItem(),
	})
	if err != nil {
		t.Fatal(err)
	}
	readExpired := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListItemRoute, harness.projectID, expired.ID), "", "")
	if readExpired.Code != http.StatusOK || decodeAssetListTestWire(t, readExpired).EffectiveStatus != assetlist.StatusExpired {
		t.Fatalf("expired read = %d %s", readExpired.Code, readExpired.Body.String())
	}
	expiredDecision := grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut,
		assetListRoute(assetListDecisionRoute, harness.projectID, expired.ID), `{"decision":"confirm"}`, "")
	if expiredDecision.Code != http.StatusConflict ||
		decodeAssetListTestProblem(t, expiredDecision).Code != "asset_list_expired" {
		t.Fatalf("expired decision = %d %s", expiredDecision.Code, expiredDecision.Body.String())
	}
}

func setAssetListItemWorking(t *testing.T, databasePath, listID string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE asset_list_items SET state = ? WHERE list_id = ?`,
		assetlist.StateWorking, listID); err != nil {
		t.Fatal(err)
	}
}
