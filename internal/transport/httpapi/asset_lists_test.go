package httpapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/listcrop"
	"github.com/LeXwDeX/Plinth/internal/project"
)

const (
	assetListProjectRoute  = "/api/v1/projects/{projectID}/asset-lists"
	assetListItemRoute     = "/api/v1/projects/{projectID}/asset-lists/{listID}"
	assetListDecisionRoute = "/api/v1/projects/{projectID}/asset-lists/{listID}/decision"
	assetListBoardRoute    = "/api/v1/projects/{projectID}/asset-lists/{listID}/board"
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
	return newAssetListHTTPHarnessWith(t, nil)
}

func newAssetListHTTPHarnessWith(t *testing.T, customize func(*catalog.ApplicationSchema)) *assetListHTTPHarness {
	t.Helper()
	module, handler, cookie, _ := grantTestBrowserWithApplication(t, customize, []string{"user"})
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
	Run                     *struct {
		Status      string `json:"status"`
		PauseReason string `json:"pause_reason"`
		Version     string `json:"version"`
		ItemCounts  struct {
			Todo     int `json:"todo"`
			Working  int `json:"working"`
			Review   int `json:"review"`
			Accepted int `json:"accepted"`
			Returned int `json:"returned"`
			Failed   int `json:"failed"`
			Skipped  int `json:"skipped"`
		} `json:"item_counts"`
	} `json:"run"`
	Items []struct {
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
		StateReason             string `json:"state_reason"`
		Version                 int64  `json:"version"`
		References              []struct {
			TaskID       string `json:"task_id"`
			AssetID      string `json:"asset_id"`
			InputAssetID string `json:"input_asset_id"`
		} `json:"references"`
		Crop *struct {
			SourceInputAssetID string `json:"source_input_asset_id"`
			SourceKind         string `json:"source_kind"`
			SourceWidth        int    `json:"source_width"`
			SourceHeight       int    `json:"source_height"`
			Region             struct {
				X      float64 `json:"x"`
				Y      float64 `json:"y"`
				Width  float64 `json:"width"`
				Height float64 `json:"height"`
			} `json:"region"`
			PixelRect struct {
				X0 int `json:"x0"`
				Y0 int `json:"y0"`
				X1 int `json:"x1"`
				Y1 int `json:"y1"`
			} `json:"pixel_rect"`
		} `json:"crop"`
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
	// #331: the edit route validates every input-asset reference against the
	// owner's stored assets, so the picture must exist.
	seedAssetListInputAsset(t, harness.repository, "owner-a", "in_1", "image/png", "image")

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

type assetListBoardTestWire struct {
	List  assetListTestWire `json:"list"`
	Items []struct {
		ID               string          `json:"id"`
		Name             string          `json:"name"`
		State            string          `json:"state"`
		Round            int             `json:"round"`
		ProjectAssetID   string          `json:"project_asset_id"`
		PlannedStepCount int             `json:"planned_step_count"`
		Result           json.RawMessage `json:"result"`
		Review           json.RawMessage `json:"review"`
		LatestReview     *struct {
			ID         string          `json:"id"`
			Round      int             `json:"round"`
			Verdict    string          `json:"verdict"`
			Reason     string          `json:"reason"`
			ReviewerID string          `json:"reviewer_id"`
			Review     json.RawMessage `json:"review"`
		} `json:"latest_review"`
	} `json:"items"`
}

func decodeAssetListBoard(t *testing.T, response *httptest.ResponseRecorder) assetListBoardTestWire {
	t.Helper()
	var board assetListBoardTestWire
	if err := json.Unmarshal(response.Body.Bytes(), &board); err != nil {
		t.Fatalf("asset list board %q: %v", response.Body.String(), err)
	}
	return board
}

func TestProjectAssetListBoardRequiresARealBrowserSession(t *testing.T) {
	t.Parallel()
	handler, _, _, _ := bearerKeyHarness(t)
	path := assetListRoute(assetListBoardRoute, "prj_any", "alist_any")

	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, path, nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous board = %d %s", anonymous.Code, anonymous.Body.String())
	}

	bearer := httptest.NewRequest(http.MethodGet, path, nil)
	bearer.Header.Set("Authorization", "Bearer "+deployToken)
	bearerResponse := httptest.NewRecorder()
	handler.ServeHTTP(bearerResponse, bearer)
	if bearerResponse.Code != http.StatusForbidden {
		t.Fatalf("bearer board = %d %s", bearerResponse.Code, bearerResponse.Body.String())
	}
}

func TestProjectAssetListBoardAggregatesReadOnlyAndOwnerScoped(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Board", defaultListItem())
	path := assetListRoute(assetListBoardRoute, harness.projectID, list.ID)

	empty := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet, path, "", "")
	if empty.Code != http.StatusOK {
		t.Fatalf("board = %d %s", empty.Code, empty.Body.String())
	}
	board := decodeAssetListBoard(t, empty)
	if board.List.ID != list.ID || board.List.Status != assetlist.StatusDraft ||
		board.List.ItemCount != 1 || board.List.Run != nil ||
		len(board.Items) != 1 || board.Items[0].ID != list.Items[0].ID ||
		board.Items[0].PlannedStepCount != 1 || board.Items[0].LatestReview != nil ||
		string(board.Items[0].Result) != `{}` {
		t.Fatalf("empty board = %+v", board)
	}

	db, err := sql.Open("sqlite", "file:"+harness.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	itemID := list.Items[0].ID
	if _, err := db.Exec(`
		UPDATE asset_list_items SET state = ?, round = 2, project_asset_id = ?,
			result_json = ? WHERE id = ?`,
		assetlist.StateAccepted, "passet_1", `{"format":"glb","task_id":"task_1"}`, itemID); err != nil {
		t.Fatal(err)
	}
	for _, review := range []struct {
		id, verdict, reason, review, createdAt string
		round                                  int
	}{
		{"rev_old", assetlist.ReviewVerdictReturned, "size", `{"steps":2}`, "2026-11-18T12:00:00.000000000Z", 1},
		{"rev_new", assetlist.ReviewVerdictApproved, "ok", `{"steps":4}`, "2026-11-18T12:01:00.000000000Z", 2},
	} {
		if _, err := db.Exec(`
			INSERT INTO asset_list_item_reviews (id, owner_id, list_id, item_id, round,
				verdict, reason, reviewer_id, review_json, created_at)
			VALUES (?, 'owner-a', ?, ?, ?, ?, ?, 'owner-a', ?, ?)`,
			review.id, list.ID, itemID, review.round, review.verdict, review.reason,
			review.review, review.createdAt); err != nil {
			t.Fatal(err)
		}
	}
	var updatedBefore string
	if err := db.QueryRow(`SELECT updated_at FROM asset_list_items WHERE id = ?`, itemID).
		Scan(&updatedBefore); err != nil {
		t.Fatal(err)
	}

	response := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet, path, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("seeded board = %d %s", response.Code, response.Body.String())
	}
	board = decodeAssetListBoard(t, response)
	item := board.Items[0]
	if item.State != assetlist.StateAccepted || item.Round != 2 ||
		item.ProjectAssetID != "passet_1" ||
		string(item.Result) != `{"format":"glb","task_id":"task_1"}` {
		t.Fatalf("board item = %+v", item)
	}
	if item.LatestReview == nil || item.LatestReview.ID != "rev_new" ||
		item.LatestReview.Verdict != assetlist.ReviewVerdictApproved ||
		item.LatestReview.Round != 2 || item.LatestReview.ReviewerID != "owner-a" ||
		string(item.LatestReview.Review) != `{"steps":4}` {
		t.Fatalf("latest review = %+v", item.LatestReview)
	}

	// A board GET is read-only: it records no decision and touches no item.
	var updatedAfter string
	if err := db.QueryRow(`SELECT updated_at FROM asset_list_items WHERE id = ?`, itemID).
		Scan(&updatedAfter); err != nil {
		t.Fatal(err)
	}
	if updatedAfter != updatedBefore {
		t.Fatalf("board updated the item: %q -> %q", updatedBefore, updatedAfter)
	}
	var reviewCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_list_item_reviews`).Scan(&reviewCount); err != nil ||
		reviewCount != 2 {
		t.Fatalf("reviews after board read = %d, %v", reviewCount, err)
	}

	// Another owner's list is 404 through the same route.
	now := time.Now().UTC()
	if _, err := harness.repository.ProjectRepository().CreateProject(context.Background(), project.Project{
		ID: "prj_board_b", OwnerID: "owner-b", Name: "B", CreatedAt: now, UpdatedAt: now,
	}, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.repository.CreateSession(context.Background(), chat.Session{
		ID: "session-board-b", OwnerID: "owner-b", OwnerName: "B", Title: "B",
		ProjectID: "prj_board_b", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	foreign := harness.propose(t, "owner-b", "prj_board_b", "session-board-b", "Foreign", defaultListItem())
	for _, target := range []string{
		assetListRoute(assetListBoardRoute, "prj_board_b", foreign.ID),
		assetListRoute(assetListBoardRoute, harness.projectID, "alist_missing"),
		assetListRoute(assetListBoardRoute, "prj_missing", list.ID),
	} {
		missing := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet, target, "", "")
		if missing.Code != http.StatusNotFound {
			t.Fatalf("board %s = %d %s", target, missing.Code, missing.Body.String())
		}
	}
}

// seedAssetListInputAsset stores one input asset row. Reference validation only
// reads the recorded metadata, so no bytes are needed.
func seedAssetListInputAsset(t *testing.T, repository *sqlite.Repository,
	owner, id, mediaType, assetType string,
) {
	t.Helper()
	err := repository.CreateInputAsset(context.Background(), generation.InputAsset{
		ID:               generation.AssetID(id),
		OwnerID:          generation.OwnerID(owner),
		StorageKey:       "seed/" + id,
		OriginalFilename: id + ".png",
		AssetType:        assetType,
		MediaType:        mediaType,
		Size:             1,
		SHA256:           strings.Repeat("a", 64),
		CreatedAt:        time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("seed input asset %s: %v", id, err)
	}
}

// seedAssetListCrop stores one crop input asset and its provenance (#331).
func seedAssetListCrop(t *testing.T, repository *sqlite.Repository,
	cropID, sourceID string,
) {
	t.Helper()
	seedAssetListInputAsset(t, repository, "owner-a", cropID, "image/png", "list_crop")
	// Crops of one source share the source id but never the pixel rect: the
	// provenance store's unique key is (owner, source, rect).
	rects := map[string]listcrop.PixelRect{
		"crop_1": {X0: 0, Y0: 0, X1: 500, Y1: 500},
		"crop_2": {X0: 500, Y0: 0, X1: 1000, Y1: 500},
		"crop_3": {X0: 0, Y0: 500, X1: 500, Y1: 1000},
	}
	rect, ok := rects[cropID]
	if !ok {
		rect = listcrop.PixelRect{X0: 0, Y0: 0, X1: 500, Y1: 500}
	}
	_, _, err := repository.InputAssetCropRepository().InsertOrGet(context.Background(), listcrop.Crop{
		AssetID:            generation.AssetID(cropID),
		OwnerID:            "owner-a",
		SourceAssetID:      generation.AssetID(sourceID),
		SourceKind:         listcrop.SourceKindInput,
		SourceInputAssetID: generation.AssetID(sourceID),
		SourceWidth:        1000,
		SourceHeight:       1000,
		Region:             listcrop.Region{X: 0, Y: 0, Width: 0.5, Height: 0.5},
		PixelRect:          rect,
		SHA256:             strings.Repeat("b", 64),
		CreatedAt:          time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("seed crop %s: %v", cropID, err)
	}
}

// #331: an edit PUT carries bare references, so the crop rules are enforced on
// that path too; a list cannot bypass them by skipping reference_input_asset_ids.
func TestProjectAssetListEditValidatesBareCropReferences(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID, "Draft", defaultListItem())
	path := assetListRoute(assetListItemRoute, harness.projectID, list.ID)
	seedAssetListCrop(t, harness.repository, "crop_1", "scene_a")
	seedAssetListCrop(t, harness.repository, "crop_2", "scene_a")
	seedAssetListCrop(t, harness.repository, "crop_3", "scene_b")
	// A list_crop row without provenance is inconsistent stored data.
	seedAssetListInputAsset(t, harness.repository, "owner-a", "crop_orphan", "image/png", "list_crop")
	// A stored picture that is not a crop stays a plain reference.
	seedAssetListInputAsset(t, harness.repository, "owner-a", "in_1", "image/png", "image")

	edit := func(title string, items string) *httptest.ResponseRecorder {
		return grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut, path,
			`{"title":"`+title+`","expected_version":0,"items":[`+items+`]}`, "")
	}
	// Two crops in one item.
	twoCrops := edit("Two crops", `{"kind":"character","name":"Knight",`+
		`"references":[{"input_asset_id":"crop_1"},{"input_asset_id":"crop_2"}]}`)
	if twoCrops.Code != http.StatusBadRequest {
		t.Fatalf("two crops = %d %s", twoCrops.Code, twoCrops.Body.String())
	}
	// Crops from different source pictures across items.
	crossSource := edit("Cross source", `{"kind":"character","name":"Knight",`+
		`"references":[{"input_asset_id":"crop_1"}]},`+
		`{"kind":"character","name":"Squire","references":[{"input_asset_id":"crop_3"}]}`)
	if crossSource.Code != http.StatusBadRequest {
		t.Fatalf("cross source = %d %s", crossSource.Code, crossSource.Body.String())
	}
	// A crop without provenance.
	orphan := edit("Orphan", `{"kind":"character","name":"Knight",`+
		`"references":[{"input_asset_id":"crop_orphan"}]}`)
	if orphan.Code != http.StatusBadRequest {
		t.Fatalf("orphan = %d %s", orphan.Code, orphan.Body.String())
	}
	// A missing or foreign input asset.
	missing := edit("Missing", `{"kind":"character","name":"Knight",`+
		`"references":[{"input_asset_id":"in_missing"}]}`)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing = %d %s", missing.Code, missing.Body.String())
	}
	// One crop plus a plain picture, same source picture across items: accepted.
	valid := edit("Valid", `{"kind":"character","name":"Knight",`+
		`"references":[{"input_asset_id":"crop_1"},{"input_asset_id":"in_1"}]},`+
		`{"kind":"character","name":"Squire","references":[{"input_asset_id":"crop_2"}]}`)
	if valid.Code != http.StatusOK {
		t.Fatalf("valid = %d %s", valid.Code, valid.Body.String())
	}
	edited := decodeAssetListTestWire(t, valid)
	if len(edited.Items) != 2 || len(edited.Items[0].References) != 2 ||
		edited.Items[0].References[0].InputAssetID != "crop_1" ||
		edited.Items[1].References[0].InputAssetID != "crop_2" {
		t.Fatalf("edited references = %+v", edited.Items)
	}
}

// assetListCropPicture is a 1000x1000 PNG with flat quadrants: big enough for
// the declared 64-pixel shortest side and cheap to encode.
func assetListCropPicture() image.Image {
	picture := image.NewNRGBA(image.Rect(0, 0, 1000, 1000))
	quadrants := [4]color.NRGBA{
		{R: 220, G: 40, B: 40, A: 255},
		{R: 40, G: 220, B: 40, A: 255},
		{R: 40, G: 40, B: 220, A: 255},
		{R: 220, G: 220, B: 40, A: 255},
	}
	for y := 0; y < 1000; y++ {
		for x := 0; x < 1000; x++ {
			index := 0
			if x >= 500 {
				index += 1
			}
			if y >= 500 {
				index += 2
			}
			picture.SetNRGBA(x, y, quadrants[index])
		}
	}
	return picture
}

// uploadScene stores one real picture through the browser upload route, so the
// edit crop instruction has bytes to decode, and returns its input asset id.
func (harness *assetListHTTPHarness) uploadScene(t *testing.T, picture image.Image) string {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="`+
		harness.product.Runtime.InputAssets.UploadFieldName+`"; filename="scene.png"`)
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(encoded.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost,
		harness.product.Runtime.HTTP.Routes.Assets, bytes.NewReader(body.Bytes()))
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Origin", "http://example.com")
	request.AddCookie(harness.cookie)
	response := httptest.NewRecorder()
	harness.handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload scene = %d %s", response.Code, response.Body.String())
	}
	var asset struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &asset); err != nil || asset.ID == "" {
		t.Fatalf("upload scene body %q: %v", response.Body.String(), err)
	}
	return asset.ID
}

