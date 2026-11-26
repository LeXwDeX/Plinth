package processing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #665 third review: a replace is held to the approved production plan only
// when it brings a new capability or the step itself falls under the plan; a
// step the plan never covered may be replaced by the same capability, and an
// Offering the plan pins is compared too.
func TestReviseReplaceIsBoundByThePlanOnlyWhereThePlanApplies665(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name       string
		approved   budget.Plan
		capability string
		offering   string
		parameters string
		refused    bool
	}{
		{"same capability, step never covered", budget.Plan{Steps: []budget.PlanStep{{Capability: "remesh"}}},
			"rigging", "rigging", `{}`, false},
		{"same capability, step covered", budget.Plan{Steps: []budget.PlanStep{{Capability: "remesh"}, {Capability: "rigging"}}},
			"rigging", "rigging", `{}`, false},
		{"new capability outside the plan", budget.Plan{Steps: []budget.PlanStep{{Capability: "remesh"}}},
			"resize", "resize", `{"origin_at":"bottom"}`, true},
		{"new capability inside the plan", budget.Plan{Steps: []budget.PlanStep{{Capability: "remesh"}, {Capability: "resize"}}},
			"resize", "resize", `{"origin_at":"bottom"}`, false},
		{"pinned offering mismatch", budget.Plan{Steps: []budget.PlanStep{{Capability: "remesh"},
			{Capability: "resize", OfferingID: "resize-pinned"}}},
			"resize", "resize", `{"origin_at":"bottom"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, _, _, plan := checkpointFixture(t)
			approved := tc.approved
			approved.Title = "plan"
			_, err := service.Revise(ctx, ReviseCommand{OwnerID: "owner", SessionID: "session", PlanID: plan.ID,
				ExpectedPlanVersion: plan.Version, Operation: OperationReplace, StepIndex: 1,
				Capability: generation.Capability(tc.capability), OfferingID: generation.OfferingID(tc.offering),
				Parameters: json.RawMessage(tc.parameters), Reason: "替换", ApprovedPlan: &approved})
			if tc.refused != errors.Is(err, ErrRevisionOutsidePlan) || (!tc.refused && err != nil) {
				t.Fatalf("replace = %v, refused want %v", err, tc.refused)
			}
		})
	}
}
