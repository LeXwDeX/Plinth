package generationtool

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestReferencesRequireCurrentTurnOrExplicitHistorySelection(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chatSchema := product.Application().Chat
	executor := Executor{chat: chatSchema}
	history := []chat.Message{
		{Role: chatSchema.Roles.User, ImageURLs: []string{"old-image"}},
		{Role: chatSchema.Roles.Assistant},
		{Role: chatSchema.Roles.User, Content: "create a fresh image"},
	}
	command := chat.ToolCommand{History: history}
	if got, err := executor.selectReferences(command, nil); err != nil || len(got) != 0 {
		t.Fatalf("fresh text request inherited references: %v", got)
	}
	command.History[2].ImageURLs = []string{"new-image"}
	if got, err := executor.selectReferences(command, nil); err != nil || !reflect.DeepEqual(got, []string{"new-image"}) {
		t.Fatalf("current turn references = %v", got)
	}
	command.Call = agents.ToolCall{Arguments: json.RawMessage(`{"reference_image_indices":[1]}`)}
	arguments := map[string]json.RawMessage{
		chatSchema.Context.ToolReferenceArgument: json.RawMessage(`[1]`),
	}
	if got, err := executor.selectReferences(command, arguments); err != nil || !reflect.DeepEqual(got, []string{"old-image"}) {
		t.Fatalf("explicit history references = %v", got)
	}
	arguments[chatSchema.Context.ToolReferenceArgument] = json.RawMessage(`[]`)
	if got, err := executor.selectReferences(command, arguments); err != nil || len(got) != 0 {
		t.Fatalf("explicit fresh generation still had references: %v", got)
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`[0]`), json.RawMessage(`[3]`), json.RawMessage(`null`), json.RawMessage(`"1"`)} {
		arguments[chatSchema.Context.ToolReferenceArgument] = raw
		if _, err := executor.selectReferences(command, arguments); err == nil {
			t.Fatalf("invalid reference selection %s accepted", raw)
		}
	}
	command.Options.ReferenceImageIndices = []int{3}
	arguments[chatSchema.Context.ToolReferenceArgument] = json.RawMessage(`[1]`)
	if _, err := executor.selectReferences(command, arguments); err == nil {
		t.Fatal("invalid human-selected reference was replaced by tool arguments")
	}
}

func TestReferenceSelectionErrorsUseProductSchemaMessages(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	schema.Presentation.Messages.InvalidReferenceIndices = "configured invalid selection"
	schema.Presentation.Messages.ReferenceIndexOutOfRangeTemplate = "configured {index} of {count}"
	executor := Executor{chat: schema}
	command := chat.ToolCommand{History: []chat.Message{{ImageURLs: []string{"first-image"}}}}
	argument := schema.Context.ToolReferenceArgument
	if _, err := executor.selectReferences(command, map[string]json.RawMessage{
		argument: json.RawMessage(`null`),
	}); err == nil || err.Error() != "configured invalid selection" {
		t.Fatalf("invalid selection message = %v", err)
	}
	if _, err := executor.selectReferences(command, map[string]json.RawMessage{
		argument: json.RawMessage(`[2]`),
	}); err == nil || err.Error() != "configured 2 of 1" {
		t.Fatalf("out of range message = %v", err)
	}
}