func (harness *assetListHTTPHarness) editDraft(t *testing.T, listID, body string) *httptest.ResponseRecorder {
	t.Helper()
	return grantTestRequest(t, harness.handler, harness.cookie, http.MethodPut,
		assetListRoute(assetListItemRoute, harness.projectID, listID), body, "")
}

// #331: an edit item may carry a crop instruction. The server cuts one region
// with the real listcrop service, stores a list_crop input asset with its
// provenance and puts its id in that item's references_json.
func TestProjectAssetListEditCropInstructionCutsAndReferences(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID,
		"Draft", defaultListItem())
	source := harness.uploadScene(t, assetListCropPicture())
	body := fmt.Sprintf(`{"title":"Cropped","expected_version":0,"items":[`+
		`{"kind":"character","name":"Knight","crop":{"source_input_asset_id":%q,`+
		`"region":{"x":0.1,"y":0.2,"width":0.4,"height":0.5}}}]}`, source)
	response := harness.editDraft(t, list.ID, body)
	if response.Code != http.StatusOK {
		t.Fatalf("crop edit = %d %s", response.Code, response.Body.String())
	}
	edited := decodeAssetListTestWire(t, response)
	if edited.Version != 1 || len(edited.Items) != 1 || len(edited.Items[0].References) != 1 {
		t.Fatalf("edited wire = %+v", edited)
	}
	cropID := edited.Items[0].References[0].InputAssetID
	if cropID == "" || cropID == source {
		t.Fatalf("crop reference = %+v", edited.Items[0].References)
	}
	provenance, found, err := harness.repository.InputAssetCropRepository().Get(
		context.Background(), generation.OwnerID("owner-a"), generation.AssetID(cropID))
	if err != nil || !found {
		t.Fatalf("crop provenance found=%v err=%v", found, err)
	}
	if provenance.SourceAssetID != generation.AssetID(source) ||
		provenance.PixelRect != (listcrop.PixelRect{X0: 100, Y0: 200, X1: 500, Y1: 700}) {
		t.Fatalf("crop provenance = %+v", provenance)
	}
}

