package chat

import (
	"context"
	"encoding/json"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/modelcheck"
)

// AssetListReviewChecks freezes the 3D acceptance checks of one list item
// from the frozen production brief (#329b). The thresholds come from the same
// purpose budget the plan card and inspect_model use, so the review snapshot
// and the plan never disagree; a check the purpose gives no usable threshold
// is left out. A deployment without usage profiles has none.
func (service *Service) AssetListReviewChecks(
	_ context.Context,
	ownerID, sessionID string,
	brief json.RawMessage,
) []budget.PlanCheck {
	policy := service.productionPlans
	if policy == nil || len(policy.Usage.Profiles) == 0 {
		return nil
	}
	command := ToolCommand{
		OwnerID: ownerID, SessionID: sessionID,
		Options: TurnOptions{ProductionBrief: productionBriefFromRaw(brief)},
	}
	profile, purposeBudget, found := policy.purposeProfile(command)
	if !found {
		return nil
	}
	checks := make([]budget.PlanCheck, 0, len(modelcheck.KnownIDs()))
	for _, check := range modelcheck.KnownIDs() {
		planned, ok := policy.planCheck(check, profile, purposeBudget)
		if !ok || !reviewCheckDeclared(planned) {
			continue
		}
		checks = append(checks, planned)
	}
	return checks
}

// productionBriefFromRaw reads a stored brief object. An unreadable value is
// an empty brief, which resolves the Schema's default purpose.
func productionBriefFromRaw(raw json.RawMessage) ProductionBrief {
	if len(raw) == 0 {
		return ProductionBrief{}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return ProductionBrief{}
	}
	return ProductionBrief(fields)
}
