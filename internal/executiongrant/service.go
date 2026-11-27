package executiongrant

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

const HumanBrowser = "human_browser"
const AgentPending = "agent_human_decision"

// AccessKey is the source of a grant a personal access key's call asked for
// (#332), for instance an external MCP client. No person decides it and no
// approval card exists for it: AccessKeyActor approves it, or denies it in
// place. See access_key.go.
const AccessKey = "access_key"

// Delivery is the source of a grant the platform asks for while keeping its own
// delivery promise (#457): a format a generation still owes is converted
// without anyone asking. No person decides it and no approval card exists for
// it, in any approval mode: the automatic completion's own ceiling is the only
// limit, and the 3D step count and the session budget stay untouched. See
// ApproveByPolicyDecision.
const Delivery = "delivery"

// DeliveryActorID is the Principal.ActorID of a delivery grant: the audit must
// read as the platform keeping its promise, never as a person's action.
const DeliveryActorID = "model-delivery"

// DeliveryActor is Grant.DecidedBy of the approval of a delivery grant. A
// person never has this actor ID.
const DeliveryActor = PolicyActorPrefix + "delivery"

// PolicyActorPrefix prefixes Grant.DecidedBy for an approval made by the
// session approval policy instead of a person. A human actor ID never has it.
const PolicyActorPrefix = "policy:"

// StoppedActor is Grant.DecidedBy of an approval that was denied by the system
// because the turn that asked for it was stopped before the approval card was
// detached from the turn. A person never has this actor ID.
const StoppedActor = "system:turn_stopped"

// CancelledByStop reports whether the system denied the grant because its
// trigger turn was stopped before the card was detached.
func (g Grant) CancelledByStop() bool { return g.Status == Denied && g.DecidedBy == StoppedActor }

// ExpiredActor is Grant.DecidedBy of an Agent approval the system denied
// because nobody decided it within the Schema's pending_approvals expiry
// (#665). A person never has this actor ID.
const ExpiredActor = "system:approval_expired"

// ExpiredUndecided reports whether the system denied the grant because its
// approval expired.
func (g Grant) ExpiredUndecided() bool { return g.Status == Denied && g.DecidedBy == ExpiredActor }

// ExpiredGrantStore denies an expired, still pending Agent grant as the system
// (ExpiredActor, audited). A grant that is no longer pending is returned as it
// is, unchanged.
type ExpiredGrantStore interface {
	ExpireExecutionGrant(ctx context.Context, ownerID, requestID string) (Grant, error)
}

// ExpirePending denies a pending Agent grant whose approval expired, so the
// grant's state says what its card says: it can no longer be approved (#665).
// A grant that has not expired, and a store without the capability, are left
// untouched.
func (s Service) ExpirePending(ctx context.Context, grant Grant) (Grant, error) {
	if !s.Expired(grant) {
		return grant, nil
	}
	store, ok := s.Grants.(ExpiredGrantStore)
	if !ok {
		return grant, nil
	}
	return store.ExpireExecutionGrant(ctx, grant.OwnerID, grant.RequestID)
}

// ApprovedByPolicy reports whether the grant was approved by session policy.
func (g Grant) ApprovedByPolicy() bool {
	return strings.HasPrefix(g.DecidedBy, PolicyActorPrefix)
}

// AgentOriginated reports whether the Agent started the step the grant
// authorizes: its own tool step (AgentPending), or a step of a processing plan
// an Agent tool created. Such a plan asks for its steps as browser grants that
// carry the plan's budget eligibility, and only an Agent-created plan's steps
// are ever budget eligible (#120). The Agent's approval rules (extra spend,
// unpriced steps, the recharge guard) apply to all of them (#665).
func (g Grant) AgentOriginated() bool {
	return g.Source == AgentPending || (g.Source == HumanBrowser && g.BudgetEligible)
}