// #331: the edit refusal codes of a crop instruction stay in the asset-list
// vocabulary and reuse the decompose copy.
func TestProjectAssetListEditCropInstructionRefusalCodes(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID,
		"Draft", defaultListItem())
	source := harness.uploadScene(t, assetListCropPicture())
	put := func(title, region string) *httptest.ResponseRecorder {
		return harness.editDraft(t, list.ID,
			fmt.Sprintf(`{"title":%q,"expected_version":0,"items":[`+
				`{"kind":"character","name":"Knight","crop":{"source_input_asset_id":%q,`+
				`"region":%s}}]}`, title, source, region))
	}
	decompose := harness.product.AssetList.Decompose

	outOfBounds := put("Out of bounds", `{"x":0.8,"y":0.1,"width":0.5,"height":0.2}`)
	if outOfBounds.Code != http.StatusBadRequest {
		t.Fatalf("out of bounds = %d %s", outOfBounds.Code, outOfBounds.Body.String())
	}
	if problem := decodeAssetListTestProblem(t, outOfBounds); problem.Code != "asset_list_region_invalid" ||
		problem.Detail != decompose.Messages.RegionInvalid {
		t.Fatalf("out of bounds problem = %+v", problem)
	}

	tooSmall := put("Too small", `{"x":0.1,"y":0.1,"width":0.01,"height":0.01}`)
	if tooSmall.Code != http.StatusBadRequest {
		t.Fatalf("too small = %d %s", tooSmall.Code, tooSmall.Body.String())
	}
	wantSmall := strings.ReplaceAll(decompose.Messages.RegionTooSmall, "{minimum}",
		fmt.Sprint(decompose.Crop.MinimumShortEdge))
	if problem := decodeAssetListTestProblem(t, tooSmall); problem.Code != "asset_list_region_too_small" ||
		problem.Detail != wantSmall {
		t.Fatalf("too small problem = %+v", problem)
	}

	missing := harness.editDraft(t, list.ID, fmt.Sprintf(`{"title":"Missing",`+
		`"expected_version":0,"items":[{"kind":"character","name":"Knight",`+
		`"crop":{"source_input_asset_id":"in_missing",`+
		`"region":{"x":0.1,"y":0.1,"width":0.4,"height":0.4}}}]}`))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing source = %d %s", missing.Code, missing.Body.String())
	}
	if problem := decodeAssetListTestProblem(t, missing); problem.Code != "asset_list_reference_invalid" ||
		problem.Detail != decompose.Messages.ReferenceInvalid {
		t.Fatalf("missing source problem = %+v", problem)
	}
}

