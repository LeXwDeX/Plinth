package openaiv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// The /v1/responses request as the fake reads it. Only the members the fake
// answers by are decoded; any other member (store, include, parallel_tool_calls,
// max_output_tokens, text, ...) is accepted as the real API does.
type responsesRequest struct {
	Model        string          `json:"model"`
	Instructions json.RawMessage `json:"instructions"`
	Input        json.RawMessage `json:"input"`
	Tools        []responsesTool `json:"tools"`
	Stream       bool            `json:"stream"`
	Include      []string        `json:"include"`
	Reasoning    *struct {
		Effort  json.RawMessage `json:"effort"`
		Summary json.RawMessage `json:"summary"`
	} `json:"reasoning"`
	// Temperature stays raw: a model constraint judges whether it was sent.
	Temperature       json.RawMessage `json:"temperature"`
	MaxOutputTokens   json.RawMessage `json:"max_output_tokens"`
	ParallelToolCalls json.RawMessage `json:"parallel_tool_calls"`
}

// responsesTool is a function tool in the flat Responses form.
type responsesTool struct {
	Name string
	Raw  map[string]any
}

func (tool *responsesTool) UnmarshalJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&tool.Raw); err != nil {
		return err
	}
	tool.Name, _ = tool.Raw["name"].(string)
	return nil
}

// inputItem is one item of input: a message, a function_call, its
// function_call_output, or a reasoning item.
type inputItem struct {
	Type             string          `json:"type"`
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	ID               string          `json:"id"`
	CallID           string          `json:"call_id"`
	Name             string          `json:"name"`
	Arguments        string          `json:"arguments"`
	Output           json.RawMessage `json:"output"`
	EncryptedContent string          `json:"encrypted_content"`
}

// kind is the item's type, with the implicit message type of an
// easy-input item filled in.
func (item inputItem) kind() string {
	if item.Type == "" && item.Role != "" {
		return "message"
	}
	return item.Type
}

// inputItems reads input: a plain string is one user message, a list is the
// items. ok is false for any other value or an empty list.
func (r responsesRequest) inputItems() (items []inputItem, ok bool) {
	var text string
	if json.Unmarshal(r.Input, &text) == nil {
		if strings.TrimSpace(text) == "" {
			return nil, false
		}
		content, _ := json.Marshal(text)
		return []inputItem{{Role: "user", Content: content}}, true
	}
	if json.Unmarshal(r.Input, &items) != nil || len(items) == 0 {
		return nil, false
	}
	return items, true
}

// instructionsText is the instructions as text; "" when absent or null.
func (r responsesRequest) instructionsText() string {
	var text string
	_ = json.Unmarshal(r.Instructions, &text)
	return text
}

// effort is reasoning.effort as the request wrote it; "" when absent.
func (r responsesRequest) effort() string {
	if r.Reasoning == nil {
		return ""
	}
	var effort string
	_ = json.Unmarshal(r.Reasoning.Effort, &effort)
	return effort
}

// summaryRequested reports whether reasoning.summary asks for a summary.
func (r responsesRequest) summaryRequested() bool {
	if r.Reasoning == nil {
		return false
	}
	var summary string
	return json.Unmarshal(r.Reasoning.Summary, &summary) == nil && summary != ""
}

// includes reports whether include lists the value.
func (r responsesRequest) includes(value string) bool {
	for _, listed := range r.Include {
		if listed == value {
			return true
		}
	}
	return false
}

// presentJSON reports whether a raw member was sent with a value (not absent,
// not null).
func presentJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// chatView is the request seen as a Chat Completions request: instructions
// become a system message, the items of one run of function_call become one
// assistant message with tool_calls, and every function_call_output a tool
// message. The fake-only request conventions (tool and script markers, the
// system-contains question, the vision-budget record) are written once, on the
// Chat Completions request, and read this view.
func (r responsesRequest) chatView(items []inputItem) chatRequest {
	var messages []map[string]any
	if instructions := r.instructionsText(); instructions != "" {
		messages = append(messages, map[string]any{"role": "system", "content": instructions})
	}
	assistant := -1 // the assistant message the current run of function_call fills
	for _, item := range items {
		switch item.kind() {
		case "message":
			role := item.Role
			if role == "developer" {
				role = "system"
			}
			if role == "user" || role == "system" {
				assistant = -1
			}
			messages = append(messages, map[string]any{"role": role, "content": chatContent(item.Content)})
		case "function_call":
			if assistant < 0 {
				messages = append(messages, map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{}})
				assistant = len(messages) - 1
			}
			messages[assistant]["tool_calls"] = append(messages[assistant]["tool_calls"].([]any), map[string]any{
				"id": item.CallID, "type": "function",
				"function": map[string]any{"name": item.Name, "arguments": item.Arguments},
			})
		case "function_call_output":
			assistant = -1
			var output string
			if json.Unmarshal(item.Output, &output) != nil {
				output = string(item.Output)
			}
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": item.CallID, "content": output})
		}
	}
	tools := make([]any, 0, len(r.Tools))
	for _, tool := range r.Tools {
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name}})
	}
	raw, _ := json.Marshal(map[string]any{"model": r.Model, "messages": messages, "tools": tools, "stream": r.Stream})
	var view chatRequest
	_ = json.Unmarshal(raw, &view)
	return view
}

// chatContent is an item's content in the Chat Completions form: a string
// stays one, a list of parts becomes text and image_url parts.
func chatContent(content json.RawMessage) any {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL string `json:"image_url"`
		Detail   string `json:"detail"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return nil
	}
	converted := make([]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text":
			converted = append(converted, map[string]any{"type": "text", "text": part.Text})
		case "input_image":
			converted = append(converted, map[string]any{"type": "image_url",
				"image_url": map[string]any{"url": part.ImageURL, "detail": part.Detail}})
		}
	}
	return converted
}

// pairingRefusal checks the items the way the real API does: every
// function_call_output answers an earlier function_call, every function_call is
// answered later, and a reasoning item is followed by the item it led to. The
// first two answers are the observed ones (the sample's error-orphan-output and
// error-missing-output, in the gateway's key order, with no code member, the
// call_id of the request put in); the third is constructed from the documents,
// in the same envelope, and says so in the endpoint table.
func (t *responsesTemplates) pairingRefusal(items []inputItem) (string, bool) {
	called := map[string]bool{}
	answered := map[string]bool{}
	for _, item := range items {
		switch item.kind() {
		case "function_call":
			called[item.CallID] = true
		case "function_call_output":
			if !called[item.CallID] {
				return refusalText(t.orphanOutputRefusal, func(message string) string {
					return strings.Replace(message, "nope_1", item.CallID, 1)
				}), true
			}
			answered[item.CallID] = true
		}
	}
	for _, item := range items {
		if item.kind() == "function_call" && !answered[item.CallID] {
			return refusalText(t.missingOutputRefusal, func(message string) string {
				return strings.Replace(message, "c_1", item.CallID, 1)
			}), true
		}
	}
	for index, item := range items {
		if item.kind() != "reasoning" {
			continue
		}
		next := index + 1
		for next < len(items) && items[next].kind() == "reasoning" {
			next++
		}
		followed := next < len(items) && (items[next].kind() == "function_call" ||
			(items[next].kind() == "message" && items[next].Role == "assistant"))
		if !followed {
			return refusalText(t.orphanOutputRefusal, func(string) string {
				if item.ID == "" {
					return "Item of type 'reasoning' was provided without its required following item."
				}
				return fmt.Sprintf("Item '%s' of type 'reasoning' was provided without its required following item.", item.ID)
			}), true
		}
	}
	return "", false
}
