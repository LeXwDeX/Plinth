package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func grantTestBrowser(t *testing.T) (http.Handler, *http.Cookie, string) {
	_, handler, cookie, session := grantTestBrowserWithApplication(t, nil, []string{"user"})
	return handler, cookie, session
}

func grantTestBrowserWithApplication(t *testing.T, customize func(*catalog.ApplicationSchema), roles []string) (*generation.Module, http.Handler, *http.Cookie, string) {
	return grantTestBrowserWithCatalog(t, customize, roles, nil)
}

func grantTestBrowserWithCatalog(t *testing.T, customize func(*catalog.ApplicationSchema), roles []string,
	source *catalog.Catalog, databaseSetups ...func(*testing.T, string),
) (*generation.Module, http.Handler, *http.Cookie, string) {
	t.Helper()
	record := authentication.UserRecord{
		ID: "owner-a", Username: "grant-user",
		PasswordHash: "$2b$04$UVs2NmHqVLnDFtkcPXeOhuCLZNX27fLFYc/TbHf7w5wkWsWZXCP2.",
		Nickname:     "Grant User", Email: "grant@example.test", Active: true,
	}
	directory := &authDirectory{
		byUsername: map[string]authentication.UserRecord{record.Username: record},
		byID:       map[string]authentication.UserRecord{record.ID: record},
	}
	module, handler, _, _ := newHTTPHarnessWithCatalogOverride(t, roles, customize,
		func(config *authentication.Config) { config.Directory = directory }, nil, nil, source, databaseSetups...)
	login := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"grant-user","password":"correct-horse"}`))
	request.Header.Set("Origin", "http://example.com")
	handler.ServeHTTP(login, request)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login status=%d body=%s cookies=%d", login.Code, login.Body.String(), len(login.Result().Cookies()))
	}
	cookie := login.Result().Cookies()[0]
	session := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/chat/sessions", `{"title":"grant test"}`, "")
	if session.Code != http.StatusCreated {
		t.Fatalf("create session status=%d body=%s", session.Code, session.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(session.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("session=%s err=%v", session.Body.String(), err)
	}
	return module, handler, cookie, created.ID
}

func grantTestRequest(t *testing.T, handler http.Handler, cookie *http.Cookie, method, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://example.com")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	handler.ServeHTTP(response, request)
	return response
}

func TestTaskExecutionRequiresSeparateHumanDecisionAndExactPreparedReplay(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	prepare := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/prepare",
		`{"session_id":"`+sessionID+`","capability":"text-to-3d","offering_id":"meshy/meshy-7.1/text-to-3d","input":{"prompt":"a carved oak chair"}}`, "")
	if prepare.Code != http.StatusCreated {
		t.Fatalf("prepare status=%d body=%s", prepare.Code, prepare.Body.String())
	}
	var prepared struct {
		ID string `json:"prepared_input_id"`
	}
	if err := json.Unmarshal(prepare.Body.Bytes(), &prepared); err != nil || prepared.ID == "" {
		t.Fatalf("prepared=%s err=%v", prepare.Body.String(), err)
	}
	grant := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/grants",
		`{"prepared_input_id":"`+prepared.ID+`"}`, "grant-key")
	if grant.Code != http.StatusCreated {
		t.Fatalf("grant status=%d body=%s", grant.Code, grant.Body.String())
	}
	var requested struct {
		ID     string `json:"request_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(grant.Body.Bytes(), &requested); err != nil || requested.ID == "" || requested.Status != "pending" {
		t.Fatalf("grant=%s err=%v", grant.Body.String(), err)
	}
	submitBody := `{"prepared_input_id":"` + prepared.ID + `","authorization_request_id":"` + requested.ID + `"}`
	before := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks", submitBody, "grant-key")
	if before.Code != http.StatusForbidden {
		t.Fatalf("pending grant accepted: %d %s", before.Code, before.Body.String())
	}
	decision := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/tasks/grants/"+requested.ID,
		`{"decision":"approve"}`, "")
	if decision.Code != http.StatusOK || !strings.Contains(decision.Body.String(), `"status":"approved"`) {
		t.Fatalf("approve status=%d body=%s", decision.Code, decision.Body.String())
	}
	decisionRetry := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/tasks/grants/"+requested.ID,
		`{"decision":"approve"}`, "")
	if decisionRetry.Code != http.StatusOK {
		t.Fatalf("lost decision response could not be retried: %d %s", decisionRetry.Code, decisionRetry.Body.String())
	}
	wrongKey := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks", submitBody, "changed-key")
	if wrongKey.Code != http.StatusForbidden {
		t.Fatalf("changed key accepted: %d %s", wrongKey.Code, wrongKey.Body.String())
	}
	accepted := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks", submitBody, "grant-key")
	if accepted.Code != http.StatusAccepted || !strings.Contains(accepted.Body.String(), `"replayed":false`) {
		t.Fatalf("submit status=%d body=%s", accepted.Code, accepted.Body.String())
	}
	replay := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks", submitBody, "grant-key")
	if replay.Code != http.StatusAccepted || !strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	boundDecisionRetry := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/tasks/grants/"+requested.ID,
		`{"decision":"approve"}`, "")
	if boundDecisionRetry.Code != http.StatusOK || !strings.Contains(boundDecisionRetry.Body.String(), `"status":"bound"`) {
		t.Fatalf("bound decision replay status=%d body=%s", boundDecisionRetry.Code, boundDecisionRetry.Body.String())
	}
}

