package executiongrant

import (
	"context"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
)

// StepsActor is Grant.DecidedBy of a grant the internal steps mode decided
// (#330): approved, or refused because the asset used all its 3D steps. A
// person never has this actor ID.
const StepsActor = PolicyActorPrefix + approvalpolicy.ModeSteps

// StepCapReached reports whether the steps mode refused the grant because the
// asset used all the 3D steps its owner allows. It is the one way a denial by
// policy is told apart from a person's decision or a system cancellation
// (CancelledByStop): the Agent reads the step-cap text instead of the generic
// denial, with or without a replay of the same call.
func (g Grant) StepCapReached() bool { return g.Status == Denied && g.DecidedBy == StepsActor }

// PolicyOutcomeStepCap is the outcome of a grant the steps mode refused
// because its asset used all its 3D steps. The grant is denied in place; no
// approval card exists for it and no Task can be created from it.
const PolicyOutcomeStepCap = "step_cap"

// ErrStepCapReached is what ApproveByPolicyDecision returns, with the denied
// grant in the result, when the steps mode refused the grant. ApplyPolicyDecision
// turns it into a result without an error, like ErrHumanRequired.
var ErrStepCapReached = errors.New("execution grant: 3D step cap reached")

// StepSummary is the 3D step standing of one asset (a chat session) under its
// entry's setting. Used counts Tasks that were or may have been charged plus
// the approved steps that have no Task yet; it is only computed for the cap
// mode and is zero otherwise.
type StepSummary struct {
	Entry string
	Mode  string
	Cap   int
	Used  int
}

// StepRequest is what the data layer needs to decide one grant under the steps
// mode: the entry whose setting applies and the policy (the counted
// capabilities and the settings of a person who never saved one).
type StepRequest struct {
	Entry string
	Steps approvalpolicy.StepsPolicy
}

// StepDecision is the data layer's answer. Approved is false when the grant
// was denied because the asset used all its steps; Summary then holds the
// numbers the refusal was decided on.
type StepDecision struct {
	Approved bool
	Summary  StepSummary
}

// StepGrantStore is the data layer's atomic 3D step decision: the usage read
// and the grant decision commit in one transaction, so two concurrent steps of
// one asset can never both take the last step of its cap.
type StepGrantStore interface {
	// ApproveExecutionGrantWithSteps decides one pending grant of the owner.
	// A capability the policy does not count is approved without a read of the
	// settings. For a counted one the owner's setting of the entry (the Schema
	// default when none is stored) decides: agent and unlimited approve; cap
	// approves while the asset's used steps are below the cap and otherwise
	// denies the grant (decided by StepsActor). Both outcomes write the
	// immutable grant audit. A repeated call for a grant it already decided
	// returns that decision unchanged.
	ApproveExecutionGrantWithSteps(ctx context.Context, owner, requestID string, request StepRequest) (Grant, StepDecision, error)
	// StepUsage reads the asset's standing under the owner's setting of the
	// entry, without deciding anything.
	StepUsage(ctx context.Context, owner, sessionID string, request StepRequest) (StepSummary, error)
}

// StepSessionReader is the optional second capability of a SessionModeReader.
// It names the entry (approvalpolicy.StepEntryList or StepEntryMCP) whose
// setting governs a session that reports the steps mode. Nothing derives the
// steps mode for a session yet; the asset-list sub-sessions (#328) and the MCP
// sessions (#332) implement both halves. A reader without it leaves a steps
// grant to a person.
type StepSessionReader interface {
	SessionStepEntry(ctx context.Context, owner, session string) (string, error)
}

// stepEntry resolves the entry of a steps session. The second result is false
// when the mode reader cannot name a valid entry.
func (s Service) stepEntry(ctx context.Context, owner, session string) (string, bool, error) {
	reader, ok := s.Modes.(StepSessionReader)
	if !ok {
		return "", false, nil
	}
	entry, err := reader.SessionStepEntry(ctx, owner, session)
	if err != nil {
		return "", false, err
	}
	return entry, approvalpolicy.ValidStepEntry(entry), nil
}

// applyStepPolicy decides one grant of a steps session (#330). The policy never
// sends a step to a person: 2D steps, LLM calls and every capability the policy
// does not count are approved at once, and a counted 3D step is approved or
// refused by the owner's setting. Only a deployment that cannot say which entry
// the session belongs to, or whose data layer cannot decide atomically, leaves
// the grant to a person (the stricter result).
func (s Service) applyStepPolicy(ctx context.Context, grant Grant, snapshot PreparedSnapshot) (PolicyResult, error) {
	steps := s.Policy.Steps
	if steps == nil {
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman}, ErrHumanRequired
	}
	if !steps.Counts(snapshot.Capability) {
		approved, err := s.Grants.ApproveExecutionGrantByPolicy(ctx, grant.OwnerID, grant.RequestID, StepsActor)
		if err != nil {
			return PolicyResult{Grant: grant, Outcome: PolicyOutcomeNone}, err
		}
		return PolicyResult{Grant: approved, Outcome: PolicyOutcomeApproved}, nil
	}
	store, ok := s.Grants.(StepGrantStore)
	if !ok {
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman}, ErrHumanRequired
	}
	entry, ok, err := s.stepEntry(ctx, grant.OwnerID, grant.SessionID)
	if err != nil {
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeNone}, err
	}
	if !ok {
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeHuman}, ErrHumanRequired
	}
	decided, decision, err := store.ApproveExecutionGrantWithSteps(ctx, grant.OwnerID, grant.RequestID,
		StepRequest{Entry: entry, Steps: *steps})
	if err != nil {
		return PolicyResult{Grant: grant, Outcome: PolicyOutcomeNone}, err
	}
	summary := decision.Summary
	if !decision.Approved {
		return PolicyResult{Grant: decided, Outcome: PolicyOutcomeStepCap, Steps: &summary}, ErrStepCapReached
	}
	return PolicyResult{Grant: decided, Outcome: PolicyOutcomeApproved, Steps: &summary}, nil
}

// StepStatus is the session's 3D step standing under its owner's setting of
// the session's entry. It reads; it decides nothing. The Agent's tool uses it to
// word a refusal again when the same call is replayed.
func (s Service) StepStatus(ctx context.Context, ownerID, sessionID string) (StepSummary, error) {
	steps := s.Policy.Steps
	store, ok := s.Grants.(StepGrantStore)
	if steps == nil || !ok || ownerID == "" || sessionID == "" {
		return StepSummary{}, ErrInvalid
	}
	entry, ok, err := s.stepEntry(ctx, ownerID, sessionID)
	if err != nil {
		return StepSummary{}, err
	}
	if !ok {
		return StepSummary{}, ErrInvalid
	}
	return store.StepUsage(ctx, ownerID, sessionID, StepRequest{Entry: entry, Steps: *steps})
}