// RechargeGuard names the newest Task of the session that failed after the
// upstream charged for it and whose result was never stored, of the given
// capability, that nothing has answered since (#294); "" when there is none.
// A step the Agent started that would repeat such a Task charges again, so the
// policy never approves it: it waits for a person on its card (#665).
type RechargeGuard func(ctx context.Context, ownerID, sessionID, capability string) (string, error)

// SessionApprovedSteps counts the session's grants that a person or the policy
// approved (approved, claimed or bound), delivery grants excluded (#702). It is
// the count the approval mode's checkpoint window is measured against.
type SessionApprovedSteps func(ctx context.Context, ownerID, sessionID string) (int, error)

// Principal is supplied by authenticated server middleware, never decoded from
// the grant request body. Other client sources require a separate identity and
// decision policy before they can use this service.
type Principal struct {
	OwnerID string
	ActorID string
	Source  string
}

type PreparedSnapshot struct {
	NativeQuote      *schema.NativeQuote `json:"native_quote,omitempty"`
	ID               string              `json:"id"`
	OwnerID          string              `json:"owner_id"`
	SessionID        string              `json:"session_id"`
	OfferingID       string              `json:"offering_id"`
	OfferingRevision string              `json:"offering_revision"`
	InputHash        string              `json:"input_hash"`
	Capability       string              `json:"capability"`
	// ExtraCredits is the extra credits the frozen input incurs; a step that
	// incurs some is the person's to approve in manual mode, whatever its
	// capability.
	ExtraCredits int `json:"extra_credits,omitempty"`
	// EstimatedCredits is the server's quote for this frozen input (#103
	// budget). It is part of the input hash; zero on records written before
	// the field existed.
	EstimatedCredits int `json:"estimated_credits,omitempty"`
	// CreditsMetered is true when the Offering bills in credits. With a zero
	// estimate it marks an input the server cannot price; a budget plan must
	// leave that step to a person (#103 review B1).
	CreditsMetered bool `json:"credits_metered,omitempty"`
}

type PreparedReader interface {
	GetExecutionPreparedSnapshot(context.Context, string, string) (PreparedSnapshot, error)
}

type PrepareCommand struct {
	Harness harness.Binding `json:"harness,omitempty"`
	Principal
	SessionID         string
	OfferingID        string
	OfferingRevision  string
	PreparedInputID   string
	PreparedInputHash string
	IdempotencyKey    string
	OperationID       string
	PlanStepID        string
	TriggerTurnID     string
	// BudgetEligible marks a step the Agent started that may be approved by an
	// active production plan instead of a per-step human decision (#103). It
	// is stored with the grant; browser-started steps are never eligible.
	BudgetEligible bool
	// AccessKeyID names the personal access key of a Source AccessKey grant
	// (#332); empty for every other source.
	AccessKeyID string
}

// SessionModeReader returns the owner's approval mode for one of its sessions.
type SessionModeReader interface {
	SessionApprovalMode(ctx context.Context, owner, session string) (string, error)
}

// Service prepares and decides grants. Policy and Modes enable approval by
// session policy; without them every grant waits for a human.
type Service struct {
	Grants   Repository
	Prepared PreparedReader
	Policy   approvalpolicy.Policy
	Modes    SessionModeReader
	// Budgets stores production plans and reservations (#103). Without it the
	// budget mode behaves like manual: every Agent step waits for a person.
	Budgets ProductionBudgetStore
	// PendingTTL is how long an Agent approval stays approvable (Schema
	// chat.pending_approvals.ttl_seconds). Zero means it never expires.
	PendingTTL time.Duration
	// Clock is the time source for expiry; nil means time.Now.
	Clock func() time.Time
	// Recharge is the #294 recharge guard every policy decision of an
	// Agent-originated step consults (three-view views and their continuation,
	// Agent processing-plan steps, #665). Nil means no guard.
	Recharge RechargeGuard
	// SessionPaidSteps counts the session's approved steps for the checkpoint
	// window of the auto and budget modes (#702). Nil means no checkpoint.
	SessionPaidSteps SessionApprovedSteps
}

