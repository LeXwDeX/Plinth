package executiongrant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
)

// Budget reasons and errors re-exported for callers that only import this
// package. The budget package owns their values.
const (
	BudgetReasonNoPlan   = budget.ReasonNoPlan
	BudgetReasonOffPlan  = budget.ReasonOffPlan
	BudgetReasonOver     = budget.ReasonOverBudget
	BudgetReasonUnpriced = budget.ReasonUnpriced
	BudgetActorPrefix    = "budget:"
	BudgetStatePending   = budget.StatePending
	BudgetStateActive    = budget.StateActive
	BudgetStateExhausted = budget.StateExhausted
	BudgetStateClosed    = budget.StateClosed
	BudgetStateDenied    = budget.StateDenied
	// AbandonedActor is Grant.DecidedBy of an approved grant denied by the
	// system because the operation it authorized ended without binding a Task;
	// its budget reservation is released in the same transaction.
	AbandonedActor = "system:budget_abandoned"
)

var (
	ErrNoBudget    = budget.ErrNotFound
	ErrBudgetGone  = budget.ErrInactive
	ErrBudgetOver  = budget.ErrOverLimit
	ErrBudgetState = budget.ErrState
)

// Policy outcomes of one grant evaluation. Approved means the grant is
// approved; Human means it stays pending for a person; None means this call
// changed nothing (the grant was not pending, or no mode reader is wired).
const (
	PolicyOutcomeApproved = "approved"
	PolicyOutcomeHuman    = "human"
	PolicyOutcomeNone     = "none"
)

// PolicyResult is what ApplyPolicyDecision learned, including the reason and
// budget the Agent's tool result reports when a budget-mode step was left to a
// person (#103).
type PolicyResult struct {
	Grant   Grant
	Outcome string
	// Reason is one of BudgetReasonNoPlan, BudgetReasonOffPlan,
	// BudgetReasonOver or BudgetReasonUnpriced for a budget-mode step left to
	// a person, else empty.
	Reason string
	// Budget is the session's current plan summary when the evaluation read
	// one; nil when there is no plan or the mode did not use the budget.
	Budget *budget.Summary
}

// ProductionBudgetStore persists production plans and their reservations.
// Only executiongrant and processing write through it; chat and the transport
// read the public summary through Service.
type ProductionBudgetStore interface {
	// CreateProductionBudget stores a pending plan and closes any other
	// pending plan of the session in the same transaction. Replaying the same
	// ID returns the stored plan unchanged.
	CreateProductionBudget(context.Context, budget.ProductionBudget) (budget.ProductionBudget, error)
	// DecideProductionBudget approves or denies a pending plan. Approval
	// closes the session's formerly active plan and releases its reservations
	// that are not bound to a Task. Denial releases nothing: a pending plan
	// never reserved credits.
	DecideProductionBudget(ctx context.Context, ownerID, budgetID, actor string, approve bool, now time.Time) (budget.ProductionBudget, error)
	GetProductionBudget(context.Context, string, string) (budget.ProductionBudget, error)
	// PendingProductionBudgets lists the session's undecided plans, newest
	// first. The caller checks expiry.
	PendingProductionBudgets(context.Context, string, string) ([]budget.ProductionBudget, error)
	// CurrentProductionBudget returns the session's active or exhausted
	// plan, if any.
	CurrentProductionBudget(context.Context, string, string) (budget.ProductionBudget, bool, error)
	// ProductionBudgetSummary returns the current plan with its settled
	// reserved/charged totals.
	ProductionBudgetSummary(context.Context, string, string) (budget.Summary, bool, error)
	// BudgetSummaryForGrant returns the summary of the plan that reserved the
	// grant, if any.
	BudgetSummaryForGrant(context.Context, string, string) (budget.Summary, bool, error)
	// ExpireProductionBudgets refuses every pending plan created at or before
	// cutoff, and returns them so the caller can post the expiry message.
	ExpireProductionBudgets(context.Context, time.Time) ([]budget.ProductionBudget, error)
	// SettleBudgetReservations marks reservations whose Task has ended as
	// charged or released, releases reservations whose grant was denied, and
	// marks an active plan exhausted once its charged totals reach the limit
	// of every unit. It returns the plans that became exhausted and need their
	// one notice posted.
	SettleBudgetReservations(context.Context, time.Time) ([]budget.ProductionBudget, error)
}

