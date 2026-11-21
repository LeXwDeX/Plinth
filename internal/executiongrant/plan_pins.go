package executiongrant

import (
	"context"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
)

// WithResolvedPlanPins wraps a plan store so every plan it returns has its
// offering pins resolved through resolve (#434). Plans saved before #426 kept
// the Agent's model name in PlanStep.OfferingID, while budget.Plan.Allows and
// budget.Plan.Covers compare the Offering ID a grant runs: read unchanged, an
// approved old plan leaves every pinned step off-plan, and a budget-mode
// three-view series cannot continue. The store keeps the bytes it stored; only
// the returned plans are resolved, and a pin resolve leaves unchanged keeps the
// stored value, so a name the Catalog cannot identify still reads as off-plan.
// A nil resolve returns store unchanged.
func WithResolvedPlanPins(store ProductionBudgetStore, resolve budget.PinResolver) ProductionBudgetStore {
	if store == nil || resolve == nil {
		return store
	}
	return resolvedPlanStore{ProductionBudgetStore: store, resolve: resolve}
}

// resolvedPlanStore is the read view WithResolvedPlanPins installs. It also
// implements the store's optional interfaces, delegating each to the wrapped
// store: wrapping the plan store must not turn off the batch session
// projection, the release of an unbound reservation, or the retry of a plan
// notice. A wrapped store that does not provide one of them keeps the same
// behavior the grant service had without it.
type resolvedPlanStore struct {
	ProductionBudgetStore
	resolve budget.PinResolver
}

// plan returns the stored plan with its pins resolved.
func (s resolvedPlanStore) plan(value budget.ProductionBudget) budget.ProductionBudget {
	value.Plan = value.Plan.ResolvePins(s.resolve)
	return value
}

// plans resolves a plan list, keeping the nil/empty distinction of the store.
func (s resolvedPlanStore) plans(values []budget.ProductionBudget) []budget.ProductionBudget {
	if values == nil {
		return nil
	}
	resolved := make([]budget.ProductionBudget, len(values))
	for index, value := range values {
		resolved[index] = s.plan(value)
	}
	return resolved
}

// summary returns the summary with its plan's pins resolved.
func (s resolvedPlanStore) summary(value budget.Summary) budget.Summary {
	value.Plan = value.Plan.ResolvePins(s.resolve)
	return value
}

func (s resolvedPlanStore) CreateProductionBudget(ctx context.Context, value budget.ProductionBudget) (budget.ProductionBudget, error) {
	stored, err := s.ProductionBudgetStore.CreateProductionBudget(ctx, value)
	if err != nil {
		return stored, err
	}
	return s.plan(stored), nil
}

func (s resolvedPlanStore) DecideProductionBudget(ctx context.Context, ownerID, budgetID, actor string,
	approve bool, now time.Time,
) (budget.ProductionBudget, error) {
	decided, err := s.ProductionBudgetStore.DecideProductionBudget(ctx, ownerID, budgetID, actor, approve, now)
	if err != nil {
		return decided, err
	}
	return s.plan(decided), nil
}

func (s resolvedPlanStore) GetProductionBudget(ctx context.Context, ownerID, budgetID string) (budget.ProductionBudget, error) {
	value, err := s.ProductionBudgetStore.GetProductionBudget(ctx, ownerID, budgetID)
	if err != nil {
		return value, err
	}
	return s.plan(value), nil
}

func (s resolvedPlanStore) PendingProductionBudgets(ctx context.Context, ownerID, sessionID string) ([]budget.ProductionBudget, error) {
	values, err := s.ProductionBudgetStore.PendingProductionBudgets(ctx, ownerID, sessionID)
	if err != nil {
		return values, err
	}
	return s.plans(values), nil
}

func (s resolvedPlanStore) CurrentProductionBudget(ctx context.Context, ownerID, sessionID string) (budget.ProductionBudget, bool, error) {
	value, found, err := s.ProductionBudgetStore.CurrentProductionBudget(ctx, ownerID, sessionID)
	if err != nil || !found {
		return value, found, err
	}
	return s.plan(value), true, nil
}

func (s resolvedPlanStore) ProductionBudgetSummary(ctx context.Context, ownerID, sessionID string) (budget.Summary, bool, error) {
	summary, found, err := s.ProductionBudgetStore.ProductionBudgetSummary(ctx, ownerID, sessionID)
	if err != nil || !found {
		return summary, found, err
	}
	return s.summary(summary), true, nil
}