func (s Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

// ExpiresAt is when a pending Agent approval stops being approvable; the zero
// time when approvals do not expire.
func (s Service) ExpiresAt(g Grant) time.Time {
	if s.PendingTTL <= 0 {
		return time.Time{}
	}
	return g.PendingSince().Add(s.PendingTTL)
}

// ExpiryCutoff is the moment before which a pending Agent approval has already
// expired: one that started waiting at or before it can no longer be approved.
// It is the zero time when approvals do not expire.
func (s Service) ExpiryCutoff() time.Time {
	if s.PendingTTL <= 0 {
		return time.Time{}
	}
	return s.now().Add(-s.PendingTTL)
}

// Expired reports whether an Agent approval waited past the Schema's expiry.
// Only a pending Agent grant can expire; a decided one is never undone.
func (s Service) Expired(g Grant) bool {
	return s.PendingTTL > 0 && g.Source == AgentPending && g.Status == Pending &&
		!s.now().Before(g.PendingSince().Add(s.PendingTTL))
}

func (s Service) Prepare(ctx context.Context, cmd PrepareCommand) (Grant, error) {
	if !cmd.Harness.Valid() || s.Grants == nil || s.Prepared == nil || cmd.OwnerID == "" || cmd.ActorID == "" ||
		(cmd.Source != HumanBrowser && cmd.Source != AgentPending && cmd.Source != AccessKey && cmd.Source != Delivery) ||
		cmd.SessionID == "" || cmd.PreparedInputID == "" {
		return Grant{}, ErrInvalid
	}
	if (cmd.Source == AgentPending && cmd.TriggerTurnID == "") ||
		(cmd.Source != AgentPending && cmd.TriggerTurnID != "") {
		return Grant{}, ErrInvalid
	}
	// The key that asked belongs to an access-key grant and to nothing else;
	// no production plan ever approves such a grant.
	if (cmd.Source == AccessKey) != (cmd.AccessKeyID != "") || (cmd.Source == AccessKey && cmd.BudgetEligible) {
		return Grant{}, ErrInvalid
	}
	// A delivery grant is never budget eligible: its money is the automatic
	// completion's own per-task ceiling, not the session's plan (#457 D7).
	if cmd.Source == Delivery && (cmd.ActorID != DeliveryActorID || cmd.BudgetEligible) {
		return Grant{}, ErrInvalid
	}
	snapshot, err := s.Prepared.GetExecutionPreparedSnapshot(ctx, cmd.OwnerID, cmd.PreparedInputID)
	if err != nil {
		return Grant{}, err
	}
	if snapshot.ID != cmd.PreparedInputID || snapshot.OwnerID != cmd.OwnerID ||
		snapshot.SessionID != cmd.SessionID || snapshot.OfferingID != cmd.OfferingID ||
		snapshot.OfferingRevision != cmd.OfferingRevision || snapshot.InputHash != cmd.PreparedInputHash {
		return Grant{}, ErrConflict
	}
	identity := Identity{
		OwnerID: cmd.OwnerID, SessionID: cmd.SessionID,
		OfferingID: snapshot.OfferingID, OfferingRevision: snapshot.OfferingRevision,
		PreparedInputID: snapshot.ID, PreparedInputHash: snapshot.InputHash,
		IdempotencyKey: cmd.IdempotencyKey, OperationID: cmd.OperationID,
		PlanStepID: cmd.PlanStepID, Harness: cmd.Harness,
	}
	if cmd.Source == AccessKey {
		store, ok := s.Grants.(AccessKeyGrantStore)
		if !ok {
			return Grant{}, ErrInvalid
		}
		return store.PreparePendingForAccessKey(ctx, identity, cmd.AccessKeyID)
	}
	if cmd.BudgetEligible {
		// Budget eligibility is declared by the server-side caller: an Agent
		// step of a chat turn, or a step of an Agent-created processing plan.
		// A browser-started step never sets it.
		store, ok := s.Grants.(BudgetGrantStore)
		if !ok {
			return Grant{}, ErrInvalid
		}
		return store.PreparePendingWithBudget(ctx, identity, cmd.Source, cmd.TriggerTurnID, true)
	}
	return s.Grants.PreparePending(ctx, identity, cmd.Source, cmd.TriggerTurnID)
}

// Decide requires the human's separate action and a server-authenticated
// principal bound to the same owner as the pending request.
func (s Service) Decide(ctx context.Context, principal Principal, requestID string, decision Decision) (Grant, error) {
	if s.Grants == nil || principal.OwnerID == "" || principal.ActorID == "" ||
		principal.Source != HumanBrowser || requestID == "" {
		return Grant{}, ErrInvalid
	}
	g, err := s.Grants.GetExecutionGrant(ctx, principal.OwnerID, requestID)
	if err != nil {
		return Grant{}, err
	}
	if g.OwnerID != principal.OwnerID || (g.Source != HumanBrowser && g.Source != AgentPending) {
		return Grant{}, ErrConflict
	}
	// An expired approval stays refused as expired after the sweep denied it
	// (ExpiredActor, #665).
	if decision == Approve && (s.Expired(g) || g.ExpiredUndecided()) {
		return Grant{}, ErrExpired
	}
	return s.Grants.DecideExecutionGrant(ctx, principal.OwnerID, requestID, principal.ActorID, decision)
}

// ApproveByPolicy approves a pending grant without a human decision when the
// session mode and the frozen step capability allow it. The capability comes
// from the server-held prepared input, never from the caller. Owner, state and
// frozen identity must all match; a grant that needs a human is left pending
// and ErrHumanRequired is returned. A repeated call for a grant already
// approved by the same policy actor returns it unchanged.
func (s Service) ApproveByPolicy(ctx context.Context, ownerID, requestID, mode string) (Grant, error) {
	result, err := s.ApproveByPolicyDecision(ctx, ownerID, requestID, mode)
	return result.Grant, err
}

// ApproveByPolicyDecision is ApproveByPolicy with the budget context the
// Agent's tool result reports (#103): the outcome, why a step was left to a
// person, and the session plan summary that was read. In the internal steps
// mode (#330) a 3D step over the asset's cap comes back as ErrStepCapReached
// with the denied grant: nothing was approved.
func (s Service) ApproveByPolicyDecision(ctx context.Context, ownerID, requestID, mode string) (PolicyResult, error) {
	if s.Grants == nil || s.Prepared == nil || ownerID == "" || requestID == "" {
		return PolicyResult{Grant: Grant{}, Outcome: PolicyOutcomeNone}, ErrInvalid
	}
	normalized := s.Policy.NormalizeMode(mode)
	actor := PolicyActorPrefix + normalized
	grant, err := s.Grants.GetExecutionGrant(ctx, ownerID, requestID)
	if err != nil {
		return PolicyResult{Grant: Grant{}, Outcome: PolicyOutcomeNone}, err
	}
	if grant.OwnerID != ownerID ||
		(grant.Source != HumanBrowser && grant.Source != AgentPending && grant.Source != Delivery) {
		return PolicyResult{Grant: Grant{}, Outcome: PolicyOutcomeNone}, ErrConflict
	}
	// A delivery grant is the platform keeping its own promise (#457 D2): every
	// approval mode decides it here. It is never left to a person (no card), it
	// never counts a 3D step (#330), and it never touches the session budget
	// (D7) — its only limit is the automatic completion's own ceiling.
	if grant.Source == Delivery {
		return s.approveDeliveryPolicy(ctx, grant)
	}
	if grant.Status != Pending {
		if (grant.DecidedBy == actor || grant.ApprovedByBudget()) &&
			(grant.Status == Approved || grant.Status == Claimed || grant.Status == Bound) {
			return PolicyResult{Grant: grant, Outcome: PolicyOutcomeApproved}, nil
		}
		if normalized == approvalpolicy.ModeSteps && grant.StepCapReached() {
			// The steps mode already refused this grant: repeat that answer.
			return PolicyResult{Grant: grant, Outcome: PolicyOutcomeStepCap}, ErrStepCapReached
		}
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeNone}, ErrState
	}
	snapshot, err := s.Prepared.GetExecutionPreparedSnapshot(ctx, ownerID, grant.PreparedInputID)
	if err != nil {
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeNone}, err
	}
	if snapshot.ID != grant.PreparedInputID || snapshot.OwnerID != ownerID ||
		snapshot.SessionID != grant.SessionID || snapshot.OfferingID != grant.OfferingID ||
		snapshot.OfferingRevision != grant.OfferingRevision || snapshot.InputHash != grant.PreparedInputHash {
		return PolicyResult{Grant: Grant{}, Outcome: PolicyOutcomeNone}, ErrConflict
	}
	// A step the Agent started that repeats a charged-but-unstored attempt of
	// the session charges again: it is never the policy's to approve, whatever
	// the mode, plan or step setting says (#294, #665).
	if grant.AgentOriginated() && s.Recharge != nil {
		taskID, err := s.Recharge(ctx, ownerID, grant.SessionID, snapshot.Capability)
		if err != nil {
			return PolicyResult{Grant: grant, Outcome: PolicyOutcomeNone}, err
		}
		if taskID != "" {
			return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman}, ErrHumanRequired
		}
	}
	// The internal steps mode decides on its own: nothing waits for a person
	// and the owner's 3D step setting is the only limit (#330).
	if normalized == approvalpolicy.ModeSteps {
		return s.applyStepPolicy(ctx, grant, snapshot)
	}
	// Every window of approved steps of the session, the next step asks a
	// person once, whatever the mode or a plan would decide (#702). Approving
	// it moves the count on, so the window starts again; denying it leaves the
	// count where it is, so the step after it asks again.
	if s.SessionPaidSteps != nil && (normalized == approvalpolicy.ModeAuto || normalized == approvalpolicy.ModeBudget) {
		approvedSteps, err := s.SessionPaidSteps(ctx, ownerID, grant.SessionID)
		if err != nil {
			return PolicyResult{Grant: grant, Outcome: PolicyOutcomeNone}, err
		}
		if s.Policy.CheckpointDue(normalized, approvedSteps) {
			return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman}, ErrHumanRequired
		}
	}
	// A budget-mode Agent step is decided against the approved plan before the
	// per-capability rules. A browser-started step and a grant created before
	// budget eligibility existed still follow manual's rules below.
	noPlan := false
	var planSummary *budget.Summary
	if normalized == approvalpolicy.ModeBudget && grant.BudgetEligible {
		result, handled, budgetErr := s.applyBudgetPolicy(ctx, grant, snapshot)
		if budgetErr != nil {
			return result, budgetErr
		}
		if handled {
			if result.Outcome == PolicyOutcomeHuman {
				return result, ErrHumanRequired
			}
			return result, nil
		}
		// No active plan: the step follows manual's rules. If those leave it to
		// a person, the tool result says why the budget did not decide it.
		noPlan = true
		planSummary = result.Budget
	}
	// A step costs what it costs, not what capability it happens to be: in
	// manual mode the policy's per-step credit threshold cards the expensive
	// processing steps that the capability list does not name (#490). The
	// estimate is the frozen one the approval card shows.
	if approvalpolicy.RequiresHumanForCredits(s.Policy, mode, snapshot.Capability, snapshot.EstimatedCredits) {
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman, Reason: budgetReason(noPlan), Budget: planSummary}, ErrHumanRequired
	}
	// An Agent step that spends extra credits is the person's to approve in
	// manual mode, even on a capability that is otherwise automatic: the
	// extra part is shown on the approval card and agreed to there.
	extra := snapshot.ExtraCredits > 0
	if snapshot.NativeQuote != nil {
		extra, err = snapshot.NativeQuote.HasExtra()
		if err != nil {
			return PolicyResult{Grant: Grant{}, Outcome: PolicyOutcomeNone}, ErrConflict
		}
	}
	// A step of an Agent-created processing plan is the Agent's step too, even
	// though the plan asks for it as a browser grant (#665).
	if grant.AgentOriginated() && approvalpolicy.RequiresHumanForExtraSpend(s.Policy, mode, extra) {
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman, Reason: budgetReason(noPlan), Budget: planSummary}, ErrHumanRequired
	}
	// An Agent step the server cannot price (an Offering that bills in credits
	// with no estimate and no native quote) cannot be judged against the manual
	// threshold: a zero estimate is "unknown", not "free", so it waits for a
	// person wherever the threshold applies (#665).
	if grant.AgentOriginated() && snapshot.CreditsMetered && snapshot.EstimatedCredits <= 0 &&
		snapshot.NativeQuote == nil && s.Policy.ManualCreditsThreshold > 0 &&
		normalized != approvalpolicy.ModeAuto {
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman, Reason: budgetReason(noPlan), Budget: planSummary}, ErrHumanRequired
	}
	approved, err := s.Grants.ApproveExecutionGrantByPolicy(ctx, ownerID, requestID, actor)
	if err != nil {
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeNone}, err
	}
	return PolicyResult{Grant: approved, Outcome: PolicyOutcomeApproved}, nil
}

