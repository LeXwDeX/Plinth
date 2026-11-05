package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// The fixture's hooked grant repository keeps the store's atomic budget
// authorization, as the production wiring passes the SQLite repository itself.
var _ executiongrant.BudgetGrantStore = (*hookedGrantRepository)(nil)

func (r *hookedGrantRepository) PreparePendingWithBudget(ctx context.Context, identity executiongrant.Identity,
	source, triggerTurnID string, budgetEligible bool) (executiongrant.Grant, error) {
	return r.Repository.(executiongrant.BudgetGrantStore).PreparePendingWithBudget(ctx, identity, source, triggerTurnID, budgetEligible)
}

func (r *hookedGrantRepository) ApproveExecutionGrantWithBudget(ctx context.Context, owner, requestID, budgetID, actor string,
	amount budget.Amount) (executiongrant.Grant, error) {
	return r.Repository.(executiongrant.BudgetGrantStore).ApproveExecutionGrantWithBudget(ctx, owner, requestID, budgetID, actor, amount)
}

// #120 item 9: the processing panel approving a paused step of an
// Agent-created plan re-prepares that step's budget-eligible grant. The
// request replays the same grant with its eligibility intact; it neither
// conflicts nor silently turns the Agent's step into a browser step.
func TestProcessingPanelGrantKeepsAgentPlanStepEligibility(t *testing.T) {
	t.Parallel()
	module, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	ctx := context.Background()
	state, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing execution fixture")
	}
	repository := state.(*fixtureExecutionState).repository
	prepare := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/prepare",
		`{"session_id":"`+sessionID+`","capability":"text-to-3d","offering_id":"meshy/meshy-7.1/text-to-3d","input":{"prompt":"a carved oak chair"}}`, "")
	if prepare.Code != http.StatusCreated {
		t.Fatalf("prepare: %d %s", prepare.Code, prepare.Body.String())
	}
	var identity struct {
		ID generation.PreparedInputID `json:"prepared_input_id"`
	}
	if err := json.Unmarshal(prepare.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	prepared, err := module.GetPreparedInput(ctx, "owner-a", identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	binding := harness.DefaultSelection("owner-a", sessionID).Binding
	now := time.Now().UTC()
	plan := processing.Plan{
		ID: "agent-eligible-plan", OwnerID: "owner-a", SessionID: sessionID, Harness: binding,
		Origin:       processing.OriginAgent,
		SourceTaskID: "frozen-source", SourceAssetID: "frozen-model", State: processing.Paused,
		Failure: processing.ReasonConsentRequired, Version: 1, RequestHash: "agent-eligible-plan-request", CreatedAt: now, UpdatedAt: now,
		Steps: []processing.Step{{Capability: prepared.Capability, OfferingID: prepared.OfferingID, Revision: prepared.OfferingRevision,
			Input: prepared.NormalizedInput, State: processing.Paused, Failure: processing.ReasonConsentRequired, Resumable: true,
			PreparedInputID: prepared.ID, PreparedInputHash: prepared.InputHash, OperationID: "agent-eligible-plan", PlanStepID: "step-0", IdempotencyKey: "agent-eligible-plan-step-0"}},
	}
	if err := repository.CreateProcessingPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	// The plan worker issued the step's grant as an Agent step before it paused.
	first, err := repository.PreparePendingWithBudget(ctx, executiongrant.Identity{
		OwnerID: "owner-a", SessionID: sessionID, OfferingID: string(prepared.OfferingID),
		OfferingRevision: prepared.OfferingRevision, PreparedInputID: string(prepared.ID),
		PreparedInputHash: prepared.InputHash, IdempotencyKey: "agent-eligible-plan-step-0",
		OperationID: "agent-eligible-plan", PlanStepID: "step-0", Harness: binding,
	}, executiongrant.HumanBrowser, "", true)
	if err != nil {
		t.Fatal(err)
	}
	response := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/grants",
		`{"prepared_input_id":"`+string(prepared.ID)+`","plan_id":"`+plan.ID+`","plan_step_id":"step-0"}`, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("panel grant request: %d %s", response.Code, response.Body.String())
	}
	var requested struct {
		ID string `json:"request_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &requested); err != nil || requested.ID != first.RequestID {
		t.Fatalf("panel grant = %s err=%v, want replay of %s", response.Body.String(), err, first.RequestID)
	}
	grant, err := repository.GetExecutionGrant(ctx, "owner-a", requested.ID)
	if err != nil || !grant.BudgetEligible {
		t.Fatalf("panel request changed the Agent step's eligibility: %+v err=%v", grant, err)
	}
}
