package chat_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #665: valid JSON that breaks an Agent tool's declared parameters is not run;
// the model gets the Schema's text naming the field, and a matching call of
// the same tool still runs.
func TestAgentToolArgumentsAreCheckedAgainstTheDeclaration(t *testing.T) {
	h := newHungTurn(t, func(schema *catalog.ChatSchema) {
		configuredRejectionTexts(schema)
		schema.Presentation.Messages.ToolArgumentsSchemaInvalid = "configured {tool}: {reason}"
	})
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{
			{ID: "raw-1", Name: "get_task", Arguments: json.RawMessage(`{"task_id":5}`)},
			{ID: "raw-3", Name: "get_task", Arguments: json.RawMessage(`{}`)},
			// An undeclared property is the tool's business, as before.
			{ID: "raw-2", Name: "get_task", Arguments: json.RawMessage(`{"task_id":"t1","extra":true}`)},
			{ID: "raw-4", Name: "get_task", Arguments: json.RawMessage(`{"task_id":"t1"}`)},
		}},
		{Content: "configured reply"},
	}}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
	if _, err := h.run(agent, tools); err != nil {
		t.Fatalf("Process = %v", err)
	}
	close(tools.commands)
	var ran []string
	for command := range tools.commands {
		ran = append(ran, string(command.Call.Arguments))
	}
	if len(ran) != 2 || ran[0] != `{"task_id":"t1","extra":true}` || ran[1] != `{"task_id":"t1"}` {
		t.Fatalf("executed = %q", ran)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("agent requests = %d", len(agent.requests))
	}
	results := toolResults(h.schema, agent.requests[1])
	want := []string{
		"configured get_task: task_id must be of type string",
		"configured get_task: task_id is required",
	}
	if len(results) != 4 {
		t.Fatalf("tool results = %#v", results)
	}
	for index, text := range want {
		if results[index].Content != text {
			t.Fatalf("result %d = %q, want %q", index, results[index].Content, text)
		}
	}
}

// #665: a call whose arguments are not valid JSON is told so, not that the
// empty object put in their place lacks a required field; a null under an
// optional parameter is that parameter left out and the call runs, while a
// null required parameter is still refused.
func TestAgentToolArgumentsInvalidJSONAndOptionalNull(t *testing.T) {
	h := newHungTurn(t, func(schema *catalog.ChatSchema) {
		configuredRejectionTexts(schema)
		schema.Presentation.Messages.ToolArgumentsSchemaInvalid = "configured {tool}: {reason}"
	})
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{
			{ID: "raw-1", Name: "get_task", Arguments: json.RawMessage(`{"task_id":`)},
			{ID: "raw-2", Name: "get_task", Arguments: json.RawMessage(`{"task_id":null}`)},
			{ID: "raw-3", Name: "list_offerings", Arguments: json.RawMessage(`{"capability":null}`)},
		}},
		{Content: "configured reply"},
	}}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 8)}
	if _, err := h.run(agent, tools); err != nil {
		t.Fatalf("Process = %v", err)
	}
	close(tools.commands)
	var ran []string
	for command := range tools.commands {
		ran = append(ran, command.Call.Name+string(command.Call.Arguments))
	}
	if len(ran) != 1 || ran[0] != `list_offerings{"capability":null}` {
		t.Fatalf("executed = %q", ran)
	}
	results := toolResults(h.schema, agent.requests[1])
	if len(results) != 3 || results[0].Content != "configured arguments invalid" ||
		results[1].Content != "configured get_task: task_id must be of type string" {
		t.Fatalf("tool results = %#v", results)
	}
}

// #665 (wave 2): a string enum value is trimmed of whitespace before it is
// checked, and the tool runs with the trimmed value; a value outside the enum
// is refused with the Schema's text listing the allowed values.
func TestAgentToolEnumArgumentsAreTrimmedAndListTheChoices(t *testing.T) {
	h := newHungTurn(t, func(schema *catalog.ChatSchema) {
		configuredRejectionTexts(schema)
		schema.Presentation.Messages.ToolArgumentsSchemaInvalid = "configured {tool}: {reason}"
		schema.Presentation.Messages.ToolArgumentNotAllowed = "{field} one of {choices}"
		for index := range schema.AgentTools {
			if schema.AgentTools[index].Name == "get_task" {
				schema.AgentTools[index].Parameters = json.RawMessage(`{"type":"object","additionalProperties":false,
					"properties":{"task_id":{"type":"string"},"mode":{"type":"string","enum":["fast","full"]}},
					"required":["task_id"]}`)
			}
		}
	})
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{
			{ID: "enum-1", Name: "get_task", Arguments: json.RawMessage(`{"task_id":"t1","mode":" full\n"}`)},
			{ID: "enum-2", Name: "get_task", Arguments: json.RawMessage(`{"task_id":"t1","mode":"slow"}`)},
		}},
		{Content: "configured reply"},
	}}
	tools := &recordingToolExecutor{commands: make(chan chat.ToolCommand, 4)}
	if _, err := h.run(agent, tools); err != nil {
		t.Fatalf("Process = %v", err)
	}
	close(tools.commands)
	var ran []string
	for command := range tools.commands {
		ran = append(ran, string(command.Call.Arguments))
	}
	if len(ran) != 1 || ran[0] != `{"mode":"full","task_id":"t1"}` {
		t.Fatalf("executed = %q", ran)
	}
	results := toolResults(h.schema, agent.requests[1])
	if len(results) != 2 || results[1].Content != "configured get_task: mode one of fast, full" {
		t.Fatalf("tool results = %#v", results)
	}
}
