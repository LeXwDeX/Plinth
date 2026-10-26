package generationtool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

func TestNativePlanPriceProjectionKeepsPrivateFrozenFieldsPrivate(t *testing.T) {
	q := &generation.NativeQuote{Version: 1, Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", BaseAmount: "0.5", ExtraAmount: "0", TotalAmount: "0.5"}
	plan := processing.Plan{ID: "plan", Steps: []processing.Step{{NativeQuote: q, PreparedInputID: "private-prepare", PreparedInputHash: "private-hash", AuthorizationRef: "private-grant", OperationID: "private-op", IdempotencyKey: "private-key", Input: json.RawMessage(`{}`)}, {EstimatedCredits: 7, Input: json.RawMessage(`{}`)}}}
	executor := &Executor{}
	config := &catalog.ChatToolProcessingSchema{Plan: &catalog.ChatToolProcessingPlanSchema{}}
	command := chat.ToolCommand{Tool: catalog.ChatToolSchema{ResultFields: catalog.ChatToolResultFields{Success: "success"}}}
	result, e := executor.processingPlanResult(command, config, plan)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"private-", "prepared_input", "authorization", "idempotency", "operation_id"} {
		if strings.Contains(result.AgentContent, secret) {
			t.Fatalf("private plan metadata leaked: %s", secret)
		}
	}
	var got map[string]json.RawMessage
	if json.Unmarshal([]byte(result.AgentContent), &got) != nil {
		t.Fatal("invalid result")
	}
	if _, ok := got["estimated_credits"]; ok {
		t.Fatal("mixed plan projected one unit total")
	}
	if !strings.Contains(string(got["price_totals"]), `"amount":"0.5"`) || !strings.Contains(string(got["price_totals"]), `"unit":"legacy.credits"`) {
		t.Fatalf("mixed totals %s", got["price_totals"])
	}
	var steps []map[string]json.RawMessage
	_ = json.Unmarshal(got["steps"], &steps)
	if _, ok := steps[0]["estimated_credits"]; ok {
		t.Fatal("native step became legacy zero")
	}
	if _, ok := steps[0]["parameters"]; !ok {
		t.Fatal("original public field lost")
	}
}
