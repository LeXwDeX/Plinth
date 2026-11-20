package openairesponses

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// The request as the Responses API reads it. Shapes the probe of 2026-11-20
// sent and the real gateway accepted (observed): instructions, a flat function
// tool, reasoning.effort, store false, include reasoning.encrypted_content,
// stream, a user message with string content, a system message in input,
// max_output_tokens, a function_call item with its function_call_output (a
// string) in input. Per the documents, not yet seen accepted: input_text and
// input_image parts, strict on the tool, parallel_tool_calls, reasoning items
// in input.
type wireRequest struct {
	Model        string `json:"model,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	// Input is the whole conversation: nothing refers to an earlier response.
	Input []any      `json:"input"`
	Tools []wireTool `json:"tools,omitempty"`
	// Stream and Store are always sent: the Adapter never lets the upstream
	// keep a response (store false), so the request is the only state.
	Stream bool `json:"stream"`
	Store  bool `json:"store"`
	// Include asks for the encrypted reasoning items of a tool loop, the only
	// way to carry the model's reasoning from one round to the next when
	// nothing is stored. Sent only with tools: a request without them has no
	// next round.
	Include   []string       `json:"include,omitempty"`
	Reasoning *wireReasoning `json:"reasoning,omitempty"`
	// Temperature is a pointer so that 0 is still sent; nil (a model whose
	// upstream refuses the member) leaves it out.
	Temperature *float64 `json:"temperature,omitempty"`
	// ParallelToolCalls is sent only when the request carries tools.
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`
}

type wireReasoning struct {
	Effort string `json:"effort"`
}

type wireMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type wireInputText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type wireInputImage struct {
	Type     string `json:"type"`
	ImageURL string `json:"image_url"`
	Detail   string `json:"detail,omitempty"`
}

type wireFunctionCall struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireFunctionCallOutput struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

// wireTool is a function tool in the flat Responses form (no nested
// "function" member, unlike Chat Completions).
type wireTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	// Strict is always sent, as false. Per the documents the Responses API
	// enforces strict parameter validation by default, which needs every
	// property required and additionalProperties false at every level; the
	// Agent's tool schemas are written for Chat Completions, where the
	// default is not strict, and have optional properties. The probe's schema
	// happened to be strict-compatible, so the default was not exercised.
	Strict bool `json:"strict"`
}

// The role names are the wire's own, as the Chat Completions Adapter passes
// them: the Schema's roles carry these values.
const (
	roleSystem    = "system"
	roleUser      = "user"
	roleAssistant = "assistant"
	roleTool      = "tool"
)

