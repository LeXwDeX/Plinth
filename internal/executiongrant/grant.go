// Package executiongrant defines a single human-authorized Provider operation.
// A grant is tied to a frozen input and cannot authorize a different operation.
package executiongrant

import (
	"context"
	"errors"
	"time"

	"github.com/LeXwDeX/Plinth/internal/harness"
)

var (
	ErrInvalid  = errors.New("execution grant: invalid identity")
	ErrConflict = errors.New("execution grant: identity conflict")
	ErrState    = errors.New("execution grant: invalid state")
	// ErrHumanRequired means the session approval mode and the step capability
	// require a human decision, so policy must not approve the grant.
	ErrHumanRequired = errors.New("execution grant: human decision required")
	// ErrExpired means an Agent approval waited longer than the Schema allows.
	// It can no longer be approved; the work is requested again in a new turn.
	ErrExpired = errors.New("execution grant: approval expired")
)

type Status string

const (
	Pending  Status = "pending"
	Approved Status = "approved"
	Claimed  Status = "claimed"
	Bound    Status = "bound"
	Denied   Status = "denied"
)

type Decision string

const (
	Approve Decision = "approve"
	Deny    Decision = "deny"
)

// Identity is server-derived from the operation and immutable prepared input.
// PlanStepID may be empty for a standalone operation; all other fields are required.
type Identity struct {
	Harness           harness.Binding `json:"harness,omitempty"`
	OwnerID           string
	SessionID         string
	OfferingID        string
	OfferingRevision  string
	PreparedInputID   string
	PreparedInputHash string
	IdempotencyKey    string
	OperationID       string
	PlanStepID        string
}

func (i Identity) Valid() bool {
	return i.Harness.Valid() && i.OwnerID != "" && i.SessionID != "" && i.OfferingID != "" &&
		i.OfferingRevision != "" && i.PreparedInputID != "" &&
		i.PreparedInputHash != "" && i.IdempotencyKey != "" && i.OperationID != ""
}

type Grant struct {
	RequestID string
	Identity
	TriggerTurnID  string
	Status         Status
	TaskID         string
	Source         string
	RequestVersion int64
	DecidedBy      string
	DecidedAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// PendingSince is when the grant started waiting for a decision. A pending
// grant changes only when it is created or re-issued, so UpdatedAt is that
// moment.
func (g Grant) PendingSince() time.Time {
	if g.UpdatedAt.IsZero() {
		return g.CreatedAt
	}
	return g.UpdatedAt
}

// Repository handles human decisions. Claiming and binding are intentionally
// separate transaction helpers on the task repository, so task insertion and
// grant consumption can share one SQLite transaction.
type Repository interface {
	PreparePending(context.Context, Identity, string, string) (Grant, error)
	DecideExecutionGrant(context.Context, string, string, string, Decision) (Grant, error)
	GetExecutionGrant(context.Context, string, string) (Grant, error)
	// ApproveExecutionGrantByPolicy moves one pending grant of the same owner
	// to approved and records decidedBy (always "policy:<mode>") in the grant
	// and in the immutable audit table. It never denies, never touches a
	// non-pending grant, and replays idempotently for the same actor.
	ApproveExecutionGrantByPolicy(ctx context.Context, owner, requestID, decidedBy string) (Grant, error)
}
