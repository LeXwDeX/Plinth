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
