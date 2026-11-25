package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

// seedRetiredConsentState writes the rows the retired conversational consent
// state machine (#491) persisted, as an older version left them behind.
func seedRetiredConsentState(t *testing.T, repository interface {
	SaveAgentState(ownerID, scope, key string, value json.RawMessage) error
}, owner, session string) {
	t.Helper()
	for key, value := range map[string]string{
		agentparams.ConsentPendingKey:   `{"reason":"first"}`,
		agentparams.ConsentRequestIDKey: `"retired-request"`,
		agentparams.ConsentDecisionKey:  `"pending"`,
		agentparams.ConsentApprovedKey:  `false`,
		agentparams.ConsentSnapshotKey:  `{"ai_model":"model-a"}`,
	} {
		if err := repository.SaveAgentState(owner, session, key, json.RawMessage(value)); err != nil {
			t.Fatal(err)
		}
	}
}

// With no execution grant the consent routes have nothing to read or decide:
// a pending consent row an older version persisted is neither shown nor
// decided, and a decision about it is the not-pending conflict (#491).
func TestConsentHTTPHasNoStoredDecisionToReadOrDecide(t *testing.T) {
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
	seedRetiredConsentState(t, repository, string(owner), "session")
	get := httptest.NewRecorder()
	server.agentConsentGet(get, httptest.NewRequest(http.MethodGet, "/api/v1/agent/consent?scope=session", nil), owner)
	if get.Code != http.StatusOK || strings.TrimSpace(get.Body.String()) != `{"consent":{}}` {
		t.Fatalf("GET showed retired consent state: status=%d body=%s", get.Code, get.Body.String())
	}
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"approved":true}`, http.StatusBadRequest},
		{`{"request_id":"retired-request"}`, http.StatusBadRequest},
		{`{"approved":true,"request_id":"retired-request"}`, http.StatusConflict},
	} {
		response := httptest.NewRecorder()
		server.agentConsentPut(response, httptest.NewRequest(http.MethodPut,
			"/api/v1/agent/consent?scope=session", strings.NewReader(tc.body)), owner)
		if response.Code != tc.status {
			t.Fatalf("PUT %s status=%d want=%d body=%s", tc.body, response.Code, tc.status, response.Body.String())
		}
	}
	if got := string(state.Get(scope)[agentparams.ConsentDecisionKey]); got != `"pending"` {
		t.Fatalf("a decision rewrote the retired consent row: %s", got)
	}
}

func TestAgentParamsPutCannotForgeOrEraseRetiredConsentRows(t *testing.T) {
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
	for _, key := range []string{agentparams.ConsentPendingKey, agentparams.ConsentDecisionKey,
		agentparams.ConsentRequestIDKey, agentparams.ConsentSnapshotKey} {
		if state.Get(scope)[key] != nil {
			t.Fatalf("ordinary params wrote the retired consent key %s", key)
		}
	}
	// A row an older version persisted is private state: parameters can neither
	// replace nor erase it, and it stays out of every parameter projection.
	seedRetiredConsentState(t, repository, string(owner), "session")
	request := httptest.NewRequest(http.MethodPut, "/api/v1/agent/params?scope=session",
		strings.NewReader(`{"params":{"consent_decision":"approved","consent_pending":null,"size":"small"}}`))
	response := httptest.NewRecorder()
	server.agentParamsPut(response, request, owner)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "consent_") {
		t.Fatalf("params PUT over retired rows: status=%d body=%s", response.Code, response.Body.String())
	}
	if got := string(state.Get(scope)[agentparams.ConsentDecisionKey]); got != `"pending"` {
		t.Fatalf("params overwrote the retired decision row: %s", got)
	}
	if state.Get(scope)[agentparams.ConsentPendingKey] == nil {
		t.Fatal("params erased the retired pending row")
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
	module, err := generation.New(generation.Dependencies{Conversions: generation.NoConversions, Repository: repository, Catalog: taskCatalog, Assets: assets, Providers: providers.NewRegistry()})
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

// #586: the count is the Agent's call now. The panel cannot write it, and an
// image_count row an older server stored for the session is ignored: every
// Offering reports its own declared default and nothing fails.
func TestAgentParamsIgnoreTheRetiredSessionImageCount(t *testing.T) {
	t.Parallel()
	repository, err := OpenMigratedTestRepository(filepath.Join(t.TempDir(), "count.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err := repository.SaveAgentState("owner", "session", "image_count", json.RawMessage(`4`)); err != nil {
		t.Fatal(err)
	}
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	assets, err := local.New(filepath.Join(t.TempDir(), "assets"))
	if err != nil {
		t.Fatal(err)
	}
	module, err := generation.New(generation.Dependencies{Conversions: generation.NoConversions, Repository: repository, Catalog: taskCatalog, Assets: assets, Providers: providers.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	store := agentparams.NewPersistentParameterStore(repository, nil)
	server := &Server{generation: module, agentParams: store, ownsAgentSession: func(_ context.Context, owner, scope string) bool { return owner == "owner" && scope == "session" }}
	if _, leaked := store.Get(agentparams.ScopeKey("owner", "session"))["image_count"]; leaked {
		t.Fatal("the retired image_count row reached the session parameters")
	}
	for _, input := range []struct{ offeringID, parameter string }{
		{"openai/gpt-image-2/text-to-image", "n"},
		{"jimeng/seedream-4-5/text-to-image", "max_images"},
	} {
		response := httptest.NewRecorder()
		server.agentParamsGet(response, httptest.NewRequest(http.MethodGet,
			"/api/v1/agent/params?scope=session&offering_id="+url.QueryEscape(input.offeringID), nil), "owner")
		var result struct {
			Params map[string]json.RawMessage            `json:"params"`
			States map[string]agentparams.ParameterState `json:"parameter_states"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("get %s: %d %s", input.offeringID, response.Code, response.Body.String())
		}
		if got, exists := result.Params[input.parameter]; exists {
			t.Fatalf("%s.%s = %s from the retired session count", input.offeringID, input.parameter, got)
		}
		if state := result.States[input.parameter]; state.Source != "default" || string(state.Value) != "1" {
			t.Fatalf("%s.%s state = %+v, want the declared default", input.offeringID, input.parameter, state)
		}
	}
	response := httptest.NewRecorder()
	server.agentParamsPut(response, httptest.NewRequest(http.MethodPut,
		"/api/v1/agent/params?scope=session&offering_id="+url.QueryEscape("openai/gpt-image-2/text-to-image"),
		strings.NewReader(`{"params":{"n":4}}`)), "owner")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("the panel still writes the count: %d %s", response.Code, response.Body.String())
	}
}
