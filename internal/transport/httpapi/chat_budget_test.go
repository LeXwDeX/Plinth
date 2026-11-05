package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
)

// A production plan card travels through the same pending-approvals route as a
// step card, is decided on its own session route, and then appears as the
// session's current budget (#103).
func TestProductionPlanCardAndSessionBudgetOverHTTP(t *testing.T) {
	w := pendingApprovalBrowsers(t, nil)
	now := time.Now().UTC()
	plan, err := w.state.repository.CreateProductionBudget(context.Background(), budget.ProductionBudget{
		ID: "pb-http", OwnerID: "owner-a", SessionID: w.sessionID,
		Plan: budget.Plan{Title: "骑士", Steps: []budget.PlanStep{
			{Capability: "image-to-3d", Purpose: "生成模型", Estimate: tripoAmount(25)},
			{Capability: "retexture", Purpose: "修贴图", Estimate: budget.Legacy(5)},
		}},
		Limits: []budget.Amount{tripoAmount(50), budget.Legacy(10)}, State: budget.StatePending, CreatedAt: now,
		MessageID: "turn-http", CallID: "call-http",
	})
	if err != nil {
		t.Fatal(err)
	}

	listPath := "/api/v1/chat/sessions/" + w.sessionID + "/pending-approvals"
	list := w.getIn(t, w.cookie, listPath, "")
	if list.Code != http.StatusOK {
		t.Fatalf("pending list status=%d body=%s", list.Code, list.Body.String())
	}
	var listed struct {
		Items []struct {
			Kind             string                  `json:"kind"`
			RequestID        string                  `json:"request_id"`
			Status           string                  `json:"status"`
			Tool             string                  `json:"tool"`
			EstimatedCredits int                     `json:"estimated_credits"`
			Budgets          []budget.Amount         `json:"budgets"`
			EstimatedTotals  []budget.Amount         `json:"estimated_totals"`
			MessageID        string                  `json:"message_id"`
			Plan             *budget.Plan            `json:"plan"`
			Inputs           []pendingapproval.Input `json:"inputs"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatalf("pending list %s: %v", list.Body.String(), err)
	}
	if len(listed.Items) != 1 {
		t.Fatalf("pending list = %s", list.Body.String())
	}
	item := listed.Items[0]
	if item.Kind != pendingapproval.ItemKindPlan || item.RequestID != plan.ID ||
		item.Status != pendingapproval.StatusPending || item.Tool != "propose_production_plan" ||
		item.EstimatedCredits != 0 || item.MessageID != "turn-http" ||
		!reflect.DeepEqual(item.Budgets, []budget.Amount{budget.Legacy(10), tripoAmount(50)}) ||
		!reflect.DeepEqual(item.EstimatedTotals, []budget.Amount{budget.Legacy(5), tripoAmount(25)}) ||
		item.Plan == nil || item.Plan.Title != "骑士" || len(item.Plan.Steps) != 2 || len(item.Inputs) != 0 {
		t.Fatalf("plan card = %+v", item)
	}

	decidePath := "/api/v1/chat/sessions/" + w.sessionID + "/budgets/" + plan.ID
	approved := grantTestRequest(t, w.handler, w.cookie, http.MethodPut, decidePath, `{"decision":"approve"}`, "")
	if approved.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body.String())
	}
	var outcome struct {
		BudgetID      string `json:"budget_id"`
		State         string `json:"state"`
		MessageID     string `json:"message_id"`
		TurnMessageID string `json:"turn_message_id"`
	}
	if err := json.Unmarshal(approved.Body.Bytes(), &outcome); err != nil {
		t.Fatal(err)
	}
	if outcome.BudgetID != plan.ID || outcome.State != budget.StateActive ||
		outcome.MessageID != pendingapproval.BudgetMessageID(plan.ID) || outcome.TurnMessageID == "" {
		t.Fatalf("approve outcome = %+v", outcome)
	}
	posted := false
	for _, message := range w.messages(t) {
		if message.ID == outcome.MessageID && strings.Contains(message.Content, "骑士") {
			posted = true
		}
	}
	if !posted {
		t.Fatal("the plan decision message was not posted")
	}
	// #134: the approval starts the turn that runs the plan: the person's
	// go-ahead and the Agent's reply waiting to be written.
	messages := w.messages(t)
	goAhead := strings.ReplaceAll(shippedContinuationMessage(t), "{title}", "骑士")
	if len(messages) < 2 || messages[len(messages)-1].ID != outcome.TurnMessageID ||
		messages[len(messages)-2].Content != goAhead {
		t.Fatalf("continuation turn messages = %+v, want the go-ahead %q", messages, goAhead)
	}
	turn, err := w.state.chat.GetTurn(context.Background(), "owner-a", w.sessionID, outcome.TurnMessageID)
	if err != nil || turn.Options.PlanContinuation != plan.ID {
		t.Fatalf("continuation turn = %+v err=%v", turn, err)
	}

	session := w.getIn(t, w.cookie, "/api/chat/sessions/"+w.sessionID, "")
	if session.Code != http.StatusOK {
		t.Fatalf("session status=%d body=%s", session.Code, session.Body.String())
	}
	var projected struct {
		Budget *budget.Summary `json:"budget"`
	}
	if err := json.Unmarshal(session.Body.Bytes(), &projected); err != nil || projected.Budget == nil {
		t.Fatalf("session budget = %s err=%v", session.Body.String(), err)
	}
	if projected.Budget.ID != plan.ID || projected.Budget.State != budget.StateActive ||
		!reflect.DeepEqual(projected.Budget.Units, []budget.UnitBudget{
			budget.NewUnitBudget(budget.Legacy(10), 0, 0), budget.NewUnitBudget(tripoAmount(50), 0, 0)}) ||
		projected.Budget.Plan.Title != "骑士" {
		t.Fatalf("session budget = %+v", projected.Budget)
	}
	if after, err := w.serviceList(t, w.cookie); err != nil || len(after) != 0 {
		t.Fatalf("decided plan still pending = %+v err=%v", after, err)
	}

	repeated := grantTestRequest(t, w.handler, w.cookie, http.MethodPut, decidePath, `{"decision":"deny"}`, "")
	if repeated.Code != http.StatusConflict {
		t.Fatalf("repeated decision status=%d body=%s", repeated.Code, repeated.Body.String())
	}
	foreign := grantTestRequest(t, w.handler, w.other, http.MethodPut, decidePath, `{"decision":"approve"}`, "")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign decision status=%d body=%s", foreign.Code, foreign.Body.String())
	}
}

// tripoAmount is a whole number of Tripo credits, a provider-native unit.
func tripoAmount(whole int64) budget.Amount {
	return budget.Amount{Unit: "tripo.credit", UnitLabel: "Tripo credits", Micros: whole * 1000000}
}

// shippedContinuationMessage is the go-ahead the shipped Schema words.
func shippedContinuationMessage(t *testing.T) string {
	t.Helper()
	shipped, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return shipped.Application().Chat.ApprovalPolicy.Budget.Continuation.Message
}

// #134: a denied plan keeps its notice and starts no turn.
func TestDeniedProductionPlanStartsNoTurn(t *testing.T) {
	w := pendingApprovalBrowsers(t, nil)
	plan, err := w.state.repository.CreateProductionBudget(context.Background(), budget.ProductionBudget{
		ID: "pb-denied-http", OwnerID: "owner-a", SessionID: w.sessionID,
		Plan:   budget.Plan{Title: "不要", Steps: []budget.PlanStep{{Capability: "image-to-3d", Purpose: "生成模型", Estimate: budget.Legacy(5)}}},
		Limits: []budget.Amount{budget.Legacy(10)}, State: budget.StatePending, CreatedAt: time.Now().UTC(),
		MessageID: "turn-denied-http", CallID: "call-denied-http",
	})
	if err != nil {
		t.Fatal(err)
	}
	before := len(w.messages(t))
	denied := grantTestRequest(t, w.handler, w.cookie, http.MethodPut,
		"/api/v1/chat/sessions/"+w.sessionID+"/budgets/"+plan.ID, `{"decision":"deny"}`, "")
	var outcome map[string]any
	if err := json.Unmarshal(denied.Body.Bytes(), &outcome); err != nil || denied.Code != http.StatusOK ||
		outcome["state"] != budget.StateDenied || outcome["turn_message_id"] != nil {
		t.Fatalf("deny = %d %s err=%v", denied.Code, denied.Body.String(), err)
	}
	messages := w.messages(t)
	if len(messages) != before+1 || messages[len(messages)-1].ID != pendingapproval.BudgetMessageID(plan.ID) {
		t.Fatalf("after a denial = %+v, want only the notice", messages)
	}
}

func (w pendingApprovalWorld) serviceList(t *testing.T, cookie *http.Cookie) ([]pendingapproval.Item, error) {
	t.Helper()
	response := w.getIn(t, cookie, "/api/v1/chat/sessions/"+w.sessionID+"/pending-approvals", "")
	if response.Code != http.StatusOK {
		t.Fatalf("pending list status=%d body=%s", response.Code, response.Body.String())
	}
	var listed struct {
		Items []pendingapproval.Item `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		return nil, err
	}
	return listed.Items, nil
}
