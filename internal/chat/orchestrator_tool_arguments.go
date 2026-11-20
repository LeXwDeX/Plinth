package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// vetToolArguments makes the arguments of a completion's tool calls safe to
// keep (#378). They are raw JSON written by the model; the turn checkpoint,
// the next request and the reply all carry them, and one stray character made
// the whole checkpoint impossible to encode.
//
// A call whose arguments are not valid JSON is not run. Its arguments become
// an empty object, so the stored round never holds the model's text, and its
// ID is returned: the round answers it with the Schema's error text and the
// model writes the call again. Empty arguments are no arguments: the tool
// decides what that means, as before; only the bytes are normalised so that
// they always encode. The calls slice is not modified; a copy is returned
// when something had to change.
func (orchestrator *Orchestrator) vetToolArguments(command ProcessTurn, calls []agents.ToolCall) ([]agents.ToolCall, []string) {
	var malformed []string
	vetted, cloned := calls, false
	for index, call := range calls {
		arguments, invalid := vetArguments(call.Arguments)
		if invalid {
			malformed = append(malformed, call.ID)
			// The log names the call and the size, never the text: the
			// arguments may carry the person's prompt.
			orchestrator.logger.Warn(
				"模型返回的工具参数不是合法的 JSON，该调用不执行，改以工具错误交还模型",
				"session_id", command.SessionID,
				"assistant_message_id", command.AssistantMessageID,
				"call_id", call.ID,
				"tool", call.Name,
				"arguments_bytes", len(call.Arguments),
			)
		}
		if !invalid && (arguments == nil) == (call.Arguments == nil) {
			continue
		}
		if !cloned {
			vetted, cloned = slices.Clone(calls), true
		}
		vetted[index].Arguments = arguments
	}
	return vetted, malformed
}

// vetArguments is the arguments to keep for a call, and whether the model's
// were invalid JSON. Valid arguments are returned as they are.
func vetArguments(raw json.RawMessage) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	switch {
	case len(trimmed) == 0:
		return nil, false
	case json.Valid(trimmed):
		return raw, false
	default:
		// An empty object: valid for every encoder, and none of the model's text.
		return json.RawMessage(`{}`), true
	}
}

// toolArgumentsInvalidText is the tool result of a call whose arguments were
// not valid JSON, in the turn's locale.
func (orchestrator *Orchestrator) toolArgumentsInvalidText(ctx context.Context) string {
	return orchestrator.text(ctx, orchestrator.schema.Presentation.Messages.ToolArgumentsInvalid)
}

// toolUnavailableText is the tool result of a call to a tool this reply
// cannot run, in the turn's locale.
func (orchestrator *Orchestrator) toolUnavailableText(ctx context.Context, name string) string {
	return strings.ReplaceAll(orchestrator.text(ctx, orchestrator.schema.Presentation.Messages.ToolUnavailable), "{tool}", name)
}