// ContinuationBasis says what lets the server submit the next step of a series
// (the views of a three-view group) without a person.
type ContinuationBasis string

const (
	// ContinuationNone: every step waits for a person.
	ContinuationNone ContinuationBasis = ""
	// ContinuationBudget: the session's approved production plan covers the steps.
	ContinuationBudget ContinuationBasis = "budget"
	// ContinuationAuto: the session approves every paid step by itself (the auto
	// mode), so the next step is exactly what a person clicking it would start.
	ContinuationAuto ContinuationBasis = "auto"
)

// Continuation is the basis on which a series of count steps of the capability
// at the Offering continues without a person. The auto mode needs no plan; the
// budget mode needs a plan that covers the steps (BudgetContinuation); the
// strict (manual) mode never continues by itself. A missing mode reader answers
// none.
func (s Service) Continuation(ctx context.Context, ownerID, sessionID, capability, offeringID string,
	count int,
) (ContinuationBasis, error) {
	if ownerID == "" || sessionID == "" || capability == "" || count < 1 || s.Modes == nil {
		return ContinuationNone, nil
	}
	mode, err := s.Modes.SessionApprovalMode(ctx, ownerID, sessionID)
	if err != nil {
		return ContinuationNone, err
	}
	switch s.Policy.NormalizeMode(mode) {
	case approvalpolicy.ModeAuto:
		return ContinuationAuto, nil
	case approvalpolicy.ModeBudget:
		covered, err := s.BudgetContinuation(ctx, ownerID, sessionID, capability, offeringID, count)
		if err != nil || !covered {
			return ContinuationNone, err
		}
		return ContinuationBudget, nil
	}
	return ContinuationNone, nil
}

