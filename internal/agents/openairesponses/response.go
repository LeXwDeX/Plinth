package openairesponses

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/upstreamhttp"
	"github.com/LeXwDeX/Plinth/internal/moderation"
)

// The statuses of a response object the Adapter tells apart.
const (
	statusCompleted  = "completed"
	statusIncomplete = "incomplete"
	statusFailed     = "failed"
)

// responseBody is the response object: the body of a non-stream answer and the
// response member of response.completed, response.incomplete and
// response.failed. The real gateway's objects carry many members the Adapter
// ignores (content_filters, access_programs, input_transformations, ...), and a
// newer API version may add more.
type responseBody struct {
	Status            string            `json:"status"`
	Output            []json.RawMessage `json:"output"`
	Usage             *wireUsage        `json:"usage"`
	Error             *wireError        `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

// wireUsage is the usage the upstream reported. Its details
// (input_tokens_details.cached_tokens, output_tokens_details.reasoning_tokens)
// are parts of the two totals, not additions to them, so they are not read: the
// Port counts the prompt and the completion, and the Adapter never estimates.
type wireUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (usage *wireUsage) agentUsage() *agents.Usage {
	if usage == nil {
		return nil
	}
	return &agents.Usage{PromptTokens: usage.InputTokens, CompletionTokens: usage.OutputTokens}
}

// wireError is an error object, in a failed response, in an error event or
// nested in one.
type wireError struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   any    `json:"param"`
}

func (err *wireError) code() string {
	if code, ok := err.Code.(string); ok {
		return code
	}
	return ""
}

// failure is what a response object says about its own failure: its error
// object (a failed response has one; so does a bare error body), or an
// incomplete response cut by the content filter (the
// documents' incomplete_details.reason content_filter). nil for any other
// response, including one cut by max_output_tokens, whose partial answer is
// returned as the Chat Completions Adapter returns a finish_reason length one.
func (body responseBody) failure() *wireError {
	switch {
	case body.Error != nil:
		return body.Error
	case body.Status == statusFailed:
		return &wireError{Message: "the response failed"}
	case body.Status == statusIncomplete && body.IncompleteDetails != nil && body.IncompleteDetails.Reason == "content_filter":
		return &wireError{Code: "content_filter", Message: "the response was cut by the content filter"}
	case body.Status != "" && body.Status != statusCompleted && body.Status != statusIncomplete:
		return &wireError{Message: fmt.Sprintf("the response ended with status %q", body.Status)}
	}
	return nil
}

// failure reads a failed response, in-stream or not, as the Port's errors: a
// vendor content-safety code is agents.ContentRejection (#35) and a declared
// used-up quota agents.QuotaExhaustion (#120). It is the same reading as of an
// HTTP refusal, applied to the error object (status is the HTTP status of the
// response that carried it, 200 in a stream). The upstream's wording stays in
// the error, which only the log reads. Per the documents: the probes saw
// neither a failed response nor an error event.
func (adapter *Adapter) failure(status int, wire *wireError) error {
	if wire == nil {
		return nil
	}
	message := strings.TrimSpace(wire.Message)
	if runes := []rune(message); len(runes) > 500 {
		message = string(runes[:500])
	}
	err := fmt.Errorf("agent response failed: %s: %s", wire.code(), message)
	body, _ := json.Marshal(map[string]any{"error": map[string]any{
		"code": wire.Code, "message": wire.Message, "type": wire.Type}})
	if code := upstreamhttp.ModerationCode(body); code != "" {
		reasons := moderation.Reasons(wire.Message)
		if len(reasons) == 0 {
			reasons = moderation.Tokens(code)
		}
		return errors.Join(&agents.ContentRejection{Reasons: reasons}, err)
	}
	if adapter.exhaustedQuota != nil {
		if class, exhausted := adapter.exhaustedQuota(status, body); exhausted {
			return errors.Join(&agents.QuotaExhaustion{Class: class}, err)
		}
	}
	return err
}

// itemBody is an output item. Members of other item types are ignored: the
// real gateway's reasoning item also carries id and content (an empty array),
// its message item phase, role and annotations, and a future version may add
// more.
type itemBody struct {
	Type             string          `json:"type"`
	Status           string          `json:"status"`
	CallID           string          `json:"call_id"`
	Name             string          `json:"name"`
	Arguments        string          `json:"arguments"`
	Content          json.RawMessage `json:"content"`
	Summary          json.RawMessage `json:"summary"`
	EncryptedContent string          `json:"encrypted_content"`
}

// replayReasoning is a reasoning item as it is sent back: the three members the
// probe of 2026-11-20 replayed and the gateway accepted (type, summary,
// encrypted_content), without the item's id and its content. The id names an
// item of a stored response, and nothing is stored (store false); the replay
// without it, ahead of the function_call it led to, was accepted, so that is
// the form kept. Nil for an item with no encrypted_content: nothing a later
// request could send would stand for it.
func replayReasoning(item itemBody) json.RawMessage {
	if item.EncryptedContent == "" {
		return nil
	}
	summary := bytes.TrimSpace(item.Summary)
	if len(summary) == 0 || bytes.Equal(summary, []byte("null")) {
		summary = []byte("[]")
	}
	encoded, err := json.Marshal(struct {
		Type             string          `json:"type"`
		Summary          json.RawMessage `json:"summary"`
		EncryptedContent string          `json:"encrypted_content"`
	}{Type: "reasoning", Summary: summary, EncryptedContent: item.EncryptedContent})
	if err != nil {
		return nil
	}
	return encoded
}

// visibleText is the text of a message item: its output_text parts and a
// refusal part, in order. A refusal is the model's answer to the person, so it
// is shown.
func (item itemBody) visibleText() string {
	var parts []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	}
	if json.Unmarshal(item.Content, &parts) != nil {
		return ""
	}
	var text strings.Builder
	for _, part := range parts {
		switch part.Type {
		case "output_text":
			text.WriteString(part.Text)
		case "refusal":
			text.WriteString(part.Refusal)
		}
	}
	return text.String()
}

// outputItem is what the Adapter knows of one output item (by output_index):
// what the events streamed of it, and, once the item is done, the item itself.
// A done item wins over what was streamed.
type outputItem struct {
	kind    string
	settled bool
	// unfinished: the item's own status says the response cut it short, so a
	// function call's arguments are not whole.
	unfinished bool
	callID     string
	name       string
	arguments  *string // the final arguments, from the done events
	streamed   strings.Builder
	text       *string // the final text of a message, from its done item
	reasoning  json.RawMessage
}

// outputs are the output items of one response, by output_index.
type outputs struct {
	items map[int]*outputItem
}

func newOutputs() *outputs { return &outputs{items: map[int]*outputItem{}} }

// slot is the item at an output_index, created on first sight: an event can
// name an item whose output_item.added was not seen.
func (o *outputs) slot(index int) *outputItem {
	item, found := o.items[index]
	if !found {
		item = &outputItem{}
		o.items[index] = item
	}
	return item
}

// start records an item that was just added: its kind, and for a function call
// the ID and name it will be answered by, so the call is known even if the
// stream ends before its done event.
func (o *outputs) start(index int, raw json.RawMessage) error {
	var body itemBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return fmt.Errorf("decode agent output item: %w", err)
	}
	item := o.slot(index)
	if item.settled {
		return nil
	}
	item.kind = body.Type
	if body.Type == "function_call" {
		item.callID, item.name = body.CallID, body.Name
	}
	return nil
}

// settle records the done item at an index.
func (o *outputs) settle(index int, raw json.RawMessage) error {
	var body itemBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return fmt.Errorf("decode agent output item: %w", err)
	}
	item := o.slot(index)
	item.kind, item.settled = body.Type, true
	switch body.Type {
	case "function_call":
		item.callID, item.name = body.CallID, body.Name
		if body.Status == "incomplete" {
			item.unfinished = true
			return nil
		}
		arguments := body.Arguments
		item.arguments = &arguments
	case "message":
		text := body.visibleText()
		item.text = &text
	case "reasoning":
		// Only an item with encrypted content can be sent back, in the form
		// that was accepted (reasoning.go).
		item.reasoning = replayReasoning(body)
	}
	return nil
}

// isSettled reports whether the item at an index was closed by a done item.
func (o *outputs) isSettled(index int) bool {
	item, found := o.items[index]
	return found && item.settled
}

// completion assembles the Port's Completion from the items, in output order.
// partial marks a response that was cut: a function call whose arguments the
// upstream did not finish is dropped, since running a call on half its
// arguments is worse than not running it.
func (o *outputs) completion(partial bool) agents.Completion {
	var result agents.Completion
	var text strings.Builder
	var carried []carriedReasoning
	for _, index := range slices.Sorted(maps.Keys(o.items)) {
		item := o.items[index]
		switch item.kind {
		case "message":
			if item.text != nil {
				text.WriteString(*item.text)
			} else {
				text.WriteString(item.streamed.String())
			}
		case "function_call":
			arguments := item.streamed.String()
			switch {
			case item.unfinished:
				continue
			case item.arguments != nil:
				arguments = *item.arguments
			case partial:
				continue
			}
			if item.name == "" {
				continue
			}
			result.ToolCalls = append(result.ToolCalls, agents.ToolCall{
				ID: item.callID, Name: item.name, Arguments: json.RawMessage(arguments),
			})
		case "reasoning":
			if item.reasoning != nil {
				carried = append(carried, carriedReasoning{Before: len(result.ToolCalls), Item: item.reasoning})
			}
		}
	}
	result.Content = text.String()
	result.ReasoningContent = encodeReasoning(carried)
	return result
}
