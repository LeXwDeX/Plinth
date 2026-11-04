package chat_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// reviewPictureTools returns a read-only render result: two pictures, no
// Task, and the view array the Schema pairs with the handles.
type reviewPictureTools struct {
	commands []chat.ToolCommand
}

func (tools *reviewPictureTools) Execute(
	_ context.Context,
	command chat.ToolCommand,
	_ func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	tools.commands = append(tools.commands, command)
	return chat.ToolResult{
		AgentContent: `{"ok":true,"views":[{"view":"front"},{"view":"back"}]}`,
		ImageURLs:    []string{"/api/assets/render-1", "/api/assets/render-2"},
		Pictures: []chat.ToolPicture{
			{URL: "/api/assets/render-1"},
			{URL: "/api/assets/render-2"},
		},
		NoSubmission: true,
	}, nil
}

func newReviewOrchestrator(t *testing.T, vision bool) (*chat.Service, *scopeRig) {
	t.Helper()
	schema := configuredChatSchema(t)
	schema.Agent.Vision = vision
	policy := chat.NewHandlePolicy(schema.Context)
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Handles = policy
		config.Attachments = &resultVerifier{}
	})
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{{ID: "render", Name: "render_model_views", Arguments: []byte(`{"model":"task-a"}`)}}},
		{Content: "done"},
	}}
	tools := &reviewPictureTools{}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "m", Agent: agent, Tools: tools,
		VisionInline: func(_ context.Context, _ generation.OwnerID, _ string) (string, bool) {
			return "data:image/png;base64,AAAA", true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rig := &scopeRig{repository: repository, service: service, orchestrator: orchestrator, agent: agent, tools: nil, schema: schema}
	return service, rig
}

// TestReadOnlyToolPicturesGetHandlesAndEachViewNamesItsOwn covers #103: a
// render has no Task, so recordTurnResult must attach its pictures on the
// image URLs alone; the result tells the Agent each view's handle.
func TestReadOnlyToolPicturesGetHandlesAndEachViewNamesItsOwn(t *testing.T) {
	service, rig := newReviewOrchestrator(t, true)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "show me the model"})
	if err != nil {
		t.Fatal(err)
	}
	if err := rig.orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if len(rig.agent.requests) != 2 {
		t.Fatalf("agent saw %d requests", len(rig.agent.requests))
	}

	// The tool reply the Agent read names both handles, and each view carries
	// its own handle in render order.
	var toolContent string
	for _, message := range rig.agent.requests[1].Messages {
		if message.Role == rig.schema.Roles.Tool {
			toolContent = message.Content
		}
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal([]byte(toolContent), &result); err != nil {
		t.Fatalf("tool result = %q: %v", toolContent, err)
	}
	var handles []string
	if err := json.Unmarshal(result[rig.schema.Context.ResultHandlesField], &handles); err != nil ||
		len(handles) != 2 || handles[0] != "@img1" || handles[1] != "@img2" {
		t.Fatalf("handles = %v (%v)", handles, err)
	}
	var views []map[string]any
	if err := json.Unmarshal(result[rig.schema.ModelReview.ResultViewsField], &views); err != nil {
		t.Fatalf("views = %s: %v", result[rig.schema.ModelReview.ResultViewsField], err)
	}
	if len(views) != 2 || views[0]["view"] != "front" || views[0]["handle"] != "@img1" ||
		views[1]["view"] != "back" || views[1]["handle"] != "@img2" {
		t.Fatalf("views = %v", views)
	}

	// The pictures ride to the model after the tool reply, each followed by
	// its handle, so the model can tell the views apart.
	var labels []string
	for _, message := range rig.agent.requests[1].Messages {
		if message.Role == rig.schema.Roles.User && len(message.Parts) > 0 {
			for _, part := range message.Parts {
				if part.Kind == rig.schema.Context.TextPartKind && strings.Contains(part.Text, "@img") {
					labels = append(labels, part.Text)
				}
			}
		}
	}
	if len(labels) != 2 || !strings.Contains(labels[0], "@img1") || !strings.Contains(labels[1], "@img2") {
		t.Fatalf("picture labels = %v", labels)
	}

	// The final reply keeps the same handles and slots.
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	final := messages[len(messages)-1]
	if got := attachmentSummary(final.Attachments); len(got) != 2 || !strings.HasPrefix(got[0], "@img1/image//1") ||
		!strings.HasPrefix(got[1], "@img2/image//2") {
		t.Fatalf("final attachments = %v", got)
	}
}

// TestReadOnlyToolPicturesStayAttachedWhenTheAgentCannotSeeThem keeps the
// handles and JSON while telling the Agent, in the Schema's words, that the
// pictures are not visible to it.
func TestReadOnlyToolPicturesStayAttachedWhenTheAgentCannotSeeThem(t *testing.T) {
	service, rig := newReviewOrchestrator(t, false)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "show me the model"})
	if err != nil {
		t.Fatal(err)
	}
	if err := rig.orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	var toolContent string
	for _, message := range rig.agent.requests[1].Messages {
		if message.Role == rig.schema.Roles.Tool {
			toolContent = message.Content
		}
	}
	if !strings.Contains(toolContent, rig.schema.Context.HistoryImageVisionOffMessage) ||
		!strings.Contains(toolContent, rig.schema.Context.ResultHandlesField) ||
		!strings.Contains(toolContent, "@img1") {
		t.Fatalf("tool reply without vision = %q", toolContent)
	}
	for _, message := range rig.agent.requests[1].Messages {
		if message.Role == rig.schema.Roles.User && len(message.Parts) > 0 {
			t.Fatalf("pictures were sent to a model without vision: %+v", message.Parts)
		}
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final := messages[len(messages)-1]; len(final.Attachments) != 2 {
		t.Fatalf("final attachments = %v", final.Attachments)
	}
}
