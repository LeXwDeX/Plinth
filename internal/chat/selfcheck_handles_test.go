package chat_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// referencePictureTools emulates compare_model_reference: one comparison
// picture, a compact summary naming the Task, and no Task of its own.
type referencePictureTools struct{}

func (tools *referencePictureTools) Execute(
	_ context.Context,
	_ chat.ToolCommand,
	_ func(chat.ToolObservation) error,
) (chat.ToolResult, error) {
	return chat.ToolResult{
		AgentContent: `{"ok":true,"views":[{"view":"front"}]}`,
		Summary:      json.RawMessage(`{"tool":"reference","task_id":"task-a","views":["front"]}`),
		ImageURLs:    []string{"/api/assets/reference-1"},
		Pictures:     []chat.ToolPicture{{URL: "/api/assets/reference-1"}},
		NoSubmission: true,
	}, nil
}

// TestSelfCheckCheckGetsTheAttachmentHandleWhenTheReplyNamesThePicture covers
// the join between the tool call and the persisted record: the tool could not
// know the handle, the orchestrator gives the picture one, and the record
// stores it so the card binds the thumbnail to its check.
func TestSelfCheckCheckGetsTheAttachmentHandleWhenTheReplyNamesThePicture(t *testing.T) {
	schema := configuredChatSchema(t)
	policy := chat.NewHandlePolicy(schema.Context)
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Handles = policy
		config.Attachments = &resultVerifier{}
	})
	store := newMemorySelfCheckStore()
	service.SetSelfChecks(store, schema.SelfCheck)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.SaveSelfCheck(ctx, "owner-a", session.ID, chat.SelfCheckRecord{
		TaskID: "task-a",
		Checks: []chat.SelfCheckCheck{{
			Round: 1, Kind: chat.SelfCheckKindReference, Tool: "compare_model_reference",
			Views: []string{"front"}, OK: true,
		}},
	}, 0); err != nil {
		t.Fatal(err)
	}
	agent := &scriptedAgent{responses: []agents.Completion{
		{ToolCalls: []agents.ToolCall{{ID: "reference", Name: "compare_model_reference", Arguments: []byte(`{"model":"task-a"}`)}}},
		{Content: "done"},
	}}
	orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
		Service: service, Schema: schema, Model: "m", Agent: agent, Tools: &referencePictureTools{},
		VisionInline: func(context.Context, generation.OwnerID, string) (string, bool) {
			return "data:image/png;base64,AAAA", true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "check it"})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Process(ctx, chat.ProcessTurn{
		OwnerID: "owner-a", SessionID: session.ID, AssistantMessageID: assistant.ID,
	}); err != nil {
		t.Fatal(err)
	}
	record, found, err := store.SelfCheck(ctx, "owner-a", session.ID, "task-a")
	if err != nil || !found || len(record.Checks) != 1 {
		t.Fatalf("record = %+v found=%v err=%v", record, found, err)
	}
	if got := record.Checks[0].Handles; len(got) != 1 || got[0] != "@img1" {
		t.Fatalf("handles = %v", got)
	}
	if record.Conclusion != nil {
		t.Fatalf("naming the picture cleared the conclusion: %+v", record.Conclusion)
	}
}