func marshalRequest(request agents.Request, stream, parallelToolCalls bool, options *Options) ([]byte, error) {
	instructions, input, err := convertMessages(request.Messages)
	if err != nil {
		return nil, err
	}
	if input == nil {
		input = []any{} // never "input": null
	}
	wire := wireRequest{
		Model:        request.Model,
		Instructions: instructions,
		Input:        input,
		Stream:       stream,
		Store:        false,
	}
	if !options.omitTemperature() {
		temperature := request.Temperature
		wire.Temperature = &temperature
	}
	if level := options.effort(request.ReasoningEffort); level != "" {
		wire.Reasoning = &wireReasoning{Effort: level}
	}
	for _, tool := range request.Tools {
		parameters := tool.Parameters
		if len(parameters) == 0 {
			// A function with no declared parameters takes none.
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		wire.Tools = append(wire.Tools, wireTool{
			Type: "function", Name: tool.Name, Description: tool.Description, Parameters: parameters,
		})
	}
	if len(wire.Tools) > 0 {
		wire.Include = []string{"reasoning.encrypted_content"}
		if parallelToolCalls {
			enabled := true
			wire.ParallelToolCalls = &enabled
		}
	}
	return json.Marshal(wire)
}

// convertMessages maps the Port's messages to instructions and input items:
//   - the system messages before the first other message are the instructions
//     (joined by a blank line); a later system message stays a system item;
//   - a user message is a user item whose content is the text, or a list of
//     input_text and input_image parts when it has parts;
//   - an assistant message is its reasoning items, its text and one
//     function_call item per tool call, in that order (see assistantItems);
//   - a tool message is the function_call_output of its call, with the text as
//     a string. The orchestrator moves the pictures of a tool reply into a user
//     message of their own, so a tool message never carries any; the text of
//     its parts is used when it has no Content.
//
// A call_id is the Port's ToolCall.ID as the caller sent it (the orchestrator
// makes them unique per round), so a function_call and its output meet on the
// same string whatever the upstream called it.
func convertMessages(messages []agents.Message) (instructions string, input []any, err error) {
	var system []string
	leading := true
	for _, message := range messages {
		switch message.Role {
		case roleSystem:
			text := plainText(message)
			if text == "" {
				continue
			}
			if leading {
				system = append(system, text)
				continue
			}
			input = append(input, wireMessage{Role: roleSystem, Content: text})
		case roleUser:
			leading = false
			if content, ok := userContent(message); ok {
				input = append(input, wireMessage{Role: roleUser, Content: content})
			}
		case roleAssistant:
			leading = false
			input = append(input, assistantItems(message)...)
		case roleTool:
			leading = false
			input = append(input, wireFunctionCallOutput{
				Type: "function_call_output", CallID: message.ToolCallID, Output: plainText(message),
			})
		default:
			return "", nil, fmt.Errorf("openai-responses agent: unsupported message role %q", message.Role)
		}
	}
	return strings.Join(system, "\n\n"), input, nil
}

// plainText is a message's text: its Content, else the text of its parts.
func plainText(message agents.Message) string {
	if message.Content != "" || len(message.Parts) == 0 {
		return message.Content
	}
	var texts []string
	for _, part := range message.Parts {
		if part.URL == "" && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// userContent is a user message's content: the text, or its parts. ok is false
// for a message with nothing to send. As on Chat Completions, parts win over
// Content (the orchestrator puts the text beside the pictures into parts).
func userContent(message agents.Message) (content any, ok bool) {
	if len(message.Parts) == 0 {
		return message.Content, message.Content != ""
	}
	parts := make([]any, 0, len(message.Parts))
	for _, part := range message.Parts {
		switch {
		case part.URL != "":
			parts = append(parts, wireInputImage{Type: "input_image", ImageURL: part.URL, Detail: part.Detail})
		case part.Text != "":
			parts = append(parts, wireInputText{Type: "input_text", Text: part.Text})
		}
	}
	return parts, len(parts) > 0
}

// assistantItems are the input items of an assistant message, in the order the
// model produced them: the reasoning items it returned (when the Adapter
// carried them), its text as an assistant message, then one function_call item
// per tool call, with the reasoning items that came between two calls kept
// between them. A reasoning item that no item follows is dropped: the API
// refuses one that leads nowhere (per the documents).
//
// The function_call items are built from the Port's ToolCall (call_id, name,
// arguments); the id and status the upstream gave are not sent back, so no item
// refers to a response the upstream never stored.
func assistantItems(message agents.Message) []any {
	carried := decodeReasoning(message.ReasoningContent)
	var items []any
	next := 0 // the next carried reasoning item to place
	placeReasoning := func(before int) {
		for ; next < len(carried) && carried[next].Before <= before; next++ {
			items = append(items, carried[next].Item)
		}
	}
	placeReasoning(0)
	if text := plainText(message); text != "" {
		items = append(items, wireMessage{Role: roleAssistant, Content: text})
	}
	for index, call := range message.ToolCalls {
		if index > 0 {
			placeReasoning(index)
		}
		arguments := strings.TrimSpace(string(call.Arguments))
		if arguments == "" {
			arguments = "{}"
		}
		items = append(items, wireFunctionCall{Type: "function_call", CallID: call.ID, Name: call.Name, Arguments: arguments})
	}
	// A carried item that came after the last call is never placed, and one
	// placed ahead of nothing (a message without text or calls) is dropped:
	// nothing follows them.
	for len(items) > 0 {
		if _, reasoning := items[len(items)-1].(json.RawMessage); !reasoning {
			break
		}
		items = items[:len(items)-1]
	}
	return items
}