// BudgetGrantStore is the data layer's atomic budget authorization: the grant
// approval and its reservation commit in one transaction.
type BudgetGrantStore interface {
	PreparePendingWithBudget(ctx context.Context, identity Identity, source, triggerTurnID string, budgetEligible bool) (Grant, error)
	// ApproveExecutionGrantWithBudget settles ended Tasks of the same plan,
	// re-checks what remains in the amount's unit, inserts the reservation and
	// approves the grant in one BEGIN IMMEDIATE transaction. ErrBudgetOver
	// means the amount does not fit in its unit (or the plan has no limit in
	// it); a zero amount fits any plan. ErrBudgetGone means the plan is no
	// longer active.
	ApproveExecutionGrantWithBudget(ctx context.Context, owner, requestID, budgetID, actor string, amount budget.Amount) (Grant, error)
}

// BudgetReservationReleaser is the optional data-layer part that gives back an
// approved-but-unbound budget reservation when its operation ends without a
// Task. The grant itself is denied in the same transaction, so a later claim or
// policy replay cannot spend the plan without a reservation (#103 review B2).
type BudgetReservationReleaser interface {
	ReleaseUnboundBudgetReservation(ctx context.Context, ownerID, requestID string) error
}

// BudgetAmount is the frozen quote a production plan reserves for this
// prepared input, in the step's own unit (#120): the native quote's total when
// the input carries one, else its legacy integer estimate. A plan compares it
// only with what remains in the same unit.
func (snapshot PreparedSnapshot) BudgetAmount() budget.Amount {
	return budget.QuoteAmount(snapshot.NativeQuote, snapshot.EstimatedCredits)
}

// AbandonBudgetReservation releases the reservation of an approved grant whose
// operation ended without binding a Task (a permanently rejected submit, or a
// plan step that will never submit this authorization). It is idempotent: a
// grant that is not approved, already bound, or has no reservation is left
// untouched. Deployments without plan storage have nothing to release.
func (s Service) AbandonBudgetReservation(ctx context.Context, ownerID, requestID string) error {
	if ownerID == "" || requestID == "" {
		return ErrInvalid
	}
	releaser, ok := s.Budgets.(BudgetReservationReleaser)
	if !ok {
		return nil
	}
	return releaser.ReleaseUnboundBudgetReservation(ctx, ownerID, requestID)
}

// BudgetNoticeStore records which exhausted or expired plan already posted its
// one chat notice. The notice follows the state transition, so a failed post is
// retried by a later sweep until the date is recorded (#103 review B3).
type BudgetNoticeStore interface {
	ProductionBudgetsAwaitingNotice(ctx context.Context, limit int) ([]budget.ProductionBudget, error)
	MarkProductionBudgetNoticeSent(ctx context.Context, ownerID, budgetID string, now time.Time) error
}

// ProductionBudgetsAwaitingNotice returns the plans whose notice has not been
// recorded yet. Deployments without notice storage return none.
func (s Service) ProductionBudgetsAwaitingNotice(ctx context.Context, limit int) ([]budget.ProductionBudget, error) {
	store, ok := s.Budgets.(BudgetNoticeStore)
	if !ok {
		return nil, nil
	}
	return store.ProductionBudgetsAwaitingNotice(ctx, limit)
}

