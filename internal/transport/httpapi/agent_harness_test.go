package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

func testConsentRequestID(t *testing.T, store *agentparams.Store, scope string) string {
	t.Helper()
	var requestID string
	if err := json.Unmarshal(store.Get(scope)[agentparams.ConsentRequestIDKey], &requestID); err != nil || requestID == "" {
		t.Fatalf("missing consent request ID: %q, %v", requestID, err)
	}
	return requestID
}

func TestConsentHTTPRejectsMissingAndStaleRequestIdentity(t *testing.T) {
	t.Parallel()
	repository, err := OpenMigratedTestRepository(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	state := agentparams.NewPersistentStore(repository, nil)
	server := &Server{agentState: state, ownsAgentSession: func(context.Context, string, string) bool { return true }}
	owner := generation.OwnerID("owner")
	scope := agentparams.ScopeKey(string(owner), "session")
	snapshot := json.RawMessage(`{"ai_model":"model-a"}`)
	if err := state.BeginConsent(scope, json.RawMessage(`{"reason":"first"}`), snapshot); err != nil {
		t.Fatal(err)
	}
	oldID := testConsentRequestID(t, state, scope)
	if err := state.BeginConsent(scope, json.RawMessage(`{"reason":"second"}`), snapshot); err != nil {
		t.Fatal(err)
	}
	currentID := testConsentRequestID(t, state, scope)
	get := httptest.NewRecorder()
	server.agentConsentGet(get, httptest.NewRequest(http.MethodGet, "/api/v1/agent/consent?scope=session", nil), owner)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), currentID) {
		t.Fatalf("GET omitted current consent identity: status=%d body=%s", get.Code, get.Body.String())
	}
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"approved":true}`, http.StatusBadRequest},
		{`{"request_id":"` + currentID + `"}`, http.StatusBadRequest},
		{`{"approved":true,"request_id":"` + oldID + `"}`, http.StatusConflict},
	} {
		response := httptest.NewRecorder()
		server.agentConsentPut(response, httptest.NewRequest(http.MethodPut,
			"/api/v1/agent/consent?scope=session", strings.NewReader(tc.body)), owner)
		if response.Code != tc.status {
			t.Fatalf("PUT %s status=%d want=%d body=%s", tc.body, response.Code, tc.status, response.Body.String())
		}
	}
	if _, allowed, err := state.ClaimConsent(scope, "call-b", "", snapshot); err != nil || allowed {
		t.Fatalf("stale HTTP decision approved current request: allowed=%v err=%v", allowed, err)
	}
}

func TestBindingNewVersionPreservesSessionHarness(t *testing.T) {
	t.Parallel()
	server := &Server{
		agentParams: agentparams.NewStore(),
		agentState:  agentparams.NewStore(),
		ownsAgentSession: func(context.Context, string, string) bool {
			return true
		},
	}
	owner := generation.OwnerID("owner")
	scope := agentparams.ScopeKey(string(owner), "session")
	bind := func(task string) {
		t.Helper()
		body, err := json.Marshal(map[string]string{"session": "session", "task": task})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPut, "/api/v1/agent/bind", strings.NewReader(string(body)))
		response := httptest.NewRecorder()
		server.agentBindPut(response, request, owner)
		if response.Code != http.StatusOK {
			t.Fatalf("bind %q status = %d body=%s", task, response.Code, response.Body.String())
		}
	}
	bind("first")
	server.agentParams.SetUser(scope, map[string]json.RawMessage{"size": json.RawMessage(`"large"`)})
	bind("first")
	if got := string(server.agentParams.Get(scope)["size"]); got != `"large"` {
		t.Fatalf("same task lost harness size: %s", got)
	}
	bind("second")
	if got := string(server.agentParams.Get(scope)["size"]); got != `"large"` {
		t.Fatalf("new version lost session harness size: %s", got)
	}
}

func TestAgentParamsPutCannotForgeOrEraseConsent(t *testing.T) {
	t.Parallel()
	repository, err := OpenMigratedTestRepository(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	params := agentparams.NewPersistentParameterStore(repository, nil)
	state := agentparams.NewPersistentStore(repository, nil)
	server := &Server{
		agentParams: params, agentState: state,
		ownsAgentSession: func(context.Context, string, string) bool { return true },
	}
	owner := generation.OwnerID("owner")
	scope := agentparams.ScopeKey(string(owner), "session")
	snapshot := json.RawMessage(`{"ai_model":"model-a","params_hash":""}`)
	for _, payload := range []string{
		`{"params":{"consent_pending":{"reason":"forged"},"consent_request_id":"forged","consent_decision":"approved","consent_snapshot":{"ai_model":"model-a","params_hash":""},"size":"large"}}`,
		`{"params":{"consent_pending":null,"consent_decision":null,"consent_claim":null,"size":"medium"}}`,
	} {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/agent/params?scope=session", strings.NewReader(payload))
		response := httptest.NewRecorder()
		server.agentParamsPut(response, request, owner)
		if response.Code != http.StatusOK {
			t.Fatalf("params PUT status=%d body=%s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "consent_") {
			t.Fatalf("params response exposed private consent state: %s", response.Body.String())
		}
	}
	if state.Get(scope)[agentparams.ConsentPendingKey] != nil {
		t.Fatal("ordinary params created pending consent")
	}
	if _, allowed, err := state.ClaimConsent(scope, "call-a", "", snapshot); err != nil || allowed {
		t.Fatalf("forged approval allowed claim: allowed=%v err=%v", allowed, err)
	}
	if err := state.BeginConsent(scope, json.RawMessage(`{"reason":"paid"}`), snapshot); err != nil {
		t.Fatal(err)
	}
	if decided, err := state.DecideConsent(scope, testConsentRequestID(t, state, scope), true); err != nil || !decided {
		t.Fatalf("approve: decided=%v err=%v", decided, err)
	}
	if _, allowed, err := state.ClaimConsent(scope, "call-a", "", snapshot); err != nil || !allowed {
		t.Fatalf("claim: allowed=%v err=%v", allowed, err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/agent/params?scope=session", strings.NewReader(`{"params":{"consent_claim":null,"consent_decision":"approved"}}`))
	response := httptest.NewRecorder()
	server.agentParamsPut(response, request, owner)
	if response.Code != http.StatusOK {
		t.Fatalf("params PUT after claim status=%d", response.Code)
	}
	if _, allowed, err := state.ClaimConsent(scope, "call-b", "", snapshot); err != nil || allowed {
		t.Fatalf("private claim erased or overwritten: allowed=%v err=%v", allowed, err)
	}
}

func TestAgentParamsOfferingHTTPIsolationAndLegacyCompatibility(t *testing.T) {
	t.Parallel()
	repository, err := OpenMigratedTestRepository(filepath.Join(t.TempDir(), "params.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	assets, err := local.New(filepath.Join(t.TempDir(), "assets"))
	if err != nil {
		t.Fatal(err)
	}
	module, err := generation.New(generation.Dependencies{Repository: repository, Catalog: taskCatalog, Assets: assets, Providers: providers.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{generation: module, agentParams: agentparams.NewPersistentParameterStore(nil, nil), ownsAgentSession: func(_ context.Context, owner, scope string) bool { return owner == "owner" && scope == "session" }}
	put := func(query, body string, owner generation.OwnerID) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		server.agentParamsPut(response, httptest.NewRequest(http.MethodPut, "/api/v1/agent/params?scope=session"+query, strings.NewReader(body)), owner)
		return response
	}
	for _, input := range []struct{ query, body string }{
		{"", `{"params":{"background":"legacy"}}`},
		{"&offering_id=openai%2Fgpt-image-2%2Ftext-to-image", `{"params":{"background":"transparent"}}`},
		{"&offering_id=openai%2Fgpt-image-2%2Fimage-to-image", `{"params":{"background":"opaque"}}`},
	} {
		response := put(input.query, input.body, "owner")
		if response.Code != http.StatusOK {
			t.Fatalf("put: %d %s", response.Code, response.Body.String())
		}
	}
	for _, input := range []struct{ query, want string }{{"", "legacy"}, {"&offering_id=openai%2Fgpt-image-2%2Ftext-to-image", "transparent"}, {"&offering_id=openai%2Fgpt-image-2%2Fimage-to-image", "opaque"}} {
		response := httptest.NewRecorder()
		server.agentParamsGet(response, httptest.NewRequest(http.MethodGet, "/api/v1/agent/params?scope=session"+input.query, nil), "owner")
		var result struct {
			Params map[string]string `json:"params"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result.Params) != 1 || result.Params["background"] != input.want {
			t.Fatalf("get %s: %s %v", input.query, response.Body.String(), err)
		}
	}
	for _, payload := range []string{`{"params":{"quality":"low"}}`, `{"params":{"unknown":1}}`, `{"params":{"background":"invalid"}}`} {
		if got := put("&offering_id=openai%2Fgpt-image-2%2Ftext-to-image", payload, "owner"); got.Code != http.StatusBadRequest {
			t.Fatalf("invalid patch accepted: %s", got.Body.String())
		}
	}
	clear := put("&offering_id=openai%2Fgpt-image-2%2Ftext-to-image", `{"params":{"background":null}}`, "owner")
	var cleared struct {
		Params map[string]json.RawMessage            `json:"params"`
		States map[string]agentparams.ParameterState `json:"parameter_states"`
	}
	if err := json.Unmarshal(clear.Body.Bytes(), &cleared); err != nil || cleared.Params["background"] != nil || cleared.States["background"].Source != "default" {
		t.Fatalf("auto retained legacy pin: %s %v", clear.Body.String(), err)
	}
	if denied := put("&offering_id=openai%2Fgpt-image-2%2Ftext-to-image", `{"params":{"background":"attacker"}}`, "other"); denied.Code != http.StatusForbidden {
		t.Fatalf("cross owner write: %d", denied.Code)
	}
	response := httptest.NewRecorder()
	server.agentParamsGet(response, httptest.NewRequest(http.MethodGet, "/api/v1/agent/params?scope=session&offering_id=openai%2Fgpt-image-2%2Ftext-to-image", nil), "other")
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross owner read: %d", response.Code)
	}
	// Closed persistence must fail the write instead of acknowledging a memory-only pin.
	server.agentParams = agentparams.NewPersistentParameterStore(repository, nil)
	if err = repository.Close(); err != nil {
		t.Fatal(err)
	}
	failed := put("&offering_id=openai%2Fgpt-image-2%2Ftext-to-image", `{"params":{"background":"opaque"}}`, "owner")
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("failed persistence status=%d body=%s", failed.Code, failed.Body.String())
	}
}