// BudgetContinuation reports whether the session's active, approved production
// plan explicitly covers count steps of the capability at the given Offering.
// Only the budget mode answers true here: the auto mode continues on its own
// basis (Continuation, #663) and the strict mode keeps its per-step behavior
// (#243). A missing mode reader, plan store or plan, a
// plan that is not active, or a mode that is not budget answers false.
func (s Service) BudgetContinuation(ctx context.Context, ownerID, sessionID, capability, offeringID string,
	count int,
) (bool, error) {
	if ownerID == "" || sessionID == "" || capability == "" || count < 1 || s.Modes == nil || s.Budgets == nil {
		return false, nil
	}
	mode, err := s.Modes.SessionApprovalMode(ctx, ownerID, sessionID)
	if err != nil {
		return false, err
	}
	if s.Policy.NormalizeMode(mode) != approvalpolicy.ModeBudget {
		return false, nil
	}
	summary, found, err := s.Budgets.ProductionBudgetSummary(ctx, ownerID, sessionID)
	if err != nil || !found {
		return false, err
	}
	if summary.State != budget.StateActive {
		return false, nil
	}
	return summary.Plan.Covers(capability, offeringID, count), nil
}

// ApplyPolicy looks up the session approval mode and approves the grant by
// policy when no human is required. Every grant creation site calls it right
// after Prepare. A grant that is not pending, or that needs a human, is
// returned unchanged without error. A failed mode lookup is an error: the
// caller must not guess a mode.
func (s Service) ApplyPolicy(ctx context.Context, grant Grant) (Grant, error) {
	result, err := s.ApplyPolicyDecision(ctx, grant)
	return result.Grant, err
}