// MarkProductionBudgetNoticeSent records a posted notice. Deployments without
// notice storage have nothing to record.
func (s Service) MarkProductionBudgetNoticeSent(ctx context.Context, ownerID, budgetID string) error {
	if ownerID == "" || budgetID == "" {
		return ErrInvalid
	}
	store, ok := s.Budgets.(BudgetNoticeStore)
	if !ok {
		return nil
	}
	return store.MarkProductionBudgetNoticeSent(ctx, ownerID, budgetID, s.now())
}

// CreateProductionPlan stores a new pending plan for the owner's session.
// The caller has validated the plan shape and its per-unit limits against the
// Schema; every unit a step is quoted in must be covered by its own limit.
// requestKey is the tool call's stable identity: the plan ID is derived from
// it, so a replayed call returns the plan it already created instead of
// opening a second card. messageID and callID anchor the pending card.
func (s Service) CreateProductionPlan(ctx context.Context, ownerID, sessionID, requestKey, messageID, callID string, plan budget.Plan, limits []budget.Amount) (budget.ProductionBudget, error) {
	if s.Budgets == nil || ownerID == "" || sessionID == "" || requestKey == "" {
		return budget.ProductionBudget{}, ErrInvalid
	}
	if strings.TrimSpace(plan.Title) == "" || len(plan.Steps) == 0 {
		return budget.ProductionBudget{}, ErrInvalid
	}
	normalized, err := budget.NormalizeLimits(limits)
	if err != nil || len(plan.Uncovered(normalized)) > 0 {
		return budget.ProductionBudget{}, ErrInvalid
	}
	return s.Budgets.CreateProductionBudget(ctx, budget.ProductionBudget{
		ID: productionBudgetID(ownerID, sessionID, requestKey), OwnerID: ownerID,
		SessionID: sessionID, Plan: plan, Limits: normalized,
		State: budget.StatePending, CreatedAt: s.now(),
		MessageID: messageID, CallID: callID,
	})
}

// productionBudgetID is deterministic for one tool call identity and scoped by
// owner and session.
func productionBudgetID(ownerID, sessionID, requestKey string) string {
	sum := sha256.Sum256([]byte(ownerID + "\x00" + sessionID + "\x00" + requestKey))
	return "pb_" + hex.EncodeToString(sum[:])
}

// DecideBudget applies the owner's human decision to a pending plan. A plan
// past its TTL can no longer be approved. The actor is recorded as given.
func (s Service) DecideBudget(ctx context.Context, principal Principal, budgetID string, decision Decision) (budget.ProductionBudget, error) {
	if s.Budgets == nil || principal.OwnerID == "" || principal.ActorID == "" ||
		principal.Source != HumanBrowser || budgetID == "" {
		return budget.ProductionBudget{}, ErrInvalid
	}
	if decision != Approve && decision != Deny {
		return budget.ProductionBudget{}, ErrInvalid
	}
	production, err := s.Budgets.GetProductionBudget(ctx, principal.OwnerID, budgetID)
	if err != nil {
		return budget.ProductionBudget{}, err
	}
	if production.OwnerID != principal.OwnerID {
		return budget.ProductionBudget{}, ErrConflict
	}
	if production.State != budget.StatePending {
		return budget.ProductionBudget{}, ErrBudgetState
	}
	if s.planExpired(production) && decision == Approve {
		return budget.ProductionBudget{}, ErrExpired
	}
	return s.Budgets.DecideProductionBudget(ctx, principal.OwnerID, budgetID,
		principal.ActorID, decision == Approve, s.now())
}

// planExpired reports whether a pending plan waited past the approval TTL.
func (s Service) planExpired(production budget.ProductionBudget) bool {
	return s.PendingTTL > 0 && production.State == budget.StatePending &&
		!s.now().Before(production.CreatedAt.Add(s.PendingTTL))
}

