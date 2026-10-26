package agents

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrImagesRejected marks an upstream refusal of a request that carried
// pictures: most likely a model without image input (#253).
var ErrImagesRejected = errors.New("agent model rejected a request with images")

type ContentPart struct {
	Kind   string
	Text   string
	URL    string
	Detail string
}

type Message struct {
	Role       string
	Content    string
	Parts      []ContentPart
	ToolCalls  []ToolCall
	ToolCallID string
	// ReasoningContent is the model's private reasoning for an assistant
	// message. It is never shown to users; adapters whose vendor contract
	// requires it send it back within the same tool loop.
	ReasoningContent string `json:",omitempty"`
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

type Request struct {
	Model string
	// Connection names the chat model connection that serves Model (#322
	// Scope 5); empty is the deployment's own connection. Adapters bound to
	// one connection ignore it; the connection router dispatches on it.
	Connection  string `json:",omitempty"`
	Temperature float64
	Messages    []Message
	Tools       []Tool
	// ScopeWithheld names the tools the session's Agent scope hides from this
	// turn (they are missing from Tools). A model adapter sends nothing of it;
	// the mock Agent reads it to answer like an Agent that knows its limits.
	ScopeWithheld []string `json:",omitempty"`
	// ReasoningEffort is the thinking level the session chose for the turn
	// (#322). An adapter writes it to the model profile's effort field when
	// the profile offers that level, else sends the profile default; a model
	// without a profile sends no thinking fields.
	ReasoningEffort string `json:",omitempty"`
	// Purpose marks a request that is not a conversation turn: PurposeSummary
	// is a context compaction's summary call (#322). Model adapters send
	// nothing of it; the mock Agent reads it.
	Purpose string `json:",omitempty"`
}

// PurposeSummary marks the summary call of a context compaction.
const PurposeSummary = "summary"

// Usage is the token usage the upstream reported for one request (#322).
// The adapter never estimates it: a reply without usage has none.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type Delta struct {
	Content string
	// Thinking reports that the model is reasoning before (or between) its
	// visible output. It carries no reasoning text: the caller only shows a
	// status, and the reasoning itself never leaves the adapter (#285).
	Thinking bool `json:",omitempty"`
}

type Completion struct {
	Content   string
	ToolCalls []ToolCall
	// ReasoningContent is retained only when the adapter must pass it back.
	ReasoningContent string `json:",omitempty"`
	// Usage is what the upstream reported for this request; nil when it
	// reported nothing (#322).
	Usage *Usage `json:",omitempty"`
}

type Adapter interface {
	Stream(
		context.Context,
		Request,
		func(Delta) error,
	) (Completion, error)
	Complete(context.Context, Request) (Completion, error)
}
