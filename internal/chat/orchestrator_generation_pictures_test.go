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

// pictureToolExecutor returns one finished 2D generation result with a fresh
// picture; it never touches a gateway.
type pictureToolExecutor struct{}

func (pictureToolExecutor) Execute(context.Context, chat.ToolCommand, func(chat.ToolObservation) error) (chat.ToolResult, error) {
	url := "/api/v1/tasks/task-1/assets/asset-1"
	return chat.ToolResult{
		TaskID:       "task-1",
		ImageURLs:    []string{url},
		AgentContent: `{"success":true,"task_id":"task-1"}`,
		Pictures:     []chat.ToolPicture{{Handle: "@img1", URL: url, Fresh: true}},
	}, nil
}

// The picture a 2D generation just produced reaches the model in the same
// turn, labelled as a fresh result rather than a history lookup (#245).
func TestGenerationPicturesReachTheSameTurn(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	schema := configuredChatSchema(t)
	schema.Agent.Vision = true
	schema.Context.TextPartKind = "text"
	schema.Context.ImagePartKind = "image"
	schema.Context.ResultImageViewIntro = "fresh intro"
	schema.Context.ResultImageViewLabelTemplate = "\n[fresh {handle}]"
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{{ID: "call-1", Name: "refine_image", Arguments: json.RawMessage(`{"prompt":"a cube"}`)}}},
		{Content: "done"},
	}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "configured-model", Agent: agent, Tools: pictureToolExecutor{},
		VisionInline: func(context.Context, generation.OwnerID, string) (string, bool) {
			return "data:image/png;base64,AAAA", true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw a cube",
		Options: chat.TurnOptions{CreativeDomain: "image", AIModel: "configured-model"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID,
		AssistantMessageID: assistant.ID, Options: chat.TurnOptions{CreativeDomain: "image", AIModel: "configured-model"}}); err != nil {
		t.Fatal(err)
	}
	if len(agent.requests) < 2 {
		t.Fatalf("agent requests = %d", len(agent.requests))
	}
	var pictures *agents.Message
	for index := len(agent.requests[1].Messages) - 1; index >= 0; index-- {
		message := agent.requests[1].Messages[index]
		if message.PicturesOnly {
			pictures = &message
			break
		}
	}
	if pictures == nil || len(pictures.Parts) < 3 {
		t.Fatalf("no picture message in the follow-up: %+v", agent.requests[1].Messages)
	}
	if pictures.Parts[0].Text != "fresh intro" || !strings.Contains(pictures.Parts[2].Text, "fresh @img1") {
		t.Fatalf("picture message = %+v", pictures.Parts)
	}
	if pictures.Parts[1].URL == "" || !strings.HasPrefix(pictures.Parts[1].URL, "data:image/") {
		t.Fatalf("picture part = %+v", pictures.Parts[1])
	}
}