// PendingBudgetExpiry is when a pending plan stops being approvable; the zero
// time when approvals do not expire.
func (s Service) PendingBudgetExpiry(production budget.ProductionBudget) time.Time {
	if s.PendingTTL <= 0 {
		return time.Time{}
	}
	return production.CreatedAt.Add(s.PendingTTL)
}

// PendingBudgetExpired reports whether a pending plan waited past the TTL.
func (s Service) PendingBudgetExpired(production budget.ProductionBudget) bool {
	return s.planExpired(production)
}

// PendingBudgets lists the session's undecided, unexpired plans, newest first.
func (s Service) PendingBudgets(ctx context.Context, ownerID, sessionID string) ([]budget.ProductionBudget, error) {
	if ownerID == "" || sessionID == "" {
		return nil, ErrInvalid
	}
	if s.Budgets == nil {
		// A deployment that predates production plans has none to list.
		return nil, nil
	}
	all, err := s.Budgets.PendingProductionBudgets(ctx, ownerID, sessionID)
	if err != nil {
		return nil, err
	}
	result := make([]budget.ProductionBudget, 0, len(all))
	for _, production := range all {
		if s.planExpired(production) {
			continue
		}
		result = append(result, production)
	}
	return result, nil
}

// GetBudget reads one plan of the owner. It is the owner-and-session check of
// the decision route: the caller compares the plan's session with the route's.
func (s Service) GetBudget(ctx context.Context, ownerID, budgetID string) (budget.ProductionBudget, error) {
	if s.Budgets == nil || ownerID == "" || budgetID == "" {
		return budget.ProductionBudget{}, ErrInvalid
	}
	return s.Budgets.GetProductionBudget(ctx, ownerID, budgetID)
}

// BudgetSummary returns the session's current plan summary.
func (s Service) BudgetSummary(ctx context.Context, ownerID, sessionID string) (budget.Summary, bool, error) {
	if s.Budgets == nil || ownerID == "" || sessionID == "" {
		return budget.Summary{}, false, nil
	}
	return s.Budgets.ProductionBudgetSummary(ctx, ownerID, sessionID)
}

// productionBudgetSummariesStore is the optional data-layer batch read of a
// session list's current plans. Deployments that predate it keep the
// per-session read through ProductionBudgetStore.
type productionBudgetSummariesStore interface {
	ProductionBudgetSummaries(context.Context, string, []string) (map[string]budget.Summary, error)
}

// ProductionBudgetSummaries reads the current plan summaries of many sessions
// in one store round trip, so listing sessions does not read one budget per
// session (#103 review C6). A session without a current plan is absent from
// the map. A deployment without plan storage, or whose store only implements
// the per-session read, returns an empty map and no error.
func (s Service) ProductionBudgetSummaries(ctx context.Context, ownerID string, sessionIDs []string) (map[string]budget.Summary, error) {
	summaries := make(map[string]budget.Summary)
	if s.Budgets == nil || ownerID == "" || len(sessionIDs) == 0 {
		return summaries, nil
	}
	store, ok := s.Budgets.(productionBudgetSummariesStore)
	if !ok {
		return summaries, nil
	}
	return store.ProductionBudgetSummaries(ctx, ownerID, sessionIDs)
}

// BudgetForGrant returns the plan summary that reserved the grant, if any.
func (s Service) BudgetForGrant(ctx context.Context, ownerID, requestID string) (budget.Summary, bool, error) {
	if s.Budgets == nil || ownerID == "" || requestID == "" {
		return budget.Summary{}, false, nil
	}
	return s.Budgets.BudgetSummaryForGrant(ctx, ownerID, requestID)
}

// ExpireBudgets refuses pending plans past cutoff and returns them so the
// caller can post the expiry message.
func (s Service) ExpireBudgets(ctx context.Context, cutoff time.Time) ([]budget.ProductionBudget, error) {
	if s.Budgets == nil {
		return nil, nil
	}
	return s.Budgets.ExpireProductionBudgets(ctx, cutoff)
}