// #331 review: maximum_items_per_image is a product cap over the items of one
// list, not a storage cap on re-framing. One item re-framed repeatedly stays
// below the cap; a second item cut from the same picture is refused.
func TestProjectAssetListEditCropInstructionHonorsPerImageLimit(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarnessWith(t, func(schema *catalog.ApplicationSchema) {
		schema.AssetList.Decompose.MaximumItemsPerImage = 1
	})
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID,
		"Draft", []assetlist.ItemInput{
			{Kind: "character", Name: "Knight"},
			{Kind: "image", Name: "Icon"},
		})
	source := harness.uploadScene(t, assetListCropPicture())
	first := harness.editDraft(t, list.ID, fmt.Sprintf(`{"title":"First",`+
		`"expected_version":0,"items":[{"kind":"character","name":"Knight",`+
		`"crop":{"source_input_asset_id":%q,`+
		`"region":{"x":0.1,"y":0.1,"width":0.4,"height":0.4}}},`+
		`{"kind":"image","name":"Icon"}]}`, source))
	if first.Code != http.StatusOK {
		t.Fatalf("first crop = %d %s", first.Code, first.Body.String())
	}
	// Re-framing the same item is one item in the list, so the cap still holds.
	reframed := harness.editDraft(t, list.ID, fmt.Sprintf(`{"title":"Reframed",`+
		`"expected_version":1,"items":[{"kind":"character","name":"Knight",`+
		`"crop":{"source_input_asset_id":%q,`+
		`"region":{"x":0.5,"y":0.5,"width":0.4,"height":0.4}}},`+
		`{"kind":"image","name":"Icon"}]}`, source))
	if reframed.Code != http.StatusOK {
		t.Fatalf("reframed crop = %d %s", reframed.Code, reframed.Body.String())
	}
	second := harness.editDraft(t, list.ID, fmt.Sprintf(`{"title":"Second",`+
		`"expected_version":2,"items":[{"kind":"character","name":"Knight",`+
		`"crop":{"source_input_asset_id":%q,`+
		`"region":{"x":0.5,"y":0.5,"width":0.4,"height":0.4}}},`+
		`{"kind":"image","name":"Icon",`+
		`"crop":{"source_input_asset_id":%q,`+
		`"region":{"x":0.1,"y":0.5,"width":0.4,"height":0.4}}}]}`, source, source))
	if second.Code != http.StatusBadRequest {
		t.Fatalf("second item = %d %s", second.Code, second.Body.String())
	}
	if problem := decodeAssetListTestProblem(t, second); problem.Code != "asset_list_image_item_limit" {
		t.Fatalf("second item problem = %+v", problem)
	}
}