func TestTaskExecutionGrantRejectsBareAndCredentialOnlyRequests(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	bare := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks",
		`{"session_id":"`+sessionID+`","capability":"text-to-3d","offering_id":"meshy/meshy-7.1/text-to-3d","input":{"prompt":"chair"}}`, "")
	if bare.Code != http.StatusForbidden || !strings.Contains(bare.Body.String(), "execution_grant_required") {
		t.Fatalf("bare submit status=%d body=%s", bare.Code, bare.Body.String())
	}
	legacy := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/meshy/text-to-3d",
		`{"prompt":"chair"}`, "")
	if legacy.Code != http.StatusForbidden {
		t.Fatalf("legacy submit status=%d body=%s", legacy.Code, legacy.Body.String())
	}
	noSession := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/prepare",
		`{"capability":"text-to-3d","offering_id":"meshy/meshy-7.1/text-to-3d","input":{"prompt":"chair"}}`, "")
	if noSession.Code != http.StatusBadRequest {
		t.Fatalf("missing session status=%d body=%s", noSession.Code, noSession.Body.String())
	}
	credentialOnly := grantTestRequest(t, handler, nil, http.MethodPost, "/api/v1/tasks/grants",
		`{"prepared_input_id":"prepared_forged"}`, "grant-key")
	if credentialOnly.Code != http.StatusForbidden {
		t.Fatalf("fallback identity created grant: %d %s", credentialOnly.Code, credentialOnly.Body.String())
	}
	decision := grantTestRequest(t, handler, nil, http.MethodPut, "/api/v1/tasks/grants/eg_forged",
		`{"decision":"approve"}`, "")
	if decision.Code != http.StatusForbidden {
		t.Fatalf("fallback identity approved grant: %d %s", decision.Code, decision.Body.String())
	}
}

