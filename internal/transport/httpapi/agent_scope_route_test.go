package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

func TestAgentReadRoutesRequireOwnedSession(t *testing.T) {
	t.Parallel()
	policy, err := access.NewPolicy(access.PolicyConfig{
		DefaultRole: "user", AdminRole: "admin",
		Roles: []access.RoleDefinition{{Key: "user"}, {Key: "admin"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	server := &Server{
		identity:    DevelopmentIdentity{DefaultClaims: access.Claims{Roles: []string{"user"}}, Policy: policy},
		application: application,
		agentParams: agentparams.NewStore(),
		agentState:  agentparams.NewStore(),
		ownsAgentSession: func(_ context.Context, owner, scope string) bool {
			return (owner == "owner-a" && scope == "session-a") || (owner == "owner-b" && scope == "session-b")
		},
		mux: http.NewServeMux(),
	}
	server.mux.HandleFunc("GET /api/v1/agent/consent", server.withIdentity(server.agentConsentGet))
	server.mux.HandleFunc("GET /api/v1/agent/params", server.withIdentity(server.agentParamsGet))
	handler := server.securityHeadersMiddleware(server.mux)
	server.agentState.Set(agentparams.ScopeKey("owner-a", "session-a"), map[string]json.RawMessage{
		agentparams.ConsentPendingKey: json.RawMessage(`{"reason":"paid"}`),
	})
	server.agentState.Set(agentparams.ScopeKey("owner-a", "session-b"), map[string]json.RawMessage{
		agentparams.ConsentPendingKey: json.RawMessage(`{"reason":"must not be read"}`),
	})
	server.agentParams.SetUser(agentparams.ScopeKey("owner-a", "session-a"), map[string]json.RawMessage{
		"size": json.RawMessage(`"large"`),
	})
	server.agentParams.SetUser(agentparams.ScopeKey("owner-a", "session-b"), map[string]json.RawMessage{
		"size": json.RawMessage(`"private"`),
	})

	for _, path := range []string{
		"/api/v1/agent/consent?scope=session-a",
		"/api/v1/agent/params?scope=session-a",
	} {
		response := serveAgentScopeRequest(handler, http.MethodGet, path, "owner-a")
		if response.Code != http.StatusOK {
			t.Errorf("owned GET %s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{
		"/api/v1/agent/consent?scope=session-b",
		"/api/v1/agent/params?scope=session-b",
		"/api/v1/agent/consent?scope=missing",
		"/api/v1/agent/params?scope=missing",
	} {
		response := serveAgentScopeRequest(handler, http.MethodGet, path, "owner-a")
		if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "must not be read") || strings.Contains(response.Body.String(), `"private"`) {
			t.Errorf("unowned GET %s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{
		"/api/v1/agent/consent?scope=session-a",
		"/api/v1/agent/params?scope=session-a",
	} {
		response := serveAgentScopeRequest(handler, http.MethodGet, path, "")
		if response.Code != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestProcessingPlanItemRoutesRequireOwnerAuthenticationAndSameOrigin(t *testing.T) {
	t.Parallel()
	repository, err := OpenMigratedTestRepository(t.TempDir() + "/plans.db")
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("meshy/processing/remesh")
	if !ok {
		t.Fatal("remesh offering missing")
	}
	now := time.Now().UTC()
	if _, err := repository.CreateSession(context.Background(), chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner A", Title: "Plan owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, plan := range []processing.Plan{
		{ID: "paused-a", OwnerID: "owner-a", SessionID: "session-a", State: processing.Paused, RequestHash: "paused", Version: 1, CreatedAt: now, UpdatedAt: now,
			Steps: []processing.Step{{Capability: offering.Capability, OfferingID: offering.ID, Input: json.RawMessage(`{}`), EstimatedCredits: offering.EstimatedCredits, Revision: offering.Revision, State: processing.Paused, Failure: processing.ReasonSubmitUnknown, Resumable: true}}},
		{ID: "failed-a", OwnerID: "owner-a", SessionID: "session-a", State: processing.Failed, RequestHash: "failed", Version: 1, CreatedAt: now, UpdatedAt: now,
			Steps: []processing.Step{{Capability: offering.Capability, OfferingID: offering.ID, Input: json.RawMessage(`{}`), Revision: offering.Revision, State: processing.Failed, Failure: processing.ReasonTaskFailed}}},
	} {
		if err := repository.CreateProcessingPlan(context.Background(), plan); err != nil {
			t.Fatal(err)
		}
	}
	service, err := processing.New(repository, planCatalog{Catalog: product}, unusedPlanGateway{}, slog.New(slog.NewTextHandler(httptest.NewRecorder(), nil)))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := access.NewPolicy(access.PolicyConfig{DefaultRole: "user", AdminRole: "admin", Roles: []access.RoleDefinition{{Key: "user"}, {Key: "admin"}}})
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	application.Runtime.HTTP.CSRFOriginEnforced = true
	server := &Server{identity: DevelopmentIdentity{DefaultClaims: access.Claims{Roles: []string{"user"}}, Policy: policy}, application: application, processing: service, mux: http.NewServeMux()}
	server.mux.HandleFunc("GET /api/v1/processing-plans/{planID}", server.withIdentity(server.getProcessingPlan))
	server.mux.HandleFunc("POST /api/v1/processing-plans/{planID}/retry", server.withIdentity(server.retryProcessingPlan))
	server.mux.HandleFunc("POST /api/v1/processing-plans/{planID}/resume", server.withIdentity(server.resumeProcessingPlan))
	handler := server.securityHeadersMiddleware(server.sameOriginCSRFMiddleware(server.mux))

	requests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/processing-plans/paused-a", ""},
		{http.MethodPost, "/api/v1/processing-plans/failed-a/retry", `{"expected_plan_version":1,"expected_offering_revision":"` + offering.Revision + `"}`},
		{http.MethodPost, "/api/v1/processing-plans/paused-a/resume", `{"expected_plan_version":1,"expected_offering_revision":"` + offering.Revision + `"}`},
	}
	for _, route := range requests {
		if response := serveAgentScopeRequestWithOrigin(handler, route.method, route.path, route.body, "", ""); response.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s status=%d body=%s", route.method, route.path, response.Code, response.Body.String())
		}
		if response := serveAgentScopeRequestWithOrigin(handler, route.method, route.path, route.body, "owner-b", ""); response.Code != http.StatusNotFound {
			t.Errorf("cross-owner %s %s status=%d body=%s", route.method, route.path, response.Code, response.Body.String())
		}
		if route.method == http.MethodPost {
			response := serveAgentScopeRequestWithOrigin(handler, route.method, route.path, route.body, "owner-a", "https://attacker.invalid")
			if response.Code != http.StatusForbidden {
				t.Errorf("cross-origin %s %s status=%d body=%s", route.method, route.path, response.Code, response.Body.String())
			}
		}
	}
	if response := serveAgentScopeRequest(handler, http.MethodGet, "/api/v1/processing-plans/paused-a", "owner-a"); response.Code != http.StatusOK {
		t.Fatalf("owned plan GET status=%d body=%s", response.Code, response.Body.String())
	}
	if response := serveAgentScopeRequestWithOrigin(handler, http.MethodPost, "/api/v1/processing-plans/failed-a/retry", `{"expected_plan_version":1,"expected_offering_revision":"`+offering.Revision+`"}`, "owner-a", ""); response.Code != http.StatusConflict {
		t.Fatalf("owned but ineligible retry status=%d body=%s", response.Code, response.Body.String())
	}
}

type planCatalog struct{ *catalog.Catalog }

func (planCatalog) OfferingAvailable(generation.OfferingID) bool { return true }

type unusedPlanGateway struct{}

func (unusedPlanGateway) GetPreparedInput(context.Context, generation.OwnerID, generation.PreparedInputID) (generation.PreparedInput, error) {
	return generation.PreparedInput{}, processing.ErrInvalid
}

func (unusedPlanGateway) VerifyPreparedTaskReplay(context.Context, generation.SubmitPreparedCommand) (generation.Task, error) {
	return generation.Task{}, processing.ErrInvalid
}

func (unusedPlanGateway) LookupByIdempotency(context.Context, generation.OwnerID, string) (generation.Task, error) {
	return generation.Task{}, &generation.Error{Code: generation.ErrNotFound, Operation: "lookup_idempotency"}
}

func (unusedPlanGateway) Get(context.Context, generation.OwnerID, generation.TaskID) (generation.Task, error) {
	return generation.Task{}, processing.ErrMissing
}

func (unusedPlanGateway) PrepareInput(context.Context, generation.PrepareInputCommand) (generation.PreparedInput, error) {
	return generation.PreparedInput{}, processing.ErrInvalid
}

func (unusedPlanGateway) SubmitPrepared(context.Context, generation.SubmitPreparedCommand) (generation.Accepted, error) {
	return generation.Accepted{}, processing.ErrInvalid
}

func serveAgentScopeRequest(handler http.Handler, method, path, owner string) *httptest.ResponseRecorder {
	return serveAgentScopeRequestWithOrigin(handler, method, path, "", owner, "")
}

func serveAgentScopeRequestWithOrigin(handler http.Handler, method, path, body, owner, origin string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	if owner != "" {
		request.Header.Set("X-User-ID", owner)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
