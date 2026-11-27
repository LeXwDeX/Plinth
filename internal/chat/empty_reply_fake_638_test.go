package chat_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/openairesponses"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

// #638 end to end on the fake: the real Responses Adapter, inside the retrying
// round, over the fake's two observed gpt-astra answers
// (openaiv1.EmptyCompletionMarker, openaiv1.SelfImageMarker) through the
// orchestrator. A round that only reasoned is sent once more and then ends the
// turn with the empty_response reason; a picture the model drew itself ends it
// with the inline_image reason after one request.
func TestTheFakesObservedAstraRoundsEndTheTurnWithTheirReasons(t *testing.T) {
	for name, want := range map[string]struct {
		marker   string
		requests int
		reason   string
	}{
		"reasoning only": {openaiv1.EmptyCompletionMarker, 2, configuredEndReasons.EmptyResponse},
		"self-drawn":     {openaiv1.SelfImageMarker, 1, configuredEndReasons.InlineImage},
	} {
		t.Run(name, func(t *testing.T) {
			schema := configuredChatSchema(t)
			schema.Roles.System, schema.Roles.User = "system", "user"
			schema.Roles.Assistant, schema.Roles.Tool = "assistant", "tool"
			fake, err := registry.New(fakeupstream.Config{})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(fake)
			t.Cleanup(server.Close)
			adapter, err := openairesponses.New(openairesponses.Config{
				BaseURL: server.URL + openaiv1.Prefix, APIKey: "fake-key",
				EndpointPath: "/responses", AuthorizationName: "Authorization", AuthorizationType: "Bearer",
				RequestTimeout: 10 * time.Second, ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 4096,
			})
			if err != nil {
				t.Fatal(err)
			}
			agent := agents.NewRetrying(adapter, agents.RetryPolicy{
				MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond, EmptyRetries: 1,
				Wait: func(context.Context, time.Duration) error { return nil },
			})
			service := newChatServiceWith(t, newMemoryChatRepository(), func(config *chat.ServiceConfig) {
				config.UserRole, config.AssistantRole = "user", "assistant"
			})
			orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
				Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: noToolExecutor{},
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
			if err != nil {
				t.Fatal(err)
			}
			assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "go on " + want.marker})
			if err != nil {
				t.Fatal(err)
			}
			if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}); err != nil {
				t.Fatal(err)
			}
			if sent := len(fake.Env().ChatRequests()); sent != want.requests {
				t.Fatalf("requests = %d, want %d", sent, want.requests)
			}
			turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantTurnEnd(t, turn, "configured-turn-done", want.reason)
			messages, err := service.ListMessages(ctx, "owner-a", session.ID)
			if err != nil {
				t.Fatal(err)
			}
			text := schema.Presentation.Messages.AgentEmptyResponse
			if want.reason == configuredEndReasons.InlineImage {
				text = schema.Presentation.Messages.AgentInlineImageOutput
			}
			if last := messages[len(messages)-1]; !strings.HasPrefix(last.Content, text) {
				t.Fatalf("assistant = %q, want the Schema's %q", last.Content, text)
			}
		})
	}
}
