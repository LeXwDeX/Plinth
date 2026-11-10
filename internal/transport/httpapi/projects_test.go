package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/project"
)

func decodeProjectResponse(t *testing.T, body string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("project body %q: %v", body, err)
	}
	return decoded
}

func projectIDFrom(t *testing.T, body string) string {
	t.Helper()
	decoded := decodeProjectResponse(t, body)
	id, _ := decoded["id"].(string)
	if id == "" {
		t.Fatalf("project id missing from %s", body)
	}
	return id
}

func TestProjectCRUDIsOwnerScopedAndAudited(t *testing.T) {
	t.Parallel()
	module, handler, cookie, _ := grantTestBrowserWithApplication(t, nil, []string{"user"})
	created := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/projects", `{"name":"  Star  "}`, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create project = %d %s", created.Code, created.Body.String())
	}
	projectID := projectIDFrom(t, created.Body.String())
	if decodeProjectResponse(t, created.Body.String())["name"] != "Star" {
		t.Fatalf("created name = %s", created.Body.String())
	}
	listed := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/v1/projects", "", "")
	var list struct {
		Projects []map[string]any `json:"projects"`
	}
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &list) != nil || len(list.Projects) != 1 {
		t.Fatalf("list projects = %d %s", listed.Code, listed.Body.String())
	}
	read := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/v1/projects/"+projectID, "", "")
	if read.Code != http.StatusOK || decodeProjectResponse(t, read.Body.String())["id"] != projectID {
		t.Fatalf("read project = %d %s", read.Code, read.Body.String())
	}
	renamed := grantTestRequest(t, handler, cookie, http.MethodPatch, "/api/v1/projects/"+projectID, `{"name":"Renamed"}`, "")
	if renamed.Code != http.StatusOK || decodeProjectResponse(t, renamed.Body.String())["name"] != "Renamed" {
		t.Fatalf("rename project = %d %s", renamed.Code, renamed.Body.String())
	}

	value, _ := fixtureExecutions.Load(module)
	repository := value.(*fixtureExecutionState).repository
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	foreign := project.Project{ID: "foreign-project", OwnerID: "owner-b", Name: "Foreign", CreatedAt: now, UpdatedAt: now}
	if _, err := repository.ProjectRepository().CreateProject(context.Background(), foreign, 10); err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/projects/foreign-project", ""},
		{http.MethodPatch, "/api/v1/projects/foreign-project", `{"name":"Stolen"}`},
		{http.MethodDelete, "/api/v1/projects/foreign-project", ""},
		{http.MethodGet, "/api/v1/projects/foreign-project/style-guide", ""},
		{http.MethodGet, "/api/v1/projects/foreign-project/conventions", ""},
		{http.MethodGet, "/api/v1/projects/foreign-project/assets", ""},
	} {
		response := grantTestRequest(t, handler, cookie, request.method, request.path, request.body, "")
		if response.Code != http.StatusNotFound {
			t.Errorf("foreign %s %s = %d %s", request.method, request.path, response.Code, response.Body.String())
		}
	}
	deleted := grantTestRequest(t, handler, cookie, http.MethodDelete, "/api/v1/projects/"+projectID, "", "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete project = %d %s", deleted.Code, deleted.Body.String())
	}
	events, total, err := repository.QueryAdministrationAuditEvents(context.Background(), administration.AuditQuery{Page: 1, PageSize: 20})
	if err != nil || total < 3 {
		t.Fatalf("project audit events = %d err=%v", total, err)
	}
	found := map[string]bool{}
	for _, event := range events {
		found[event.Action] = true
	}
	for _, action := range []string{"create_project", "update_project", "delete_project"} {
		if !found[action] {
			t.Errorf("missing audit action %s in %+v", action, found)
		}
	}
}

