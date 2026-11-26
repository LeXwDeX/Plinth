package chat_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// bigResultToolExecutor answers every call with the same large JSON result.
type bigResultToolExecutor struct{ content string }

func (executor bigResultToolExecutor) Execute(context.Context, chat.ToolCommand,
	func(chat.ToolObservation) error) (chat.ToolResult, error) {
	return chat.ToolResult{AgentContent: executor.content, NoSubmission: true}, nil
}

// #665: the tool result the model reads next is bounded by
// chat.agent.max_tool_result_characters and carries the Schema note; the
// default bound leaves an ordinary result untouched.
func TestOrchestratorBoundsAToolResultForTheModel665(t *testing.T) {
	schema := configuredChatSchema(t)
	if schema.Agent.MaxToolResultCharacters <= 0 || schema.Presentation.Messages.ToolResultTruncated == "" {
		t.Fatal("the default catalog declares no tool result bound")
	}
	tool := schema.Tools[0].Name
	entries := make([]map[string]string, 200)
	for index := range entries {
		entries[index] = map[string]string{"asset_id": "asset", "note": strings.Repeat("说明", 100)}
	}
	raw, _ := json.Marshal(map[string]any{"assets": entries})
	for _, test := range []struct {
		name  string
		limit int
		cut   bool
	}{{"over the bound", 4000, true}, {"default bound", schema.Agent.MaxToolResultCharacters, false}} {
		schema.Agent.MaxToolResultCharacters = test.limit
		agent := &scriptedAgent{responses: []agents.Completion{
			{ToolCalls: []agents.ToolCall{budgetCall("call-1", tool, "paint")}}, {Content: "done"}}}
		service := newChatService(t, newMemoryChatRepository())
		if _, err := processOnce(t, service, schema, agent, bigResultToolExecutor{content: string(raw)}); err != nil {
			t.Fatal(err)
		}
		if len(agent.requests) < 2 {
			t.Fatalf("%s: requests = %d", test.name, len(agent.requests))
		}
		var result string
		for _, message := range agent.requests[1].Messages {
			if message.Role == schema.Roles.Tool {
				result = message.Content
			}
		}
		if !test.cut {
			if result != string(raw) {
				t.Fatalf("%s: result changed (%d characters)", test.name, utf8.RuneCountInString(result))
			}
			continue
		}
		var decoded map[string]any
		if utf8.RuneCountInString(result) > test.limit || json.Unmarshal([]byte(result), &decoded) != nil {
			t.Fatalf("%s: result of %d characters is not bounded JSON", test.name, utf8.RuneCountInString(result))
		}
		note, _ := decoded["truncation_note"].(string)
		if !strings.Contains(note, tool) || !strings.Contains(note, "4000") ||
			!strings.Contains(note, schema.Presentation.Messages.ToolResultCutStrings) {
			t.Fatalf("%s: note = %q", test.name, note)
		}
	}
}
