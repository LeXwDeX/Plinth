package generationtool_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestOfferingParametersApplyAfterSelectionAndBeforeExplicitParameters(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	for _, tc := range []struct {
		name     string
		explicit map[string]json.RawMessage
		want     string
	}{
		{"legacy defaults overridden", map[string]json.RawMessage{}, "scoped"},
		{"explicit wins", map[string]json.RawMessage{"quality": json.RawMessage(`"explicit"`)}, "explicit"},
		{"direct callers remain explicit", nil, "legacy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := application.Chat.Tools[0]
			tool.DefaultParameters = map[string]json.RawMessage{"quality": json.RawMessage(`"tool-default"`)}
			offering := generation.Offering{ID: "provider/model/text", ModelID: "model", Capability: tool.FallbackCapability, Enabled: true, Timeout: time.Second, Parameters: []generation.Parameter{{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt}, {Name: "quality", Type: "string"}}}
			gateway := &fakeGateway{offerings: []generation.Offering{offering}, tasks: []generation.Task{{ID: "task", State: generation.TaskSucceeded}}}
			calls := 0
			executor, err := generationtool.New(generationtool.Config{Gateway: gateway, References: &fakeReferences{}, Chat: application.Chat, OfferingParameters: func(owner, session, id string) map[string]json.RawMessage {
				calls++
				if owner != "owner" || session != "session" || id != string(offering.ID) {
					t.Fatalf("wrong resolved scope: %s %s %s", owner, session, id)
				}
				return map[string]json.RawMessage{"quality": json.RawMessage(`"scoped"`), "prompt": json.RawMessage(`"hijack"`), "undeclared": json.RawMessage(`true`)}
			}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = executor.Execute(context.Background(), chat.ToolCommand{OwnerID: "owner", SessionID: "session", Tool: tool, Call: agents.ToolCall{ID: "call", Name: tool.Name, Arguments: json.RawMessage(`{"prompt":"paint"}`)}, Options: chat.TurnOptions{AIModel: "model", Parameters: map[string]json.RawMessage{"quality": json.RawMessage(`"legacy"`)}}, ExplicitParameters: tc.explicit}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var input map[string]any
			if err = json.Unmarshal(gateway.submitted.Input, &input); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || input["quality"] != tc.want || input["prompt"] != "paint" || input["undeclared"] != nil {
				t.Fatalf("input=%v resolverCalls=%d", input, calls)
			}
		})
	}
}