// SettleBudgets settles ended Tasks' reservations and updates plan states. It
// returns the plans that just became exhausted so the caller can post their
// one notice.
func (s Service) SettleBudgets(ctx context.Context) ([]budget.ProductionBudget, error) {
	if s.Budgets == nil {
		return nil, nil
	}
	return s.Budgets.SettleBudgetReservations(ctx, s.now())
}

// applyBudgetPolicy evaluates one Agent step of a budget-mode session. The
// second result reports whether the plan decided the step. A session without
// an active plan is not handled here: it falls back to manual's per-capability
// rules, and only a step those rules leave to a person reports no_plan.
func (s Service) applyBudgetPolicy(ctx context.Context, grant Grant, snapshot PreparedSnapshot) (PolicyResult, bool, error) {
	if s.Budgets == nil {
		// A deployment without production-plan storage has no active plan; the
		// step follows manual's rules below.
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman}, false, nil
	}
	summary, found, err := s.Budgets.ProductionBudgetSummary(ctx, grant.OwnerID, grant.SessionID)
	if err != nil {
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman}, false, err
	}
	if !found {
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman}, false, nil
	}
	if summary.State != budget.StateActive && summary.State != budget.StateExhausted {
		// A pending, denied or closed plan is not a budget the session may
		// spend under; the step follows manual's rules.
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman, Budget: &summary}, false, nil
	}
	if !summary.Plan.Allows(snapshot.Capability, grant.OfferingID) {
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman, Reason: BudgetReasonOffPlan, Budget: &summary}, true, nil
	}
	if summary.State == budget.StateExhausted {
		// Once the approved budget is used up, no further Agent step runs
		// without a person, even one manual mode would approve on its own: the
		// person approved a total, and going past it needs a new decision.
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman, Reason: BudgetReasonOver, Budget: &summary}, true, nil
	}
	amount := snapshot.BudgetAmount()
	if snapshot.CreditsMetered && amount.Micros <= 0 {
		// A metered step without a server quote cannot be reserved: approving it
		// at zero would spend the plan without covering the step, so the person
		// decides it like any other unpriced charge (#103 review B1).
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman, Reason: BudgetReasonUnpriced, Budget: &summary}, true, nil
	}
	approver, ok := s.Grants.(BudgetGrantStore)
	if !ok {
		// Without the atomic reservation the plan cannot cover the step; leave
		// it to a person instead of approving outside the budget.
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman, Reason: BudgetReasonNoPlan, Budget: &summary}, true, nil
	}
	actor := BudgetActorPrefix + summary.ID
	approved, err := approver.ApproveExecutionGrantWithBudget(ctx, grant.OwnerID, grant.RequestID,
		summary.ID, actor, amount)
	if err == nil {
		after, _, summaryErr := s.Budgets.ProductionBudgetSummary(ctx, grant.OwnerID, grant.SessionID)
		if summaryErr != nil {
			return PolicyResult{Grant: approved, Outcome: PolicyOutcomeApproved}, true, nil
		}
		return PolicyResult{Grant: approved, Outcome: PolicyOutcomeApproved, Budget: &after}, true, nil
	}
	if errors.Is(err, ErrBudgetOver) {
		after, _, readErr := s.Budgets.ProductionBudgetSummary(ctx, grant.OwnerID, grant.SessionID)
		if readErr == nil {
			return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman, Reason: BudgetReasonOver, Budget: &after}, true, nil
		}
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman, Reason: BudgetReasonOver}, true, nil
	}
	if errors.Is(err, ErrBudgetGone) {
		after, foundAfter, readErr := s.Budgets.ProductionBudgetSummary(ctx, grant.OwnerID, grant.SessionID)
		if readErr == nil && foundAfter {
			return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman, Reason: BudgetReasonOver, Budget: &after}, true, nil
		}
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman, Reason: BudgetReasonNoPlan}, true, nil
	}
	return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman}, true, err
}