// #331 review: the storage guardrail is a multiple of maximum_items_per_image,
// so repeatedly re-framing a full list keeps saving: three rounds of 24 items
// cut 72 distinct rectangles, far past the product cap but under the guard.
func TestProjectAssetListEditCropStorageGuardAllowsRepeatedRecrops(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	source := harness.uploadScene(t, assetListCropPicture())
	items := func(round int) string {
		parts := make([]string, 24)
		for index := 0; index < 24; index++ {
			// A 6x4 grid with a 2-pixel per-round offset: every round cuts 24
			// rectangles that collide neither within the round nor across rounds.
			x := 0.16*float64(index%6) + 0.002*float64(round)
			y := 0.22*float64(index/6) + 0.002*float64(round)
			parts[index] = fmt.Sprintf(
				`{"kind":"image","name":"Item %d","crop":{"source_input_asset_id":%q,`+
					`"region":{"x":%v,"y":%v,"width":0.1,"height":0.1}}}`,
				index+1, source, x, y)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID,
		"Draft", []assetlist.ItemInput{{Kind: "image", Name: "Seed"}})
	version := list.Version
	for round := 0; round < 3; round++ {
		response := harness.editDraft(t, list.ID, fmt.Sprintf(
			`{"title":"Round %d","expected_version":%d,"items":%s}`, round+1, version, items(round)))
		if response.Code != http.StatusOK {
			t.Fatalf("round %d edit = %d %s", round+1, response.Code, response.Body.String())
		}
		version = decodeAssetListTestWire(t, response).Version
	}
	count, err := harness.repository.InputAssetCropRepository().CountBySource(
		context.Background(), generation.OwnerID("owner-a"), generation.AssetID(source))
	if err != nil || count != 72 {
		t.Fatalf("stored crops = %d, %v; want 72 accumulated reframes", count, err)
	}
}

// #331 review: a region over the pixel limit is its own refusal code and copy,
// not the generic malformed-region one.
func TestProjectAssetListEditCropInstructionTooLarge(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarnessWith(t, func(schema *catalog.ApplicationSchema) {
		schema.AssetList.Decompose.Crop.MaximumRegionPixels = 5_000
	})
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID,
		"Draft", defaultListItem())
	source := harness.uploadScene(t, assetListCropPicture())
	response := harness.editDraft(t, list.ID, fmt.Sprintf(`{"title":"Too large",`+
		`"expected_version":0,"items":[{"kind":"character","name":"Knight",`+
		`"crop":{"source_input_asset_id":%q,`+
		`"region":{"x":0,"y":0,"width":1,"height":1}}}]}`, source))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("too large = %d %s", response.Code, response.Body.String())
	}
	want := harness.product.AssetList.Decompose.Messages.ImageTooLarge
	if problem := decodeAssetListTestProblem(t, response); problem.Code != "asset_list_image_too_large" ||
		problem.Detail != want {
		t.Fatalf("too large problem = %+v; want code asset_list_image_too_large and %q", problem, want)
	}
}