// ApplyPolicyDecision is ApplyPolicy with the budget context of the decision
// (#103). Budget reasons come back with a nil error when the grant was left
// pending for a human; a real failure comes back with the error.
func (s Service) ApplyPolicyDecision(ctx context.Context, grant Grant) (PolicyResult, error) {
	if grant.Status != Pending || s.Modes == nil {
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeNone}, nil
	}
	mode, err := s.Modes.SessionApprovalMode(ctx, grant.OwnerID, grant.SessionID)
	if err != nil {
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeNone}, err
	}
	result, err := s.ApproveByPolicyDecision(ctx, grant.OwnerID, grant.RequestID, mode)
	if err == nil {
		return result, nil
	}
	if errors.Is(err, ErrHumanRequired) {
		result.Grant = grant
		return result, nil
	}
	if errors.Is(err, ErrStepCapReached) {
		// The steps mode refused the step: the denied grant is the answer, and
		// no card follows.
		return result, nil
	}
	return PolicyResult{Grant: grant, Outcome: PolicyOutcomeNone, Reason: result.Reason, Budget: result.Budget}, err
}

// approveDeliveryPolicy approves a delivery grant with no human decision, in
// every approval mode. It rechecks the frozen identity the same way the mode
// policies do, so a grant whose prepared input no longer matches is a conflict
// rather than an approval, and it records the delivery actor so the audit says
// who let the platform spend.
func (s Service) approveDeliveryPolicy(ctx context.Context, grant Grant) (PolicyResult, error) {
	if grant.Status != Pending {
		if grant.Status == Approved && grant.DecidedBy == DeliveryActor {
			return PolicyResult{Grant: grant, Outcome: PolicyOutcomeApproved}, nil
		}
		return PolicyResult{Grant: Grant{}, Outcome: PolicyOutcomeNone}, ErrState
	}
	snapshot, err := s.Prepared.GetExecutionPreparedSnapshot(ctx, grant.OwnerID, grant.PreparedInputID)
	if err != nil {
		return PolicyResult{Grant: Grant{}, Outcome: PolicyOutcomeNone}, err
	}
	if snapshot.ID != grant.PreparedInputID || snapshot.OwnerID != grant.OwnerID ||
		snapshot.SessionID != grant.SessionID || snapshot.OfferingID != grant.OfferingID ||
		snapshot.OfferingRevision != grant.OfferingRevision || snapshot.InputHash != grant.PreparedInputHash {
		return PolicyResult{Grant: Grant{}, Outcome: PolicyOutcomeNone}, ErrConflict
	}
	approved, err := s.Grants.ApproveExecutionGrantByPolicy(ctx, grant.OwnerID, grant.RequestID, DeliveryActor)
	if err != nil {
		return PolicyResult{Grant: Grant{}, Outcome: PolicyOutcomeNone}, err
	}
	return PolicyResult{Grant: approved, Outcome: PolicyOutcomeApproved}, nil
}

// budgetReason names the budget reason only when a budget-mode step without an
// active plan was left to a person by manual's rules.
func budgetReason(noPlan bool) string {
	if noPlan {
		return BudgetReasonNoPlan
	}
	return ""
}
