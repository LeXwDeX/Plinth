package chat

import (
	"context"
	"errors"
	"time"
)

// Session thinking level, context usage and context compaction (#322).
//
// A round is one user message together with the assistant replies (and their
// tool calls) that follow it up to the next user message. Compaction folds
// every round before the newest kept ones into one session summary; Agent
// requests then send the system prompt, the summary and the messages after
// the summary. The chat history people see is never changed, and every
// structured state (tasks, pending approvals, choice cards, session notes,
// attachment handles) keeps being read from its own store, never from the
// summary.

// Compaction triggers.
const (
	CompactionManual = "manual"
	CompactionAuto   = "auto"
)

var (
	// ErrReasoningUnavailable refuses a thinking level while the session's
	// model offers none; ErrInvalidReasoningEffort one the model does not offer.
	ErrReasoningUnavailable   = errors.New("chat: the session's model offers no thinking level")
	ErrInvalidReasoningEffort = errors.New("chat: invalid thinking level")
	// ErrCompactionUnavailable: the deployment has compaction off.
	// ErrCompactionTurnActive: a turn of the session has not finished.
	// ErrNothingToCompact: no finished round lies before the kept ones.
	// ErrCompactionConflict: another compaction of the session won.
	// ErrCompactionFailed wraps a failed summary call; the session is unchanged.
	ErrCompactionUnavailable = errors.New("chat: context compaction is off")
	ErrCompactionTurnActive  = errors.New("chat: a turn of the session is still running")
	ErrNothingToCompact      = errors.New("chat: nothing to compact")
	ErrCompactionConflict    = errors.New("chat: the session was compacted concurrently")
	ErrCompactionFailed      = errors.New("chat: context compaction failed")
)

// ContextUsage is the token usage the upstream reported for the newest model
// request of the session. The context in use is PromptTokens plus
// CompletionTokens: what that request sent and what its reply added. It is
// never estimated.
type ContextUsage struct {
	PromptTokens     int
	CompletionTokens int
	Model            string
	UpdatedAt        time.Time
}

// Tokens is the context the request left in use.
func (usage ContextUsage) Tokens() int {
	return usage.PromptTokens + usage.CompletionTokens
}

// Compaction is one compaction of a session: the summary that replaced the
// messages up to and including ThroughMessageID in Agent requests. Rounds is
// how many rounds the summary covers in all (earlier summaries included).
type Compaction struct {
	ID               string
	OwnerID          string
	SessionID        string
	Trigger          string
	Summary          string
	ThroughMessageID string
	ThroughCreatedAt time.Time
	Rounds           int
	CreatedAt        time.Time
}

// SessionContext is a session's stored thinking level, newest usage (nil
// while unknown: never reported, or reset by a compaction) and newest
// compaction (nil before the first).
type SessionContext struct {
	ReasoningEffort string
	// ChatModel is the model ID the person chose for the session; "" follows
	// the administrator's default (#322 Scope 5).
	ChatModel string
	Usage     *ContextUsage
	Latest    *Compaction
}

// ContextStore keeps the thinking level, usage and compactions of sessions.
// Every read and write is scoped to the owner; a missing or foreign session
// reads as not found and is never written.
type ContextStore interface {
	SetSessionReasoningEffort(ctx context.Context, owner, session, effort string) (bool, error)
	// SetSessionChatModel stores the session's chat model choice ("" follows
	// the default); it does not touch the session's updated_at.
	SetSessionChatModel(ctx context.Context, owner, session, model string) (bool, error)
	SessionContext(ctx context.Context, owner, session string) (SessionContext, bool, error)
	// RecordContextUsage replaces the session's usage; nil records that the
	// upstream reported none. It does not touch the session's updated_at.
	RecordContextUsage(ctx context.Context, owner, session string, usage *ContextUsage) error
	// SaveCompaction stores the compaction as the session's newest one if the
	// newest one is still previousID ("" for none), and clears the usage until
	// the next request reports it; otherwise ErrCompactionConflict.
	SaveCompaction(ctx context.Context, compaction Compaction, previousID string) (Compaction, error)
	// ListCompactions returns the session's newest compactions, at most
	// limit, oldest first.
	ListCompactions(ctx context.Context, owner, session string, limit int) ([]Compaction, error)
}