// #331: the list detail exposes the crop provenance of an item's crop
// reference, so the merge-duplicates editor can union the normalized regions.
// The source picture itself may be gone: the provenance row still answers.
func TestProjectAssetListDetailExposesCropProvenance(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID,
		"Draft", defaultListItem())
	// scene_a is never stored as an input asset: provenance outlives its
	// source picture, and the detail still reports the geometry.
	seedAssetListCrop(t, harness.repository, "crop_1", "scene_a")
	seedAssetListInputAsset(t, harness.repository, "owner-a", "in_1", "image/png", "image")
	edit := `{"title":"Cropped","expected_version":0,"items":[` +
		`{"kind":"character","name":"Knight","references":[` +
		`{"input_asset_id":"crop_1"},{"input_asset_id":"in_1"}]},` +
		`{"kind":"character","name":"Squire","references":[{"input_asset_id":"in_1"}]}]}`
	if response := harness.editDraft(t, list.ID, edit); response.Code != http.StatusOK {
		t.Fatalf("edit = %d %s", response.Code, response.Body.String())
	}
	read := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListItemRoute, harness.projectID, list.ID), "", "")
	if read.Code != http.StatusOK {
		t.Fatalf("read = %d %s", read.Code, read.Body.String())
	}
	wire := decodeAssetListTestWire(t, read)
	if len(wire.Items) != 2 || wire.Items[0].Crop == nil {
		t.Fatalf("items = %+v", wire.Items)
	}
	provenance, found, err := harness.repository.InputAssetCropRepository().Get(
		context.Background(), generation.OwnerID("owner-a"), generation.AssetID("crop_1"))
	if err != nil || !found {
		t.Fatalf("crop provenance found=%v err=%v", found, err)
	}
	crop := wire.Items[0].Crop
	if crop.SourceInputAssetID != string(provenance.SourceInputAssetID) ||
		crop.SourceKind != provenance.SourceKind ||
		crop.SourceWidth != provenance.SourceWidth || crop.SourceHeight != provenance.SourceHeight {
		t.Fatalf("crop source = %+v; provenance = %+v", crop, provenance)
	}
	if crop.Region.X != provenance.Region.X || crop.Region.Y != provenance.Region.Y ||
		crop.Region.Width != provenance.Region.Width || crop.Region.Height != provenance.Region.Height {
		t.Fatalf("crop region = %+v; provenance = %+v", crop.Region, provenance.Region)
	}
	if crop.PixelRect.X0 != provenance.PixelRect.X0 || crop.PixelRect.Y0 != provenance.PixelRect.Y0 ||
		crop.PixelRect.X1 != provenance.PixelRect.X1 || crop.PixelRect.Y1 != provenance.PixelRect.Y1 {
		t.Fatalf("crop pixel rect = %+v; provenance = %+v", crop.PixelRect, provenance.PixelRect)
	}
	// An item whose references carry no provenance keeps the field absent.
	if wire.Items[1].Crop != nil {
		t.Fatalf("plain item crop = %+v", wire.Items[1].Crop)
	}
}

