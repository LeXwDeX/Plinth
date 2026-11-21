package app

import (
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// budgetPlanStore is the plan store the grant service reads (#434). A plan
// saved before #426 pinned the Agent's model name in PlanStep.OfferingID, while
// the budget compares Offering IDs; the store's read view resolves each such
// pin through the runtime Catalog, using the same ModelNameMatches rule the
// plans and generation tools select an Offering with, so an approved old plan
// keeps covering the steps it quoted. Resolution needs the runtime Catalog and
// therefore belongs here, in assembly, not in internal/budget. The source of
// Offerings is the one the plan proposal quotes from, so a legacy plan and a
// new plan of the same model resolve to the same Offering, and the store keeps
// the bytes it stored (no migration rewrites plan_json).
func budgetPlanStore(store executiongrant.ProductionBudgetStore,
	offerings func(generation.Capability) []generation.Offering) executiongrant.ProductionBudgetStore {
	return executiongrant.WithResolvedPlanPins(store, func(capability, pinned string) (string, bool) {
		return chat.ResolvePlanPin(offerings(generation.Capability(capability)), capability, pinned)
	})
}
