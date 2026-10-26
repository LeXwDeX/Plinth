package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// A manually approved, frozen plan must authorize the exact same Harness
// identity as the later paid submission. No Provider or background worker runs.
func TestProcessingPlanHumanGrantPreservesHarnessThroughResume(t *testing.T) {
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
		ID: "human-harness-plan", OwnerID: "owner-a", SessionID: sessionID, Harness: binding,
		SourceTaskID: "frozen-source", SourceAssetID: "frozen-model", State: processing.Paused,
		Failure: processing.ReasonConsentRequired, Version: 1, RequestHash: "frozen-plan-request", CreatedAt: now, UpdatedAt: now,
		Steps: []processing.Step{{Capability: prepared.Capability, OfferingID: prepared.OfferingID, Revision: prepared.OfferingRevision,
			Input: prepared.NormalizedInput, State: processing.Paused, Failure: processing.ReasonConsentRequired, Resumable: true,
			PreparedInputID: prepared.ID, PreparedInputHash: prepared.InputHash, OperationID: "human-harness-plan", PlanStepID: "step-0", IdempotencyKey: "human-harness-plan-step-0"}},
	}
	if err := repository.CreateProcessingPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	response := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/grants",
		`{"prepared_input_id":"`+string(prepared.ID)+`","plan_id":"`+plan.ID+`","plan_step_id":"step-0"}`, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("request grant: %d %s", response.Code, response.Body.String())
	}
	var requested struct {
		ID string `json:"request_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &requested); err != nil || requested.ID == "" {
		t.Fatalf("grant: %s %v", response.Body.String(), err)
	}
	grant, err := repository.GetExecutionGrant(ctx, "owner-a", requested.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !harness.SameBinding(grant.Harness, binding) {
		t.Fatalf("human plan grant lost durable Harness identity: got %+v want %+v", grant.Harness, binding)
	}
	decision := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/tasks/grants/"+requested.ID, `{"decision":"approve"}`, "")
	if decision.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", decision.Code, decision.Body.String())
	}
	resumeBody, _ := json.Marshal(processing.ResumeCommand{ExpectedPlanVersion: 1, ExpectedOfferingRevision: prepared.OfferingRevision, AuthorizationRef: requested.ID})
	resumed := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/processing-plans/"+plan.ID+"/resume", string(resumeBody), "")
	if resumed.Code != http.StatusAccepted {
		t.Fatalf("resume: %d %s", resumed.Code, resumed.Body.String())
	}
	stored, err := repository.GetProcessingPlan(ctx, "owner-a", plan.ID)
	if err != nil || stored.State != processing.Pending || stored.Steps[0].AuthorizationRef != requested.ID || !harness.SameBinding(stored.Harness, binding) {
		t.Fatalf("resumed plan=%+v err=%v", stored, err)
	}
	command := generation.SubmitPreparedCommand{OwnerID: "owner-a", SessionID: sessionID, PreparedInputID: prepared.ID,
		OperationID: stored.ID, PlanStepID: stored.Steps[0].PlanStepID, IdempotencyKey: stored.Steps[0].IdempotencyKey, AuthorizationRef: requested.ID, Harness: stored.Harness}
	accepted, err := module.SubmitPrepared(ctx, command)
	if err != nil || accepted.TaskID == "" || accepted.Replayed {
		t.Fatalf("approved plan could not submit exact identity: %+v %v", accepted, err)
	}
	replay, err := module.SubmitPrepared(ctx, command)
	if err != nil || replay.TaskID != accepted.TaskID || !replay.Replayed {
		t.Fatalf("approved plan replay=%+v err=%v", replay, err)
	}
	command.Harness = harness.Binding{}
	if _, err := module.SubmitPrepared(ctx, command); err == nil {
		t.Fatal("legacy zero identity replayed nonzero approved plan")
	}
}