// #331: stored data may carry several crop references on one item; the last
// one wins, matching the edit rule that replaces the previous crop with the
// new one appended last. A foreign owner's crop and a list_crop asset without
// provenance never produce a crop object.
func TestProjectAssetListDetailPicksLastCropAndSkipsUnprovenanced(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID,
		"Draft", defaultListItem())
	seedAssetListCrop(t, harness.repository, "crop_1", "scene_a")
	seedAssetListCrop(t, harness.repository, "crop_2", "scene_a")
	// A list_crop row without provenance is inconsistent stored data.
	seedAssetListInputAsset(t, harness.repository, "owner-a", "crop_orphan", "image/png", "list_crop")
	// Another owner's crop with full provenance must stay invisible.
	seedAssetListInputAsset(t, harness.repository, "owner-b", "crop_foreign", "image/png", "list_crop")
	if _, _, err := harness.repository.InputAssetCropRepository().InsertOrGet(
		context.Background(), listcrop.Crop{
			AssetID:            generation.AssetID("crop_foreign"),
			OwnerID:            generation.OwnerID("owner-b"),
			SourceAssetID:      generation.AssetID("scene_b"),
			SourceKind:         listcrop.SourceKindInput,
			SourceInputAssetID: generation.AssetID("scene_b"),
			SourceWidth:        1000,
			SourceHeight:       1000,
			Region:             listcrop.Region{X: 0, Y: 0, Width: 0.5, Height: 0.5},
			PixelRect:          listcrop.PixelRect{X0: 0, Y0: 0, X1: 500, Y1: 500},
			SHA256:             strings.Repeat("c", 64),
			CreatedAt:          time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC),
		}); err != nil {
		t.Fatalf("seed foreign crop: %v", err)
	}
	// The seed service has no reference port, so it stores references the
	// public edit path would refuse; the read path must still refuse them.
	if _, err := harness.seed.Edit(context.Background(), assetlist.EditInput{
		Owner: "owner-a", ProjectID: harness.projectID, ListID: list.ID,
		ExpectedVersion: 0, Title: "Stored",
		Items: []assetlist.ItemInput{
			{Kind: "character", Name: "Knight", References: []project.Reference{
				{InputAssetID: "crop_1"}, {InputAssetID: "crop_2"}}},
			{Kind: "character", Name: "Squire", References: []project.Reference{
				{InputAssetID: "crop_orphan"}, {InputAssetID: "crop_foreign"}}},
		},
		SessionID: harness.sessionID,
	}); err != nil {
		t.Fatalf("seed stored references: %v", err)
	}
	read := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListItemRoute, harness.projectID, list.ID), "", "")
	if read.Code != http.StatusOK {
		t.Fatalf("read = %d %s", read.Code, read.Body.String())
	}
	wire := decodeAssetListTestWire(t, read)
	if len(wire.Items) != 2 || wire.Items[0].Crop == nil {
		t.Fatalf("items = %+v", wire.Items)
	}
	// crop_2's stored rect starts at x0=500; crop_1's starts at 0.
	if wire.Items[0].Crop.PixelRect.X0 != 500 ||
		wire.Items[0].Crop.SourceInputAssetID != "scene_a" {
		t.Fatalf("last crop = %+v", wire.Items[0].Crop)
	}
	if wire.Items[1].Crop != nil {
		t.Fatalf("unprovenanced crop = %+v", wire.Items[1].Crop)
	}
	if strings.Contains(read.Body.String(), "scene_b") {
		t.Fatalf("foreign crop source leaked: %s", read.Body.String())
	}
}

