package agents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// The kinds of thinking a reasoning item can carry, as the Catalog's
// chat.agent.thinking.model_profiles[].thinking_kind declares them (#377).
// ReasoningNone is also what an item of a message written before the kind
// existed carries.
const (
	ReasoningNone      = "none"
	ReasoningPlaintext = "plaintext"
	ReasoningEncrypted = "encrypted"
)

// ErrImagesRejected marks an upstream refusal of a request that carried
// pictures, where the model or endpoint takes no picture input at all: most
// likely a model without image input (#253). A picture the model read but
// would not accept is ErrImageInvalid below, not this.
var ErrImagesRejected = errors.New("agent model rejected a request with images")

// ErrImageInvalid marks an upstream refusal of a request whose picture the
// model read and rejected as a picture: its dimensions, its size, its format
// or the number of pictures is outside what the model takes (#497). The model
// does read images, so the person is asked for another picture, not for
// another model.
var ErrImageInvalid = errors.New("agent upstream refused the request's picture")

// ImageInvalidity is an ErrImageInvalid that carries what the upstream
// objected to, as tokens of a closed vocabulary (dimensions, size, format,
// count) and never as the upstream's own wording: upstream bytes must not
// steer what a person reads.
type ImageInvalidity struct {
	Reasons []string
}

func (invalidity *ImageInvalidity) Error() string { return ErrImageInvalid.Error() }

func (invalidity *ImageInvalidity) Is(target error) bool { return target == ErrImageInvalid }

// ErrInlineImageOutput marks a reply in which the Agent model drew a picture
// itself (delta.images on Chat Completions, an image_generation_call item on
// Responses, #638) instead of answering in text or calling a generation
// tool. The picture is not kept, so the person is told to retry or switch the
// conversation model rather than reading an unknown error.
var ErrInlineImageOutput = errors.New("agent model answered with a picture of its own instead of text or a tool call")

// ErrContentRejected marks an upstream content-safety refusal: the vendor's
// moderation flagged the pictures or text of the request (#35). The model
// may read images fine; the person should change the input, not the model.
var ErrContentRejected = errors.New("agent upstream content moderation rejected the request")

// ContentRejection is an ErrContentRejected that carries the reasons the
// upstream's review named (vendor tokens such as "sexual" or "input_image").
type ContentRejection struct {
	Reasons []string
}

func (rejection *ContentRejection) Error() string { return ErrContentRejected.Error() }

func (rejection *ContentRejection) Is(target error) bool { return target == ErrContentRejected }

// ErrQuotaExhausted marks an upstream refusal that the chat connection's
// Schema error mapping declares as a used-up account balance or key quota
// (#120: the LLMs gateway's 429 quota_exceeded). Resending does not help
// until an administrator tops up or raises the quota.
var ErrQuotaExhausted = errors.New("agent upstream balance or quota is exhausted")

// QuotaExhaustion is an ErrQuotaExhausted that carries the failure class the
// declaration named (insufficient_balance or credential_quota).
type QuotaExhaustion struct {
	Class string
}

func (exhaustion *QuotaExhaustion) Error() string {
	return ErrQuotaExhausted.Error() + " (" + exhaustion.Class + ")"
}

func (exhaustion *QuotaExhaustion) Is(target error) bool { return target == ErrQuotaExhausted }

// ErrRequestRejected marks an upstream refusal of one request parameter: an
// HTTP 400 invalid_request_error that names the request field (#401, for
// example a model that accepts no temperature other than its default). The
// model may serve other requests; the person should switch model or an
// administrator should read the log, which keeps the upstream's own wording.
var ErrRequestRejected = errors.New("agent upstream rejected a request parameter")

// RequestRejection is an ErrRequestRejected that carries the request field the
// upstream named, already checked to be a plain field path (never free text).
type RequestRejection struct {
	Param string
}

func (rejection *RequestRejection) Error() string {
	return ErrRequestRejected.Error() + " (" + rejection.Param + ")"
}

func (rejection *RequestRejection) Is(target error) bool { return target == ErrRequestRejected }

// RequestRejectedParam is the request field of a parameter refusal in err.
func RequestRejectedParam(err error) (string, bool) {
	var rejection *RequestRejection
	if errors.As(err, &rejection) {
		return rejection.Param, true
	}
	return "", false
}

// ContentRejectedReasons are the reasons of a content-safety refusal in err.
func ContentRejectedReasons(err error) []string {
	var rejection *ContentRejection
	if errors.As(err, &rejection) {
		return rejection.Reasons
	}
	return nil
}

// ImageInvalidReasons are the reason tokens of a picture refusal in err.
func ImageInvalidReasons(err error) []string {
	var invalidity *ImageInvalidity
	if errors.As(err, &invalidity) {
		return invalidity.Reasons
	}
	return nil
}

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
	// FreshPictures marks a tool reply whose Parts carry pictures this turn's
	// generation just produced (#245). The orchestrator uses it to frame the
	// following picture message as fresh results, not a history lookup; it is
	// never sent to an adapter.
	FreshPictures bool `json:"-"`
	// PicturesOnly marks the synthetic user message that carries tool pictures:
	// it is not a new person message. Text-only test adapters skip it when they
	// look for the latest request; real adapters ignore the flag.
	PicturesOnly bool `json:"-"`
	// Reasoning is what the model thought, kept only so that the same model's
	// next round of the same tool loop can be given it back. Every item names
	// the source it came from and is never shown to users; an item is sent back
	// only to that source (#377).
	Reasoning []ReasoningItem `json:",omitempty"`
}

