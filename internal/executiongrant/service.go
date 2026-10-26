package executiongrant

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

const HumanBrowser = "human_browser"
const AgentPending = "agent_human_decision"

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

// ApprovedByPolicy reports whether the grant was approved by session policy.
func (g Grant) ApprovedByPolicy() bool {
	return strings.HasPrefix(g.DecidedBy, PolicyActorPrefix)
}

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
	// PendingTTL is how long an Agent approval stays approvable (Schema
	// chat.pending_approvals.ttl_seconds). Zero means it never expires.
	PendingTTL time.Duration
	// Clock is the time source for expiry; nil means time.Now.
	Clock func() time.Time
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
		(cmd.Source != HumanBrowser && cmd.Source != AgentPending) || cmd.SessionID == "" || cmd.PreparedInputID == "" {
		return Grant{}, ErrInvalid
	}
	if (cmd.Source == AgentPending && cmd.TriggerTurnID == "") ||
		(cmd.Source == HumanBrowser && cmd.TriggerTurnID != "") {
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
	if decision == Approve && s.Expired(g) {
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
	if s.Grants == nil || s.Prepared == nil || ownerID == "" || requestID == "" {
		return Grant{}, ErrInvalid
	}
	actor := PolicyActorPrefix + s.Policy.NormalizeMode(mode)
	grant, err := s.Grants.GetExecutionGrant(ctx, ownerID, requestID)
	if err != nil {
		return Grant{}, err
	}
	if grant.OwnerID != ownerID || (grant.Source != HumanBrowser && grant.Source != AgentPending) {
		return Grant{}, ErrConflict
	}
	if grant.Status != Pending {
		if grant.DecidedBy == actor && (grant.Status == Approved || grant.Status == Claimed || grant.Status == Bound) {
			return grant, nil
		}
		return Grant{}, ErrState
	}
	snapshot, err := s.Prepared.GetExecutionPreparedSnapshot(ctx, ownerID, grant.PreparedInputID)
	if err != nil {
		return Grant{}, err
	}
	if snapshot.ID != grant.PreparedInputID || snapshot.OwnerID != ownerID ||
		snapshot.SessionID != grant.SessionID || snapshot.OfferingID != grant.OfferingID ||
		snapshot.OfferingRevision != grant.OfferingRevision || snapshot.InputHash != grant.PreparedInputHash {
		return Grant{}, ErrConflict
	}
	if approvalpolicy.RequiresHuman(s.Policy, mode, snapshot.Capability) {
		return Grant{}, ErrHumanRequired
	}
	// An Agent step that spends extra credits is the person's to approve in
	// manual mode, even on a capability that is otherwise automatic: the
	// extra part is shown on the approval card and agreed to there.
	extra := snapshot.ExtraCredits > 0
	if snapshot.NativeQuote != nil {
		extra, err = snapshot.NativeQuote.HasExtra()
		if err != nil {
			return Grant{}, ErrConflict
		}
	}
	if grant.Source == AgentPending && approvalpolicy.RequiresHumanForExtraSpend(s.Policy, mode, extra) {
		return Grant{}, ErrHumanRequired
	}
	return s.Grants.ApproveExecutionGrantByPolicy(ctx, ownerID, requestID, actor)
}

// ApplyPolicy looks up the session approval mode and approves the grant by
// policy when no human is required. Every grant creation site calls it right
// after Prepare. A grant that is not pending, or that needs a human, is
// returned unchanged without error. A failed mode lookup is an error: the
// caller must not guess a mode.
func (s Service) ApplyPolicy(ctx context.Context, grant Grant) (Grant, error) {
	if grant.Status != Pending || s.Modes == nil {
		return grant, nil
	}
	mode, err := s.Modes.SessionApprovalMode(ctx, grant.OwnerID, grant.SessionID)
	if err != nil {
		return grant, err
	}
	approved, err := s.ApproveByPolicy(ctx, grant.OwnerID, grant.RequestID, mode)
	if err == nil {
		return approved, nil
	}
	if errors.Is(err, ErrHumanRequired) {
		return grant, nil
	}
	return grant, err
}
