package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
)

// #410: a plan whose turn ended with required steps left offers "continue".
// The route starts the plan's turn again, only for the person's own browser
// session, and the session projection pairs every step with the Task that ran
// it so the card reads the server's answer.

// insertPlanTaskHTTP writes one Task of the session with the given capability,
// state and submission time, the way a plan step would have run it.
func insertPlanTaskHTTP(t *testing.T, w pendingApprovalWorld, id, capability, offeringID, state string, created time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+w.state.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stamp := created.UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO generation_tasks
 (id, owner_id, capability, offering_json, input_json, request_hash, state, progress,
  materialize_failures, version, created_at, updated_at, session_id)
 VALUES (?, ?, ?, ?, '{}', ?, ?, 100, 0, 1, ?, ?, ?)`,
		id, "owner-a", capability, `{"id":"`+offeringID+`"}`, id+"-hash", state, stamp, stamp, w.sessionID); err != nil {
		t.Fatal(err)
	}
}

func TestContinueProductionPlanStartsTheTurnAgain(t *testing.T) {
	w := pendingApprovalBrowsers(t, nil)
	ctx := context.Background()
	now := time.Now().UTC()
	plan, err := w.state.repository.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-continue", OwnerID: "owner-a", SessionID: w.sessionID,
		Plan: budget.Plan{Title: "四视图加总图", Steps: []budget.PlanStep{
			{Capability: "image-to-3d", Purpose: "生成模型", Estimate: tripoAmount(25)},
			{Capability: "retexture", Purpose: "修贴图", Estimate: budget.Legacy(5)},
		}},
		Limits: []budget.Amount{tripoAmount(50), budget.Legacy(10)}, State: budget.StatePending, CreatedAt: now,
		MessageID: "turn-continue", CallID: "call-continue",
	})
	if err != nil {
		t.Fatal(err)
	}
	decidePath := "/api/v1/chat/sessions/" + w.sessionID + "/budgets/" + plan.ID
	approved := grantTestRequest(t, w.handler, w.cookie, http.MethodPut, decidePath, `{"decision":"approve"}`, "")
	if approved.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body.String())
	}
	var decided struct {
		TurnMessageID string `json:"turn_message_id"`
	}
	if err := json.Unmarshal(approved.Body.Bytes(), &decided); err != nil || decided.TurnMessageID == "" {
		t.Fatalf("approve outcome = %s err=%v", approved.Body.String(), err)
	}
	// The approval turn ended; the plan's first step ran, the second did not.
	if err := w.state.chat.FinishTurn(ctx, decided.TurnMessageID, false); err != nil {
		t.Fatal(err)
	}
	insertPlanTaskHTTP(t, w, "task-continue-1", "image-to-3d", "offering-a", "SUCCEEDED", now.Add(time.Minute))

	continuePath := decidePath + "/continue"
	started := grantTestRequest(t, w.handler, w.cookie, http.MethodPost, continuePath, "", "")
	if started.Code != http.StatusOK {
		t.Fatalf("continue status=%d body=%s", started.Code, started.Body.String())
	}
	var outcome struct {
		TurnMessageID string `json:"turn_message_id"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &outcome); err != nil || outcome.TurnMessageID == "" {
		t.Fatalf("continue outcome = %s err=%v", started.Body.String(), err)
	}
	// The new turn runs the same plan, so it is the approved plan's continuation.
	turn, err := w.state.chat.GetTurn(ctx, "owner-a", w.sessionID, outcome.TurnMessageID)
	if err != nil || turn.Options.PlanContinuation != plan.ID {
		t.Fatalf("continued turn = %+v err=%v", turn, err)
	}
	// Only this person's browser session may start it; another owner's session
	// reads 404 and a foreign cookie cannot start the plan at all.
	foreign := grantTestRequest(t, w.handler, w.other, http.MethodPost, continuePath, "", "")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign continue status=%d body=%s", foreign.Code, foreign.Body.String())
	}

	// The session projection carries each step's standing, so the card reads it
	// instead of pairing steps with Tasks itself.
	session := w.getIn(t, w.cookie, "/api/chat/sessions/"+w.sessionID, "")
	if session.Code != http.StatusOK {
		t.Fatalf("session status=%d body=%s", session.Code, session.Body.String())
	}
	var projected struct {
		Budget *struct {
			State string `json:"state"`
			Steps []struct {
				Index     int    `json:"index"`
				State     string `json:"state"`
				TaskID    string `json:"task_id"`
				TaskState string `json:"task_state"`
			} `json:"steps"`
		} `json:"budget"`
	}
	if err := json.Unmarshal(session.Body.Bytes(), &projected); err != nil || projected.Budget == nil {
		t.Fatalf("session budget = %s err=%v", session.Body.String(), err)
	}
	if len(projected.Budget.Steps) != 2 {
		t.Fatalf("projected steps = %+v", projected.Budget.Steps)
	}
	if projected.Budget.Steps[0].State != budget.StepDone || projected.Budget.Steps[0].TaskID != "task-continue-1" ||
		projected.Budget.Steps[0].Index != 1 {
		t.Fatalf("first projected step = %+v", projected.Budget.Steps[0])
	}
	// The turn that just started runs, so the second step still waits.
	if projected.Budget.Steps[1].State != budget.StepPending || projected.Budget.Steps[1].TaskID != "" {
		t.Fatalf("second projected step = %+v", projected.Budget.Steps[1])
	}
}