func TestAgentConsentProjectsAndDecidesDurableExecutionGrant(t *testing.T) {
	t.Parallel()
	module, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	turnResponse := grantTestRequest(t, handler, cookie, http.MethodPost,
		"/api/chat/sessions/"+sessionID+"/messages", `{"content":"make an oak chair"}`, "")
	if turnResponse.Code != http.StatusAccepted {
		t.Fatalf("start turn status=%d body=%s", turnResponse.Code, turnResponse.Body.String())
	}
	var turn struct {
		AssistantID string `json:"messageId"`
	}
	if err := json.Unmarshal(turnResponse.Body.Bytes(), &turn); err != nil || turn.AssistantID == "" {
		t.Fatalf("turn=%s err=%v", turnResponse.Body.String(), err)
	}
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	prepared, err := module.PrepareInput(context.Background(), generation.PrepareInputCommand{
		OwnerID: "owner-a", SessionID: sessionID, Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: json.RawMessage(`{"prompt":"an oak chair"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := repository.InsertAgentOperation(context.Background(), generationtool.Operation{
		OwnerID: "owner-a", SessionID: sessionID, AssistantMessageID: turn.AssistantID,
		CallID: "tool-call-1", ToolName: "create_model_3d", CommandHash: "command-hash-1",
		IdempotencyKey: "agent-fixture-key-1", PreparedInputID: prepared.ID,
		PreparedInputHash: prepared.InputHash, OfferingID: prepared.OfferingID,
		OfferingRevision: prepared.OfferingRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	grants := executiongrant.Service{Grants: repository, Prepared: repository}
	grant, err := grants.Prepare(context.Background(), executiongrant.PrepareCommand{
		Principal: executiongrant.Principal{OwnerID: "owner-a", ActorID: "agent-planner", Source: executiongrant.AgentPending},
		SessionID: sessionID, OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision,
		PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash,
		IdempotencyKey: operation.IdempotencyKey, OperationID: operation.OperationID,
		TriggerTurnID: turn.AssistantID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AttachAgentGrantRequest(context.Background(), operation, grant.RequestID); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/agent/consent?scope=" + sessionID
	get := grantTestRequest(t, handler, cookie, http.MethodGet, path, "", "")
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), grant.RequestID) ||
		!strings.Contains(get.Body.String(), `"consent_decision":"pending"`) ||
		!strings.Contains(get.Body.String(), `"prompt":"an oak chair"`) ||
		!strings.Contains(get.Body.String(), `"offering_revision":"`+prepared.OfferingRevision+`"`) ||
		!strings.Contains(get.Body.String(), `"capability":"text-to-3d"`) ||
		strings.Contains(get.Body.String(), operation.CommandHash) {
		t.Fatalf("pending projection status=%d body=%s", get.Code, get.Body.String())
	}
	stale := grantTestRequest(t, handler, cookie, http.MethodPut, path,
		`{"request_id":"eg_stale","approved":true}`, "")
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale decision status=%d body=%s", stale.Code, stale.Body.String())
	}
	noCookie := grantTestRequest(t, handler, nil, http.MethodPut, path,
		`{"request_id":"`+grant.RequestID+`","approved":true}`, "")
	if noCookie.Code != http.StatusForbidden {
		t.Fatalf("credential-only decision status=%d body=%s", noCookie.Code, noCookie.Body.String())
	}
	approved := grantTestRequest(t, handler, cookie, http.MethodPut, path,
		`{"request_id":"`+grant.RequestID+`","approved":true}`, "")
	if approved.Code != http.StatusOK {
		t.Fatalf("approved status=%d body=%s", approved.Code, approved.Body.String())
	}
	get = grantTestRequest(t, handler, cookie, http.MethodGet, path, "", "")
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"consent_decision":"approved"`) {
		t.Fatalf("approved projection status=%d body=%s", get.Code, get.Body.String())
	}
	accepted, err := module.SubmitPrepared(context.Background(), generation.SubmitPreparedCommand{
		OwnerID: "owner-a", SessionID: sessionID, PreparedInputID: prepared.ID,
		IdempotencyKey: operation.IdempotencyKey, OperationID: operation.OperationID,
		AuthorizationRef: grant.RequestID,
	})
	if err != nil || accepted.TaskID == "" {
		t.Fatalf("bind approved task: %+v %v", accepted, err)
	}
	get = grantTestRequest(t, handler, cookie, http.MethodGet, path, "", "")
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"consent":{}`) {
		t.Fatalf("bound grant remained a pending consent: status=%d body=%s", get.Code, get.Body.String())
	}
	late := grantTestRequest(t, handler, cookie, http.MethodPut, path,
		`{"request_id":"`+grant.RequestID+`","approved":true}`, "")
	if late.Code != http.StatusConflict {
		t.Fatalf("bound grant allowed late decision: status=%d body=%s", late.Code, late.Body.String())
	}
}