func TestProjectStyleConventionsAndAssets(t *testing.T) {
	t.Parallel()
	handler, cookie, _ := grantTestBrowser(t)
	created := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/projects", `{"name":"Star"}`, "")
	projectID := projectIDFrom(t, created.Body.String())

	guide := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/v1/projects/"+projectID+"/style-guide", "", "")
	if guide.Code != http.StatusOK || decodeProjectResponse(t, guide.Body.String())["enabled"] != false {
		t.Fatalf("default guide = %d %s", guide.Code, guide.Body.String())
	}
	invalidPalette := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/projects/"+projectID+"/style-guide",
		`{"enabled":true,"text":"ink","palette":["red"],"references":[]}`, "")
	if invalidPalette.Code != http.StatusBadRequest {
		t.Fatalf("invalid palette = %d %s", invalidPalette.Code, invalidPalette.Body.String())
	}
	savedGuide := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/projects/"+projectID+"/style-guide",
		`{"enabled":true,"text":"ink","palette":["#112233"],"references":[]}`, "")
	if savedGuide.Code != http.StatusOK {
		t.Fatalf("save guide = %d %s", savedGuide.Code, savedGuide.Body.String())
	}
	readGuide := decodeProjectResponse(t, savedGuide.Body.String())
	if readGuide["enabled"] != true || readGuide["text"] != "ink" {
		t.Fatalf("saved guide = %s", savedGuide.Body.String())
	}

	conventions := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/projects/"+projectID+"/conventions",
		`{"brief":{"purpose":"game_character","style":"ink","triangle_budget":500},"texts":{"naming":"hero_"}}`, "")
	if conventions.Code != http.StatusOK {
		t.Fatalf("save conventions = %d %s", conventions.Code, conventions.Body.String())
	}
	readConventions := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/v1/projects/"+projectID+"/conventions", "", "")
	if readConventions.Code != http.StatusOK {
		t.Fatalf("read conventions = %d %s", readConventions.Code, readConventions.Body.String())
	}
	decoded := decodeProjectResponse(t, readConventions.Body.String())
	texts, _ := decoded["texts"].(map[string]any)
	if texts["naming"] != "hero_" {
		t.Fatalf("conventions texts = %s", readConventions.Body.String())
	}
	invalidConventions := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/projects/"+projectID+"/conventions",
		`{"brief":{"undeclared":"x"}}`, "")
	if invalidConventions.Code != http.StatusBadRequest {
		t.Fatalf("invalid conventions = %d %s", invalidConventions.Code, invalidConventions.Body.String())
	}

	asset := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/projects/"+projectID+"/assets",
		`{"kind":"character","name":"Hero","description":"lead","images":[],"models":[]}`, "")
	if asset.Code != http.StatusCreated {
		t.Fatalf("create asset = %d %s", asset.Code, asset.Body.String())
	}
	assetID := projectIDFrom(t, asset.Body.String())
	assets := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/v1/projects/"+projectID+"/assets", "", "")
	var listed struct {
		Assets []map[string]any `json:"assets"`
	}
	if assets.Code != http.StatusOK || json.Unmarshal(assets.Body.Bytes(), &listed) != nil || len(listed.Assets) != 1 {
		t.Fatalf("list assets = %d %s", assets.Code, assets.Body.String())
	}
	patched := grantTestRequest(t, handler, cookie, http.MethodPatch, "/api/v1/projects/"+projectID+"/assets/"+assetID,
		`{"name":"Renamed"}`, "")
	if patched.Code != http.StatusOK || decodeProjectResponse(t, patched.Body.String())["name"] != "Renamed" {
		t.Fatalf("patch asset = %d %s", patched.Code, patched.Body.String())
	}
	badKind := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/projects/"+projectID+"/assets",
		`{"kind":"unknown","name":"Bad"}`, "")
	if badKind.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind = %d %s", badKind.Code, badKind.Body.String())
	}
	promoted := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/projects/"+projectID+"/promote",
		`{"source":{"input_asset_id":"not-ours"},"kind":"character","name":"Stolen"}`, "")
	if promoted.Code != http.StatusBadRequest {
		t.Fatalf("foreign promote = %d %s", promoted.Code, promoted.Body.String())
	}
	deleted := grantTestRequest(t, handler, cookie, http.MethodDelete, "/api/v1/projects/"+projectID+"/assets/"+assetID, "", "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete asset = %d %s", deleted.Code, deleted.Body.String())
	}
}

func TestChatSessionBelongsToProject(t *testing.T) {
	t.Parallel()
	module, handler, cookie, _ := grantTestBrowserWithApplication(t, nil, []string{"user"})
	created := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/projects", `{"name":"Star"}`, "")
	projectID := projectIDFrom(t, created.Body.String())

	session := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/chat/sessions",
		`{"title":"project session","project_id":"`+projectID+`"}`, "")
	if session.Code != http.StatusCreated {
		t.Fatalf("create session = %d %s", session.Code, session.Body.String())
	}
	wire := decodeProjectResponse(t, session.Body.String())
	if wire["projectId"] != projectID || wire["styleLockSource"] != "project" {
		t.Fatalf("session project wire = %s", session.Body.String())
	}
	sessionID, _ := wire["id"].(string)

	source := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/chat/sessions/"+sessionID+"/style-lock",
		`{"enabled":true,"text":"ink","references":[],"source":"session"}`, "")
	if source.Code != http.StatusOK {
		t.Fatalf("set style source = %d %s", source.Code, source.Body.String())
	}
	styleLock, _ := decodeProjectResponse(t, source.Body.String())["styleLock"].(map[string]any)
	if styleLock["source"] != "session" {
		t.Fatalf("style lock source = %s", source.Body.String())
	}
	invalidSource := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/chat/sessions/"+sessionID+"/style-lock",
		`{"enabled":true,"text":"ink","references":[],"source":"bogus"}`, "")
	if invalidSource.Code != http.StatusBadRequest {
		t.Fatalf("invalid style source = %d %s", invalidSource.Code, invalidSource.Body.String())
	}

	second := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/projects", `{"name":"Second"}`, "")
	secondID := projectIDFrom(t, second.Body.String())
	assigned := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/chat/sessions/"+sessionID+"/project",
		`{"project_id":"`+secondID+`"}`, "")
	if assigned.Code != http.StatusOK || decodeProjectResponse(t, assigned.Body.String())["projectId"] != secondID {
		t.Fatalf("assign project = %d %s", assigned.Code, assigned.Body.String())
	}
	cleared := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/chat/sessions/"+sessionID+"/project",
		`{"project_id":""}`, "")
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear project = %d %s", cleared.Code, cleared.Body.String())
	}
	if _, present := decodeProjectResponse(t, cleared.Body.String())["projectId"]; present {
		t.Fatalf("cleared session still carries projectId: %s", cleared.Body.String())
	}

	value, _ := fixtureExecutions.Load(module)
	repository := value.(*fixtureExecutionState).repository
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	if _, err := repository.ProjectRepository().CreateProject(context.Background(), project.Project{
		ID: "foreign-project", OwnerID: "owner-b", Name: "Foreign", CreatedAt: now, UpdatedAt: now,
	}, 10); err != nil {
		t.Fatal(err)
	}
	foreign := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/chat/sessions/"+sessionID+"/project",
		`{"project_id":"foreign-project"}`, "")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign project assignment = %d %s", foreign.Code, foreign.Body.String())
	}
	foreignCreate := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/chat/sessions",
		`{"title":"foreign","project_id":"foreign-project"}`, "")
	if foreignCreate.Code != http.StatusNotFound {
		t.Fatalf("foreign project session = %d %s", foreignCreate.Code, foreignCreate.Body.String())
	}
}