// ReasoningItem is one piece of a reply's thinking with the source it came
// from: the chat connection and model that produced it, the wire it came on and
// how that vendor carries thinking. A model is never given another connection's,
// another model's or another kind's reasoning (#377).
type ReasoningItem struct {
	Connection string `json:"connection,omitempty"`
	Model      string `json:"model,omitempty"`
	Wire       string `json:"wire,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Content    string `json:"content,omitempty"`
}

// Matches reports whether the item may be sent back to source: the same
// connection, model and wire, and a thinking kind both sides declare. An item
// that names no kind (a message of a checkpoint written before #377) matches
// nothing, because no request can tell where it came from.
func (item ReasoningItem) Matches(source Source) bool {
	if item.Kind == "" || item.Kind == ReasoningNone || item.Kind != source.Kind {
		return false
	}
	return item.Connection == source.Connection && item.Model == source.Model &&
		item.Wire == source.Wire
}

// ReasoningText is the reasoning an Adapter reads back: the content of the
// items of one kind, in order, "" when the message carries none of it. Kind is
// the kind the Adapter's own protocol carries (a profile's thinking_kind, or
// the Responses API's encrypted items); an item of another kind is another
// model's and is never sent on (#377).
func (message Message) ReasoningText(kind string) string {
	if kind == "" || kind == ReasoningNone {
		return ""
	}
	var text strings.Builder
	for _, item := range message.Reasoning {
		if item.Kind == kind {
			text.WriteString(item.Content)
		}
	}
	return text.String()
}

// UnmarshalJSON reads a message, including one of a checkpoint written before
// #377 whose assistant message carried a single reasoning_content string: that
// string becomes one item with no source, so the text is still readable while
// no request can be traced back to it and nothing passes it on.
func (message *Message) UnmarshalJSON(data []byte) error {
	type plain Message
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*message = Message(decoded)
	if len(message.Reasoning) > 0 {
		return nil
	}
	var legacy struct {
		ReasoningContent string
	}
	if err := json.Unmarshal(data, &legacy); err == nil && legacy.ReasoningContent != "" {
		message.Reasoning = []ReasoningItem{{Content: legacy.ReasoningContent}}
	}
	return nil
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
	// Thinking is the thinking contract the request runs under, frozen when the
	// turn started (#377): the wire its model is served on, how that model
	// carries its reasoning and whether its vendor requires the reasoning of a
	// tool loop back. An Adapter decides from it rather than from its own
	// configured profile, so a Catalog that changed under a running or
	// recovered turn cannot change how that turn's reasoning is sent. Nil: the
	// Adapter's configured profile decides (every request that is not a turn).
	Thinking *Thinking `json:",omitempty"`
}

// Thinking is a request's frozen thinking contract: the part of a Catalog
// thinking profile that decides how the request's reasoning is carried and
// whether it must be given back (#377).
type Thinking struct {
	Wire     string `json:"wire,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Passback bool   `json:"passback,omitempty"`
}

// Source is where a reasoning item came from and so the only request it may be
// sent back to: the connection and model, the wire and the thinking kind.
type Source struct {
	Connection string
	Model      string
	Wire       string
	Kind       string
}

// ForRequest is the source of the reasoning a request's assistant messages
// carry. A request without a frozen thinking contract has no kind, so nothing
// matches it.
func ForRequest(request Request) Source {
	source := Source{Connection: request.Connection, Model: request.Model}
	if request.Thinking != nil {
		source.Wire, source.Kind = request.Thinking.Wire, request.Thinking.Kind
	}
	return source
}

// PurposeSummary marks the summary call of a context compaction.
const PurposeSummary = "summary"

// PurposeSkillDraft marks the call that drafts a personal Skill (#548).
const PurposeSkillDraft = "skill_draft"

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
	// ToolCalling reports that the model started writing a tool call: the
	// first call fragment of a completion arrived, long before the call's
	// arguments are complete (#375). Like Thinking it carries no content; the
	// caller only shows a status. Complete has no such signal: it returns the
	// whole completion at once.
	ToolCalling bool `json:",omitempty"`
}

type Completion struct {
	Content   string
	ToolCalls []ToolCall
	// ReasoningContent is retained only when the adapter must pass it back.
	ReasoningContent string `json:",omitempty"`
	// Usage is what the upstream reported for this request; nil when it
	// reported nothing (#322).
	Usage *Usage `json:",omitempty"`
	// FinishReason is why the upstream ended the completion when it is one
	// the caller acts on (#665): FinishLength or FinishContentFilter. Empty
	// for an ordinary end.
	FinishReason string `json:",omitempty"`
}

// The finish reasons a Completion reports (#665): the model stopped at its
// output token limit, so its text or its tool call arguments may be cut; or
// the upstream's content filter ended the reply.
const (
	FinishLength        = "length"
	FinishContentFilter = "content_filter"
)

// FinishReasonOf keeps the upstream finish reasons a caller acts on.
func FinishReasonOf(reason string) string {
	switch reason {
	case FinishLength, FinishContentFilter:
		return reason
	}
	return ""
}

// Empty reports a completion that said nothing the reply can hold: no visible
// text and no tool call (#638). Its reasoning and its usage do not count: a
// round that only thought, or whose terminal event was lost with its usage, is
// still empty.
func (completion Completion) Empty() bool {
	return strings.TrimSpace(completion.Content) == "" && len(completion.ToolCalls) == 0
}

// WithoutReasoning is a completion with its reasoning dropped, for what a turn
// keeps after it has ended (#377).
func (completion Completion) WithoutReasoning() Completion {
	completion.ReasoningContent = ""
	return completion
}

type Adapter interface {
	Stream(
		context.Context,
		Request,
		func(Delta) error,
	) (Completion, error)
	Complete(context.Context, Request) (Completion, error)
}
