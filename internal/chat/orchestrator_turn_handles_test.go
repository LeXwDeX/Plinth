package chat_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// turnHandleTools plays the two tools of the #25 reply: the first call
// produces a picture, the second names it by handle. It resolves handles
// against what the orchestrator gave it, like the real tools do.
type turnHandleTools struct {
	policy   chat.HandlePolicy
	commands []chat.ToolCommand
	resolved []string
	refusal  error
}

func (tools *turnHandleTools) Execute(_ context.Context, command chat.ToolCommand, _ func(chat.ToolObservation) error) (chat.ToolResult, error) {
	tools.commands = append(tools.commands, command)
	var arguments struct {
		Handles []string `json:"reference_handles"`
	}
	_ = json.Unmarshal(command.Call.Arguments, &arguments)
	if len(arguments.Handles) == 0 {
		return chat.ToolResult{TaskID: "task-tree", ImageURLs: []string{"/api/v1/tasks/task-tree/assets/tree"},
			AgentContent: `{"success":true,"task_id":"task-tree"}`}, nil
	}
	resolved, err := tools.policy.ResolveHandles(command.ReferenceHistory(), command.Options.Selection, arguments.Handles, chat.AttachmentKindImage)
	if err != nil {
		tools.refusal = err
		return chat.ToolResult{AgentContent: `{"success":false}`, NoSubmission: true}, nil
	}
	for _, item := range resolved {
		tools.resolved = append(tools.resolved, item.URL)
	}
	return chat.ToolResult{AgentContent: `{"success":true}`, NoSubmission: true}, nil
}

// #25: a reply that makes a picture and then uses it. The picture gets its
// handle as soon as the call returns, the result tells the Agent the handle,
// the next step resolves it, and the final message keeps the same number.
func TestSameReplyNamesThePictureItJustMadeByHandle(t *testing.T) {
	schema := configuredChatSchema(t)
	policy := chat.NewHandlePolicy(schema.Context)
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Handles = policy
		config.Attachments = &resultVerifier{}
	})
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	// The session already has @img1 and @img2.
	repository.mu.Lock()
	repository.messages[session.ID] = []chat.Message{
		{ID: "earlier-user", SessionID: session.ID, Role: schema.Roles.User, Content: "画一张场景"},
		{ID: "earlier-reply", SessionID: session.ID, Role: schema.Roles.Assistant,
			ImageURLs: []string{"/api/v1/tasks/task-scene/assets/scene", "/api/v1/tasks/task-scene/assets/scene-b"},
			Attachments: []chat.Attachment{
				{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "task-scene", ImageSlot: 1},
				{Handle: "@img2", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "task-scene", ImageSlot: 2},
			}},
	}
	repository.mu.Unlock()
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "把中间的树转成 3D"})
	if err != nil {
		t.Fatal(err)
	}
	refine, model := "refine_image", "create_model_3d_from_image"
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{{ID: "isolate", Name: refine, Arguments: []byte(`{"prompt":"isolate the tree"}`)}}},
		{ToolCalls: []agents.ToolCall{{ID: "to-3d", Name: model, Arguments: []byte(`{"prompt":"tree","reference_handles":["@img3"]}`)}}},
		{Content: "done"},
	}}
	tools := &turnHandleTools{policy: policy}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "m", Agent: agent, Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID}); err != nil {
		t.Fatal(err)
	}

	if tools.refusal != nil || !reflect.DeepEqual(tools.resolved, []string{"/api/v1/tasks/task-tree/assets/tree"}) {
		t.Fatalf("@img3 resolved to %v (refusal %v), want the picture this reply made", tools.resolved, tools.refusal)
	}
	// The Agent was told the handle in the result it read before the second call.
	if len(agent.requests) < 2 {
		t.Fatalf("agent saw %d requests", len(agent.requests))
	}
	var told string
	for _, message := range agent.requests[1].Messages {
		if message.Role == schema.Roles.Tool && told == "" {
			told = message.Content
		}
	}
	key := schema.Context.ResultHandlesField
	var result map[string]json.RawMessage
	if key == "" || json.Unmarshal([]byte(told), &result) != nil || string(result[key]) != `["@img3"]` {
		t.Fatalf("first result %q does not name @img3 under %q", told, key)
	}
	// The final message keeps the handle the Agent was told; nothing is numbered twice.
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	final := messages[len(messages)-1]
	if got := attachmentSummary(final.Attachments); !reflect.DeepEqual(got, []string{"@img3/image/task-tree/1"}) {
		t.Fatalf("final attachments = %v", got)
	}
}

// #25: "the person's latest message" still means the person's message when
// the reply's own results ride along for reference lookups.
func TestReferenceHistoryAddsTheReplyWithoutHidingThePersonsMessage(t *testing.T) {
	user := chat.Message{Role: "user", Content: "ask"}
	command := chat.ToolCommand{History: []chat.Message{user}}
	if got := command.ReferenceHistory(); len(got) != 1 {
		t.Fatalf("a reply without results added %d messages", len(got)-1)
	}
	command.Turn = chat.Message{Role: "assistant", ImageURLs: []string{"made"}}
	got := command.ReferenceHistory()
	if len(got) != 2 || got[1].ImageURLs[0] != "made" || command.History[len(command.History)-1].Content != "ask" {
		t.Fatalf("reference history = %+v", got)
	}
	if strings.Contains(strings.Join(command.History[0].ImageURLs, ""), "made") {
		t.Fatal("history changed")
	}
}