func (s resolvedPlanStore) BudgetSummaryForGrant(ctx context.Context, ownerID, requestID string) (budget.Summary, bool, error) {
	summary, found, err := s.ProductionBudgetStore.BudgetSummaryForGrant(ctx, ownerID, requestID)
	if err != nil || !found {
		return summary, found, err
	}
	return s.summary(summary), true, nil
}

func (s resolvedPlanStore) ExpireProductionBudgets(ctx context.Context, cutoff time.Time) ([]budget.ProductionBudget, error) {
	values, err := s.ProductionBudgetStore.ExpireProductionBudgets(ctx, cutoff)
	if err != nil {
		return values, err
	}
	return s.plans(values), nil
}

func (s resolvedPlanStore) SettleBudgetReservations(ctx context.Context, now time.Time) ([]budget.ProductionBudget, error) {
	values, err := s.ProductionBudgetStore.SettleBudgetReservations(ctx, now)
	if err != nil {
		return values, err
	}
	return s.plans(values), nil
}

// ProductionBudgetSummaries is the optional batch read of the session list.
// Without a wrapped store that provides it, it reports no plans, as the grant
// service already does for a store without the batch interface.
func (s resolvedPlanStore) ProductionBudgetSummaries(ctx context.Context, ownerID string,
	sessionIDs []string,
) (map[string]budget.Summary, error) {
	summaries := map[string]budget.Summary{}
	store, ok := s.ProductionBudgetStore.(productionBudgetSummariesStore)
	if !ok {
		return summaries, nil
	}
	stored, err := store.ProductionBudgetSummaries(ctx, ownerID, sessionIDs)
	if err != nil || len(stored) == 0 {
		return stored, err
	}
	for sessionID, summary := range stored {
		summaries[sessionID] = s.summary(summary)
	}
	return summaries, nil
}

// CompleteFinishedBudgets is the optional completion write (#410). Without a
// wrapped completion store there is nothing to complete.
func (s resolvedPlanStore) CompleteFinishedBudgets(ctx context.Context, now time.Time) ([]budget.ProductionBudget, error) {
	store, ok := s.ProductionBudgetStore.(budgetCompletionStore)
	if !ok {
		return nil, nil
	}
	values, err := store.CompleteFinishedBudgets(ctx, now)
	if err != nil {
		return values, err
	}
	return s.plans(values), nil
}

// CompleteFinishedBudget is the same for one plan of one owner.
func (s resolvedPlanStore) CompleteFinishedBudget(ctx context.Context, ownerID, budgetID string, now time.Time) ([]budget.ProductionBudget, error) {
	store, ok := s.ProductionBudgetStore.(budgetCompletionStore)
	if !ok {
		return nil, nil
	}
	values, err := store.CompleteFinishedBudget(ctx, ownerID, budgetID, now)
	if err != nil {
		return values, err
	}
	return s.plans(values), nil
}

// ProductionBudgetsAwaitingNotice is the optional notice retry read. Without a
// wrapped notice store there is nothing to retry.
func (s resolvedPlanStore) ProductionBudgetsAwaitingNotice(ctx context.Context, limit int) ([]budget.ProductionBudget, error) {
	store, ok := s.ProductionBudgetStore.(BudgetNoticeStore)
	if !ok {
		return nil, nil
	}
	values, err := store.ProductionBudgetsAwaitingNotice(ctx, limit)
	if err != nil {
		return values, err
	}
	return s.plans(values), nil
}

// MarkProductionBudgetNoticeSent records a posted notice through the wrapped
// notice store.
func (s resolvedPlanStore) MarkProductionBudgetNoticeSent(ctx context.Context, ownerID, budgetID string, now time.Time) error {
	store, ok := s.ProductionBudgetStore.(BudgetNoticeStore)
	if !ok {
		return nil
	}
	return store.MarkProductionBudgetNoticeSent(ctx, ownerID, budgetID, now)
}

// ReleaseUnboundBudgetReservation releases an approved-but-unbound reservation
// through the wrapped releaser.
func (s resolvedPlanStore) ReleaseUnboundBudgetReservation(ctx context.Context, ownerID, requestID string) error {
	releaser, ok := s.ProductionBudgetStore.(BudgetReservationReleaser)
	if !ok {
		return nil
	}
	return releaser.ReleaseUnboundBudgetReservation(ctx, ownerID, requestID)
}
