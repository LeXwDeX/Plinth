package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

type reconcileGateway struct {
	unusedPlanGateway
	lookupErr error
}

func (g *reconcileGateway) LookupByIdempotency(context.Context, generation.OwnerID, string) (generation.Task, error) {
	return generation.Task{}, g.lookupErr
}

func TestProcessingReconcileRouteScopeVersionAndSchemaErrors(t *testing.T) {
	t.Parallel()
	repository, err := OpenMigratedTestRepository(t.TempDir() + "/reconcile.db")
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := repository.CreateSession(context.Background(), chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner A", Title: "Reconcile", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	plan := processing.Plan{ID: "locked", OwnerID: "owner-a", SessionID: "session-a", State: processing.Paused, Failure: processing.ReasonSubmitUnreconciled,
		RequestHash: "locked", Version: 1, CreatedAt: now, UpdatedAt: now,
		Steps: []processing.Step{{State: processing.Paused, Failure: processing.ReasonSubmitUnreconciled, IdempotencyKey: "original-processing-key"}}}
	if err := repository.CreateProcessingPlan(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	gateway := &reconcileGateway{lookupErr: &generation.Error{Code: generation.ErrConflict}}
	service, err := processing.New(repository, planCatalog{Catalog: product}, gateway, nil)
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
	route := application.Runtime.HTTP.Routes.ProcessingPlanReconcile
	server.mux.HandleFunc("POST "+route, server.withIdentity(server.reconcileProcessingPlan))
	handler := server.sameOriginCSRFMiddleware(server.mux)
	path := "/api/v1/processing-plans/locked/reconcile"
	for _, test := range []struct {
		name, owner, origin, body string
		status                    int
	}{
		{"anonymous", "", "", `{"expected_plan_version":1}`, http.StatusUnauthorized},
		{"cross owner", "owner-b", "", `{"expected_plan_version":1}`, http.StatusNotFound},
		{"cross origin", "owner-a", "https://attacker.invalid", `{"expected_plan_version":1}`, http.StatusForbidden},
		{"malformed", "owner-a", "", `{`, http.StatusBadRequest},
		{"missing version", "owner-a", "", `{}`, http.StatusBadRequest},
		{"stale version", "owner-a", "", `{"expected_plan_version":2}`, http.StatusConflict},
		{"undetermined", "owner-a", "", `{"expected_plan_version":1}`, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := serveAgentScopeRequestWithOrigin(handler, http.MethodPost, path, test.body, test.owner, test.origin)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.name == "undetermined" {
				var problem struct {
					Code   string `json:"code"`
					Detail string `json:"detail"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
					t.Fatal(err)
				}
				if problem.Code != "processing_reconcile_undetermined" || problem.Detail != application.Content.Messages["ui.workspace.processing.plan_reconcile_undetermined"] {
					t.Fatalf("problem=%+v", problem)
				}
			}
		})
	}
	gateway.lookupErr = &generation.Error{Code: generation.ErrNotFound}
	response := serveAgentScopeRequestWithOrigin(handler, http.MethodPost, path, `{"expected_plan_version":1}`, "owner-a", "")
	if response.Code != http.StatusOK {
		t.Fatalf("clear status=%d body=%s", response.Code, response.Body.String())
	}
	var cleared processing.Plan
	if err := json.Unmarshal(response.Body.Bytes(), &cleared); err != nil {
		t.Fatal(err)
	}
	if cleared.Failure != processing.ReasonReconciledAbsent || !cleared.Steps[0].Resumable || cleared.Version != 2 {
		t.Fatalf("clear=%+v", cleared)
	}
	persisted, err := repository.GetProcessingPlan(context.Background(), "owner-a", plan.ID)
	if err != nil || persisted.Failure != cleared.Failure || persisted.Steps[0].IdempotencyKey != "original-processing-key" {
		t.Fatalf("persisted=%+v err=%v", persisted, err)
	}
	response = serveAgentScopeRequestWithOrigin(handler, http.MethodPost, path, `{"expected_plan_version":2}`, "owner-a", "")
	if response.Code != http.StatusOK {
		t.Fatalf("repeat status=%d body=%s", response.Code, response.Body.String())
	}
}