// A turn that still runs cannot be continued a second time: the plan is
// already running in it.
func TestContinueProductionPlanRefusesWhileTheTurnRuns(t *testing.T) {
	w := pendingApprovalBrowsers(t, nil)
	ctx := context.Background()
	now := time.Now().UTC()
	plan, err := w.state.repository.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-running", OwnerID: "owner-a", SessionID: w.sessionID,
		Plan:   budget.Plan{Title: "进行中", Steps: []budget.PlanStep{{Capability: "image-to-3d", Purpose: "生成模型", Estimate: tripoAmount(25)}}},
		Limits: []budget.Amount{tripoAmount(50)}, State: budget.StatePending, CreatedAt: now,
		MessageID: "turn-running", CallID: "call-running",
	})
	if err != nil {
		t.Fatal(err)
	}
	decidePath := "/api/v1/chat/sessions/" + w.sessionID + "/budgets/" + plan.ID
	if approved := grantTestRequest(t, w.handler, w.cookie, http.MethodPut, decidePath, `{"decision":"approve"}`, ""); approved.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body.String())
	}
	// The approval turn has not ended.
	response := grantTestRequest(t, w.handler, w.cookie, http.MethodPost, decidePath+"/continue", "", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("continue while the turn runs status=%d body=%s", response.Code, response.Body.String())
	}
	var problem struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || problem.Error == "" {
		t.Fatalf("continue problem = %s err=%v", response.Body.String(), err)
	}
}

// A plan that is no longer the session's active one (a newer plan replaced it)
// refuses the continuation with the Schema's text.
func TestContinueProductionPlanRefusesAReplacedPlan(t *testing.T) {
	w := pendingApprovalBrowsers(t, nil)
	ctx := context.Background()
	now := time.Now().UTC()
	first, err := w.state.repository.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-replaced", OwnerID: "owner-a", SessionID: w.sessionID,
		Plan:   budget.Plan{Title: "旧计划", Steps: []budget.PlanStep{{Capability: "image-to-3d", Purpose: "生成模型", Estimate: tripoAmount(25)}}},
		Limits: []budget.Amount{tripoAmount(50)}, State: budget.StatePending, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	decidePath := "/api/v1/chat/sessions/" + w.sessionID + "/budgets/" + first.ID
	if approved := grantTestRequest(t, w.handler, w.cookie, http.MethodPut, decidePath, `{"decision":"approve"}`, ""); approved.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body.String())
	}
	second, err := w.state.repository.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: "pb-new", OwnerID: "owner-a", SessionID: w.sessionID,
		Plan:   budget.Plan{Title: "新计划", Steps: []budget.PlanStep{{Capability: "retexture", Purpose: "修贴图", Estimate: budget.Legacy(5)}}},
		Limits: []budget.Amount{budget.Legacy(10)}, State: budget.StatePending, CreatedAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	newPath := "/api/v1/chat/sessions/" + w.sessionID + "/budgets/" + second.ID
	if approved := grantTestRequest(t, w.handler, w.cookie, http.MethodPut, newPath, `{"decision":"approve"}`, ""); approved.Code != http.StatusOK {
		t.Fatalf("approve the new plan status=%d body=%s", approved.Code, approved.Body.String())
	}
	response := grantTestRequest(t, w.handler, w.cookie, http.MethodPost, decidePath+"/continue", "", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("continue a replaced plan status=%d body=%s", response.Code, response.Body.String())
	}
	var problem struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || problem.Error == "" {
		t.Fatalf("replaced plan problem = %s err=%v", response.Body.String(), err)
	}
}

var _ = sql.ErrNoRows
