package executiongrant

import (
	"context"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
)

// The grants of a personal access key (#332).
//
// A key's call, for instance an external MCP client's, is not a person at a
// browser and not an Agent turn, so it has its own grant source (AccessKey) and
// its own decider. The decider never waits for a person and no approval card
// exists for the source: every grant is decided in place, at once, either
// approved or denied.
//
//   - A capability the step policy does not count (2D, LLM, anything else) is
//     approved without reading any setting.
//   - A counted 3D capability follows the owner's MCP setting of the 3D step
//     mode (#330): agent and unlimited approve; cap approves while the asset
//     (one chat session) has steps left and otherwise denies the grant.
//   - A step only a person could approve is denied with its own reason: the
//     external caller is told to ask the person to act in the UI.
//
// The decision is the same transaction as the steps mode's
// (StepGrantStore.ApproveExecutionGrantWithSteps), told the actor, the entry
// and the person-needed flag, so the counting is written once.

// AccessKeyActor is Grant.DecidedBy of a grant its policy approved, or refused
// at the asset's 3D step cap. A person never has this actor ID.
const AccessKeyActor = PolicyActorPrefix + "access_key"

// AccessKeyNeedsPersonActor is Grant.DecidedBy of a grant its policy denied
// because only a person could approve the step. It is the one way that denial
// is told apart from the cap and from a person's decision.
const AccessKeyNeedsPersonActor = AccessKeyActor + ":needs_person"

// AccessKeyNeedsPerson reports whether the policy denied the grant because the
// step is one only a person may approve, and an access key has none to ask.
func (g Grant) AccessKeyNeedsPerson() bool {
	return g.Status == Denied && g.DecidedBy == AccessKeyNeedsPersonActor
}

// PolicyOutcomeAccessKeyNeedsPerson is the outcome of an access-key grant the
// policy denied because only a person could approve it. The grant is denied in
// place; no approval card exists for it and no Task can be created from it.
const PolicyOutcomeAccessKeyNeedsPerson = "needs_person"

// ErrAccessKeyNeedsPerson is what ApproveForAccessKey returns, with the denied
// grant in the result, when the step needs a person.
var ErrAccessKeyNeedsPerson = errors.New("execution grant: step needs a person")

// AccessKeyGrantStore is the data layer's preparation of an access-key grant.
// Its decision is StepGrantStore's, told AccessKeyActor.
type AccessKeyGrantStore interface {
	// PreparePendingForAccessKey issues a pending grant of source AccessKey
	// that records the key. A grant is a replay of the same operation of the
	// same owner and session; one that is still pending or was denied is
	// issued again under the current key, an approved or consumed one is
	// returned unchanged.
	PreparePendingForAccessKey(ctx context.Context, identity Identity, accessKeyID string) (Grant, error)
}

// AccessKeyStep is what the caller knows about the step beyond the frozen
// input. The server derives it; it is never read from a request.
type AccessKeyStep struct {
	// NeedsPerson marks a step only a person could approve: it repeats an
	// earlier attempt of the session that was charged upstream but whose
	// result was not stored (#294), or its submission reliability is unknown.
	// The grant is denied, never left pending.
	NeedsPerson bool
}

// ApproveForAccessKey decides one pending grant of an access key (#332). The
// grant must be of source AccessKey and the owner's; the capability comes from
// the frozen input, never from the caller. An approval returns the approved
// grant. A refusal comes back as an error with the denied grant in the result,
// so a caller that only checks the error cannot take it for an approval:
// ErrStepCapReached (Outcome step_cap, Steps holds the numbers) when the asset
// used all its 3D steps, ErrAccessKeyNeedsPerson (Outcome needs_person) when
// only a person could approve the step. Nothing is ever left pending, and the
// denied grant can never create a Task. Deciding a grant again repeats its
// decision and writes nothing.
func (s Service) ApproveForAccessKey(ctx context.Context, ownerID, requestID string, step AccessKeyStep) (PolicyResult, error) {
	none := func(err error) (PolicyResult, error) { return PolicyResult{Outcome: PolicyOutcomeNone}, err }
	store, isStore := s.Grants.(StepGrantStore)
	if s.Grants == nil || s.Prepared == nil || ownerID == "" || requestID == "" || s.Policy.Steps == nil || !isStore {
		return none(ErrInvalid)
	}
	grant, err := s.Grants.GetExecutionGrant(ctx, ownerID, requestID)
	if err != nil {
		return none(err)
	}
	if grant.OwnerID != ownerID || grant.Source != AccessKey || grant.AccessKeyID == "" {
		return none(ErrConflict)
	}
	if grant.Status == Pending {
		// The frozen input must still be the one the grant was issued for.
		snapshot, err := s.Prepared.GetExecutionPreparedSnapshot(ctx, ownerID, grant.PreparedInputID)
		if err != nil {
			return none(err)
		}
		if snapshot.ID != grant.PreparedInputID || snapshot.OwnerID != ownerID ||
			snapshot.SessionID != grant.SessionID || snapshot.OfferingID != grant.OfferingID ||
			snapshot.OfferingRevision != grant.OfferingRevision || snapshot.InputHash != grant.PreparedInputHash {
			return none(ErrConflict)
		}
	}
	decided, decision, err := store.ApproveExecutionGrantWithSteps(ctx, ownerID, requestID, StepRequest{
		Entry: approvalpolicy.StepEntryMCP, Steps: *s.Policy.Steps,
		Actor: AccessKeyActor, NeedsPerson: step.NeedsPerson,
	})
	if err != nil {
		return none(err)
	}
	summary := decision.Summary
	var steps *StepSummary
	if summary != (StepSummary{}) {
		steps = &summary
	}
	switch {
	case decision.Approved:
		return PolicyResult{Grant: decided, Outcome: PolicyOutcomeApproved, Steps: steps}, nil
	case decision.NeedsPerson:
		return PolicyResult{Grant: decided, Outcome: PolicyOutcomeAccessKeyNeedsPerson}, ErrAccessKeyNeedsPerson
	default:
		return PolicyResult{Grant: decided, Outcome: PolicyOutcomeStepCap, Steps: steps}, ErrStepCapReached
	}
}

// AccessKeyStepStatus is the 3D step standing of an asset (a chat session) of
// the owner under the owner's MCP setting. It reads and decides nothing; an
// external caller's tool result words a refusal with it, and shows the steps
// left.
func (s Service) AccessKeyStepStatus(ctx context.Context, ownerID, sessionID string) (StepSummary, error) {
	store, ok := s.Grants.(StepGrantStore)
	if s.Policy.Steps == nil || !ok || ownerID == "" || sessionID == "" {
		return StepSummary{}, ErrInvalid
	}
	return store.StepUsage(ctx, ownerID, sessionID, StepRequest{Entry: approvalpolicy.StepEntryMCP, Steps: *s.Policy.Steps})
}
