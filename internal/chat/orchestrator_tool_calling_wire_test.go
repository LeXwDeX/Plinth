package chat_test

// #431: the Responses Adapter reports a tool call's start with the content-free
// ToolCalling delta (#375's counterpart on the Responses wire), so a session
// whose model runs on /v1/responses shows the preparing-tool status while the
// call's arguments stream. The test drives the real openairesponses Adapter
// over the fake upstream's /v1/responses — whose stream is the one observed on
// 2026-11-20, a reasoning item and then the function_call item with its
// arguments fragments — through the orchestrator, and compares the event
// sequence with the one the #375 test pins for a scripted Agent.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents/openairesponses"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

func TestOrchestratorShowsPreparingToolStatusForTheResponsesWire(t *testing.T) {
	schema := configuredChatSchema(t)
	// The stored messages carry the role names the service writes, and the
	// Responses Adapter sends them as they are: the wire's own names here, so
	// the fake answers the tool marker of a "user" message.
	schema.Roles.System, schema.Roles.User = "system", "user"
	schema.Roles.Assistant, schema.Roles.Tool = "assistant", "tool"
	schema.Stream.EventTypes.Thinking = "configured-thinking"
	schema.Presentation.Status.Thinking = "configured thinking"
	schema.Stream.EventTypes.Status = "configured-status-event"
	schema.Presentation.Status.Generating = "configured generating"
	schema.Presentation.Status.PreparingTool = "configured preparing tool"
	schema.Stream.EventTypes.TaskRegistered = "configured-registered"

	fake, err := registry.New(fakeupstream.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	// A model without a profile, so the request carries the Schema temperature
	// the fake accepts for every model but the constrained gpt-astra.
	adapter, err := openairesponses.New(openairesponses.Config{
		BaseURL: server.URL + openaiv1.Prefix, APIKey: "fake-key",
		EndpointPath: "/responses", AuthorizationName: "Authorization", AuthorizationType: "Bearer",
		RequestTimeout: 10 * time.Second, ScannerMaxBytes: 1 << 20, ErrorBodyMaxBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := newChatServiceWith(t, newMemoryChatRepository(), func(config *chat.ServiceConfig) {
		config.UserRole, config.AssistantRole = "user", "assistant"
	})
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "configured-model",
		Agent: adapter, Tools: scriptedToolExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	content := openaiv1.ReasoningMarker + " " + openaiv1.ToolMarkerFor(schema.Tools[0].Name, `{"prompt":"paint"}`)
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	events, err := service.ListEvents(ctx, "owner-a", session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}

	var sequence []string
	var streamed strings.Builder
	for _, event := range events {
		kind, _ := event.Data["configured-type"].(string)
		content, _ := event.Data["configured-content"].(string)
		switch kind {
		case "configured-thinking":
			sequence = append(sequence, "thinking")
		case "configured-status-event":
			sequence = append(sequence, "status:"+content)
		case "configured-registered":
			if len(sequence) == 0 || sequence[len(sequence)-1] != "registered" {
				sequence = append(sequence, "registered")
			}
		case "configured-text":
			// The fake streams the answer in pieces; one entry for the run.
			streamed.WriteString(content)
			if strings.TrimSpace(content) != "" &&
				(len(sequence) == 0 || sequence[len(sequence)-1] != "text") {
				sequence = append(sequence, "text")
			}
		}
	}
	// The call's start replaces the thinking status while its arguments
	// stream, before the generating status and the task registration; the
	// round after the tool run opens with the thinking status again.
	want := []string{
		"thinking", "status:configured preparing tool", "status:configured generating",
		"registered", "thinking", "text",
	}
	if strings.Join(sequence, "|") != strings.Join(want, "|") {
		t.Fatalf("sequence = %v, want %v", sequence, want)
	}
	if streamed.String() != openaiv1.FixedReply || messages[1].Content != openaiv1.FixedReply {
		t.Fatalf("text events = %q, stored = %q", streamed.String(), messages[1].Content)
	}
}
