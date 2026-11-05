package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #120 item 6: the approval decision member is Schema data. With the Catalog
// renaming chat.api.fields.decision, the public schema names the new member
// (the browser builds its body from it), both decision routes accept it, and
// the former literal is refused like any unknown member.
func TestApprovalDecisionMemberFollowsTheSchema(t *testing.T) {
	const member = "verdict"
	w := pendingApprovalBrowsers(t, func(schema *catalog.ApplicationSchema) {
		schema.Chat.API.Fields.Decision = member
	})

	public := w.getIn(t, w.cookie, "/api/v1/schema", "")
	var projected struct {
		Schema struct {
			Chat struct {
				API struct {
					Fields struct {
						Decision string `json:"decision"`
					} `json:"fields"`
				} `json:"api"`
			} `json:"chat"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(public.Body.Bytes(), &projected); err != nil || public.Code != http.StatusOK ||
		projected.Schema.Chat.API.Fields.Decision != member {
		t.Fatalf("public decision member = %q status=%d err=%v", projected.Schema.Chat.API.Fields.Decision, public.Code, err)
	}

	// A step grant on the task grant route.
	prepare := grantTestRequest(t, w.handler, w.cookie, http.MethodPost, "/api/v1/tasks/prepare",
		`{"session_id":"`+w.sessionID+`","capability":"text-to-3d","offering_id":"meshy/meshy-7.1/text-to-3d","input":{"prompt":"a carved oak chair"}}`, "")
	if prepare.Code != http.StatusCreated {
		t.Fatalf("prepare: %d %s", prepare.Code, prepare.Body.String())
	}
	var prepared struct {
		ID string `json:"prepared_input_id"`
	}
	if err := json.Unmarshal(prepare.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	requested := grantTestRequest(t, w.handler, w.cookie, http.MethodPost, "/api/v1/tasks/grants",
		`{"prepared_input_id":"`+prepared.ID+`"}`, "approval-decision-member")
	var grant struct {
		RequestID string `json:"request_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(requested.Body.Bytes(), &grant); err != nil || requested.Code != http.StatusCreated || grant.Status != "pending" {
		t.Fatalf("grant: %d %s err=%v", requested.Code, requested.Body.String(), err)
	}
	grantPath := "/api/v1/tasks/grants/" + grant.RequestID
	for _, refused := range []string{`{"decision":"approve"}`, `{"` + member + `":"approve","decision":"approve"}`, `{"` + member + `":"maybe"}`, `{}`} {
		if response := grantTestRequest(t, w.handler, w.cookie, http.MethodPut, grantPath, refused, ""); response.Code != http.StatusBadRequest {
			t.Fatalf("grant decision %s: %d %s", refused, response.Code, response.Body.String())
		}
	}
	if response := grantTestRequest(t, w.handler, w.cookie, http.MethodPut, grantPath, `{"`+member+`":"approve"}`, ""); response.Code != http.StatusOK {
		t.Fatalf("grant decision with the Schema member: %d %s", response.Code, response.Body.String())
	}

	// A production plan on its session budget route.
	plan, err := w.state.repository.CreateProductionBudget(context.Background(), budget.ProductionBudget{
		ID: "pb-decision-member", OwnerID: "owner-a", SessionID: w.sessionID,
		Plan:         budget.Plan{Title: "改名", Steps: []budget.PlanStep{{Capability: "image-to-3d", Purpose: "生成模型", EstimatedCredits: 5}}},
		LimitCredits: 10, State: budget.StatePending, CreatedAt: time.Now().UTC(),
		MessageID: "turn-decision-member", CallID: "call-decision-member",
	})
	if err != nil {
		t.Fatal(err)
	}
	planPath := "/api/v1/chat/sessions/" + w.sessionID + "/budgets/" + plan.ID
	if response := grantTestRequest(t, w.handler, w.cookie, http.MethodPut, planPath, `{"decision":"approve"}`, ""); response.Code != http.StatusBadRequest {
		t.Fatalf("plan decision with the former member: %d %s", response.Code, response.Body.String())
	}
	response := grantTestRequest(t, w.handler, w.cookie, http.MethodPut, planPath, `{"`+member+`":"approve"}`, "")
	var outcome struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &outcome); err != nil || response.Code != http.StatusOK || outcome.State != budget.StateActive {
		t.Fatalf("plan decision with the Schema member: %d %s err=%v", response.Code, response.Body.String(), err)
	}
}
