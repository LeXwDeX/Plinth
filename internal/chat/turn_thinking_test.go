package chat_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// A turn freezes the thinking contract of its model when it starts: the wire it
// is served on, how it carries its thinking and whether its vendor requires the
// reasoning back. A model that matches no profile, and a deployment with
// thinking off, carry no thinking at all (#377).
func TestModelPolicyFreezesTheThinkingContractOfTheModels(t *testing.T) {
	policy := chat.ModelPolicy{
		Model: "deepseek",
		Thinking: &catalog.ChatThinkingSchema{Enabled: true, ModelProfiles: []catalog.ChatThinkingModelProfile{
			{ModelPrefixes: []string{"deepseek"}, ReasoningPassback: true, ThinkingKind: catalog.ChatThinkingPlaintext},
			{ModelPrefixes: []string{"gpt-astra"}, WireAPI: catalog.ChatWireResponses, ThinkingKind: catalog.ChatThinkingEncrypted},
		}},
	}
	plaintext := policy.TurnThinking("DeepSeek-Flash")
	if plaintext.Wire != catalog.ChatWireCompletions || plaintext.Kind != agents.ReasoningPlaintext || !plaintext.Passback {
		t.Fatalf("deepseek contract = %#v", plaintext)
	}
	encrypted := policy.TurnThinking("gpt-astra")
	if encrypted.Wire != catalog.ChatWireResponses || encrypted.Kind != agents.ReasoningEncrypted || encrypted.Passback {
		t.Fatalf("gpt-astra contract = %#v", encrypted)
	}
	// No profile: nothing to send back, on chat completions.
	plain := policy.TurnThinking("gpt-4o")
	if plain.Wire != catalog.ChatWireCompletions || plain.Kind != agents.ReasoningNone || plain.Passback {
		t.Fatalf("model without a profile = %#v", plain)
	}
	off := chat.ModelPolicy{Model: "deepseek", Thinking: &catalog.ChatThinkingSchema{}}
	if got := off.TurnThinking("deepseek"); got.Kind != agents.ReasoningNone || got.Passback {
		t.Fatalf("thinking off = %#v", got)
	}
	var absent chat.ModelPolicy
	if got := absent.TurnThinking("deepseek"); got.Kind != agents.ReasoningNone {
		t.Fatalf("a deployment without thinking = %#v", got)
	}
}

// The turn stores the contract it started with, and every model request of the
// turn carries it, so a Catalog that changed under the turn cannot change how
// its reasoning is sent.
func TestTurnFreezesTheThinkingContractWithItself(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID: "owner-a", SessionID: session.ID, Content: "draw",
	})
	if err != nil {
		t.Fatal(err)
	}
	turn := repository.turns[assistant.ID]
	if turn.Options.Thinking == nil || turn.Options.Thinking.Kind != agents.ReasoningPlaintext ||
		!turn.Options.Thinking.Passback || turn.Options.Thinking.Wire != catalog.ChatWireCompletions {
		t.Fatalf("frozen contract = %#v", turn.Options.Thinking)
	}
	// It survives a restart of the turn, which is what a checkpoint resumes
	// from.
	encoded, err := json.Marshal(turn.Options)
	if err != nil {
		t.Fatal(err)
	}
	var options chat.TurnOptions
	if err := json.Unmarshal(encoded, &options); err != nil {
		t.Fatal(err)
	}
	if options.Thinking == nil || options.Thinking.Kind != agents.ReasoningPlaintext || !options.Thinking.Passback {
		t.Fatalf("restored contract = %#v", options.Thinking)
	}
}
