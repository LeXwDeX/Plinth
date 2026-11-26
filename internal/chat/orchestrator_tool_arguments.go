package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema/toolschema"
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
//
// #665: valid JSON for an Agent tool must also match the tool's declared
// parameters (required, type, enum, length, item and number bounds) in the
// registry the turn runs from. A property the declaration does not name is
// left to the tool, as before: most tools ignore it, and a strict one names it
// in its own refusal. Such a call is not run either; its arguments are kept,
// and its result names the field (toolArgumentsInvalidText). Generation tools
// keep their lenient decoding: their arguments are mapped onto Offering
// parameters, which check them.
//
// The second list holds every call that is not run; the third the ones among
// them whose arguments were not valid JSON, so their result says that rather
// than what the empty object put in their place lacks.
func (orchestrator *Orchestrator) vetToolArguments(ctx context.Context, command ProcessTurn, calls []agents.ToolCall) ([]agents.ToolCall, []string, []string) {
	var malformed, invalidJSON []string
	vetted, cloned := calls, false
	registry := orchestrator.turnRegistry(ctx)
	for index, call := range calls {
		arguments, invalid := vetArguments(call.Arguments)
		// A tool this reply cannot run, or one its scope refuses, keeps the
		// answer that says so; its arguments are not the problem.
		_, offered := registry.resolve(call.Name, false)
		trimmed := false
		if !invalid && offered && !orchestrator.scopeRefuses(registry, call.Name, command.Options.AgentScope, false) {
			// The tool runs with the enum values the declaration names, so a
			// value written with surrounding whitespace is kept trimmed.
			if normalized, changed := trimAgentToolEnums(registry, call); changed {
				arguments, call.Arguments, trimmed = normalized, normalized, true
			}
			if reason := agentToolArgumentsViolation(registry, call); reason != "" {
				malformed = append(malformed, call.ID)
				orchestrator.logger.Warn(
					"模型给 Agent 工具的参数不符合工具定义，该调用不执行，改以工具错误交还模型",
					"session_id", command.SessionID,
					"assistant_message_id", command.AssistantMessageID,
					"call_id", call.ID,
					"tool", call.Name,
					"reason", reason,
				)
			}
		}
		if invalid {
			malformed = append(malformed, call.ID)
			invalidJSON = append(invalidJSON, call.ID)
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
		if !invalid && !trimmed && (arguments == nil) == (call.Arguments == nil) {
			continue
		}
		if !cloned {
			vetted, cloned = slices.Clone(calls), true
		}
		vetted[index].Arguments = arguments
	}
	return vetted, malformed, invalidJSON
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

// agentToolArgumentsViolation is where a call's arguments break the declared
// parameters of the Agent tool it names (#665), or "" when they match, the
// tool is not an Agent tool, or the registry does not know it. The reason
// names the path and the rule, never a value the model wrote.
func agentToolArgumentsViolation(registry *toolRegistry, call agents.ToolCall) string {
	violation := agentToolArgumentsError(registry, call)
	if violation == nil {
		return ""
	}
	return violationField(violation) + " " + violation.Message
}

// agentToolArgumentsError is the first place a call's arguments break the
// declaration of the Agent tool it names, or nil (see
// agentToolArgumentsViolation). A string an enum constrains is compared
// without its surrounding whitespace (#665), as the tool receives it.
func agentToolArgumentsError(registry *toolRegistry, call agents.ToolCall) *toolschema.ValidationError {
	schema := agentToolDeclaration(registry, call.Name)
	if schema == nil {
		return nil
	}
	openObjects(schema)
	arguments := bytes.TrimSpace(call.Arguments)
	if len(arguments) == 0 {
		arguments = []byte(`{}`)
	}
	arguments, _ = schema.TrimEnumStrings(arguments)
	var violation *toolschema.ValidationError
	// #665: a null under an optional parameter is that parameter left out,
	// as the tool's own decoding reads it; a required one stays refused.
	if err := schema.ValidateWith(arguments, toolschema.ValidateOptions{OptionalNullAbsent: true}); errors.As(err, &violation) {
		return violation
	}
	return nil
}

// agentToolDeclaration is the parsed parameter declaration of the Agent tool
// the registry names, or nil when it is not an Agent tool, the registry does
// not know it, or the declaration does not parse.
func agentToolDeclaration(registry *toolRegistry, name string) *toolschema.Schema {
	if registry == nil {
		return nil
	}
	tool, known := registry.byName[name]
	if !known || tool.generation != nil {
		return nil
	}
	schema, err := toolschema.Parse(tool.parameters, catalog.AgentToolArgumentOptions)
	if err != nil {
		// A frozen declaration from before #665 may not parse; it keeps the
		// lenient handling it was written for.
		return nil
	}
	return schema
}

// trimAgentToolEnums is the call's arguments with the whitespace around every
// string an enum of the Agent tool's declaration constrains removed, and
// whether that changed them (#665). Only whitespace goes; a value that is
// still not a declared one is refused by agentToolArgumentsViolation.
func trimAgentToolEnums(registry *toolRegistry, call agents.ToolCall) (json.RawMessage, bool) {
	schema := agentToolDeclaration(registry, call.Name)
	if schema == nil || len(bytes.TrimSpace(call.Arguments)) == 0 {
		return call.Arguments, false
	}
	return schema.TrimEnumStrings(call.Arguments)
}

// violationField is the argument a violation names, or "the arguments" for
// the whole object.
func violationField(violation *toolschema.ValidationError) string {
	field := strings.TrimPrefix(strings.TrimPrefix(violation.Path, "$"), ".")
	if field == "" {
		return "the arguments"
	}
	return field
}

// openObjects drops additionalProperties: false from a parsed declaration, so
// only the declared properties' own rules are enforced (see vetToolArguments).
func openObjects(schema *toolschema.Schema) {
	if schema == nil {
		return
	}
	if schema.AdditionalProperties != nil && !*schema.AdditionalProperties {
		schema.AdditionalProperties = nil
	}
	openObjects(schema.Items)
	openObjects(schema.AdditionalSchema)
	for _, property := range schema.Properties {
		openObjects(property)
	}
}

// toolArgumentsInvalidText is the tool result of a call that was not run
// because of its arguments, in the turn's locale: invalid JSON (invalidJSON:
// its arguments are the empty object put in their place, whose violations
// are not the model's), or (#665) arguments that break the Agent tool's
// declaration, naming the field.
func (orchestrator *Orchestrator) toolArgumentsInvalidText(ctx context.Context, call agents.ToolCall, invalidJSON bool) string {
	messages := orchestrator.schema.Presentation.Messages
	if template := messages.ToolArgumentsSchemaInvalid; !invalidJSON && strings.TrimSpace(template) != "" {
		if violation := agentToolArgumentsError(orchestrator.turnRegistry(ctx), call); violation != nil {
			reason := violationField(violation) + " " + violation.Message
			// #665: a value outside an enum names the allowed ones in the
			// Schema's words.
			if choices := messages.ToolArgumentNotAllowed; len(violation.Allowed) > 0 && strings.TrimSpace(choices) != "" {
				reason = strings.NewReplacer("{field}", violationField(violation),
					"{choices}", toolschema.EnumChoices(violation.Allowed)).Replace(orchestrator.text(ctx, choices))
			}
			return strings.NewReplacer("{tool}", call.Name, "{reason}", reason).
				Replace(orchestrator.text(ctx, template))
		}
	}
	return orchestrator.text(ctx, messages.ToolArgumentsInvalid)
}

// toolUnavailableText is the tool result of a call to a tool this reply
// cannot run, in the turn's locale.
func (orchestrator *Orchestrator) toolUnavailableText(ctx context.Context, name string) string {
	return strings.ReplaceAll(orchestrator.text(ctx, orchestrator.schema.Presentation.Messages.ToolUnavailable), "{tool}", name)
}