// #331: the summary list stays small; only the detail carries crop provenance.
func TestProjectAssetListSummaryOmitsCropProvenance(t *testing.T) {
	t.Parallel()
	harness := newAssetListHTTPHarness(t)
	list := harness.propose(t, "owner-a", harness.projectID, harness.sessionID,
		"Draft", defaultListItem())
	seedAssetListCrop(t, harness.repository, "crop_1", "scene_a")
	edit := `{"title":"Cropped","expected_version":0,"items":[` +
		`{"kind":"character","name":"Knight","references":[{"input_asset_id":"crop_1"}]}]}`
	if response := harness.editDraft(t, list.ID, edit); response.Code != http.StatusOK {
		t.Fatalf("edit = %d %s", response.Code, response.Body.String())
	}
	detail := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListItemRoute, harness.projectID, list.ID), "", "")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"crop"`) {
		t.Fatalf("detail = %d %s", detail.Code, detail.Body.String())
	}
	summary := grantTestRequest(t, harness.handler, harness.cookie, http.MethodGet,
		assetListRoute(assetListProjectRoute, harness.projectID, ""), "", "")
	if summary.Code != http.StatusOK {
		t.Fatalf("summary = %d %s", summary.Code, summary.Body.String())
	}
	if strings.Contains(summary.Body.String(), `"crop"`) ||
		strings.Contains(summary.Body.String(), `"pixel_rect"`) {
		t.Fatalf("summary leaked crop provenance: %s", summary.Body.String())
	}
}
