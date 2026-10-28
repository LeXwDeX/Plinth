package generationtool

import (
	"context"
	"errors"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

var ErrOperationNotFound = errors.New("generation tool: operation not found")
var ErrOperationConflict = errors.New("generation tool: operation identity conflict")

// Operation freezes one planned agent tool call before asking a human to
// decide. The original idempotency key and prepared input survive restarts.
type Operation struct {
	Harness            harness.Binding `json:"harness,omitempty"`
	OwnerID            string
	SessionID          string
	AssistantMessageID string
	CallID             string
	ToolName           string
	CommandHash        string
	OperationID        string
	IdempotencyKey     string
	PreparedInputID    generation.PreparedInputID
	PreparedInputHash  string
	OfferingID         generation.OfferingID
	OfferingRevision   string
	GrantRequestID     string
	// RechargeTaskID names the newest earlier Task of the session that failed
	// after the upstream finished and charged, whose result was not stored and
	// that nothing answered since (#294). It is decided once, when the operation
	// is created; a set value makes the step wait for a person on its approval
	// card whatever the session's approval mode, and the card says the earlier
	// attempt was already charged so this one charges again.
	RechargeTaskID string
	// ReferenceCount of ReferenceTotal are the reference pictures the step
	// uses of the ones the turn gave, when the Offering takes fewer (#329 E:
	// Meshy 7.1 Image to 3D takes one). Decided once, when the operation is
	// created, from the same cut that froze the input; both are 0 when no
	// picture was left out (and on operations created before this was kept).
	// The approval card and the message that reports the finished step say
	// it with the Offering's reference-limit note (runtime.generation_messages).
	ReferenceCount int
	ReferenceTotal int
	// ReferenceSource freezes what the reference pictures really were when
	// this operation was prepared: the source identities before and after a
	// three-view replacement, de-duplication and the Offering's cut, and the
	// exact front view the prepared input uses. It is empty for operations
	// written before this fact was kept and for steps without reference
	// pictures. Its Identity is never guessed from a URL, a digest or today's
	// three-view groups.
	ReferenceSource ReferenceSourceFact
	// Seq orders operations by creation (the store's insertion order).
	Seq int64
	// Detached means the turn stopped waiting for the human decision and ended
	// with the grant still pending. The turn no longer touches the grant; the
	// pending-approval service owns its later decision and result.
	Detached bool
	// PolicyEvaluated means the session approval policy already looked at the
	// grant once. It is never applied again, so a later mode switch cannot
	// approve a grant that was left waiting.
	PolicyEvaluated bool
	// Settled means the outcome of a detached decision (the denial, or the
	// finished task) was posted into the session.
	Settled bool
}

type OperationStore interface {
	LoadAgentOperation(context.Context, string, string, string, string) (Operation, error)
	InsertAgentOperation(context.Context, Operation) (Operation, error)
	AttachAgentGrantRequest(context.Context, Operation, string) (Operation, error)
	// DetachAgentOperation marks the operation detached while its grant is
	// still pending and reports whether it is detached afterwards. It is false
	// when the grant was decided first; the caller then continues in the turn.
	DetachAgentOperation(context.Context, Operation) (Operation, bool, error)
	// MarkAgentPolicyEvaluated records that session policy looked at the grant.
	MarkAgentPolicyEvaluated(context.Context, Operation) error
	// ListPendingDetached lists the session's approvals that are still waiting
	// for a person; the executor looks through them for a duplicate request.
	ListPendingDetached(ctx context.Context, owner, session string, limit int, pendingAfter time.Time) ([]Operation, error)
}

// DetachedOperationStore is the part of the store the pending-approval service
// uses once a turn is gone. Every method is owner scoped.
type DetachedOperationStore interface {
	LoadAgentOperationByGrant(ctx context.Context, owner, requestID string) (Operation, error)
	// ListPendingDetached returns the session's detached operations whose
	// grant is still pending and whose trigger turn still exists, newest first.
	// A trigger turn that was stopped does not hide a detached approval. When
	// pendingAfter is not zero, a grant that started waiting at or before it
	// (an expired one) is left out, so expired approvals neither show nor
	// count against limit.
	ListPendingDetached(ctx context.Context, owner, session string, limit int, pendingAfter time.Time) ([]Operation, error)
	// ListUnsettledDetached returns detached operations after the cursor whose
	// grant was decided but whose outcome was not posted yet, oldest first.
	ListUnsettledDetached(ctx context.Context, afterSeq int64, limit int) ([]Operation, error)
	// ListWaitingDetached returns detached operations after the cursor whose
	// grant is still pending, oldest first: the sweep marks the expired ones.
	ListWaitingDetached(ctx context.Context, afterSeq int64, limit int) ([]Operation, error)
	// ListStoppedUndetached returns operations after the cursor whose grant is
	// pending or approved but unclaimed, that were never detached, and whose
	// trigger turn was stopped.
	ListStoppedUndetached(ctx context.Context, afterSeq int64, limit int) ([]Operation, error)
	// CancelStoppedAgentOperation denies such a grant as the system and detaches
	// the operation so its cancellation is posted. False when it no longer applies.
	CancelStoppedAgentOperation(ctx context.Context, op Operation) (bool, error)
	SettleAgentOperation(ctx context.Context, op Operation) error
}
