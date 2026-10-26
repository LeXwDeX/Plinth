package sqlite_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

type acceptingVerifier struct{}

func (acceptingVerifier) VerifyAttachment(_ context.Context, _, _ string, ref chat.AttachmentRef) (chat.Attachment, error) {
	return chat.Attachment{Kind: ref.Kind, Role: chat.AttachmentRoleGenerated, AssetID: ref.AssetID, TaskID: ref.TaskID,
		CaptureID: ref.CaptureID, ImageSlot: ref.ImageSlot, URL: ref.URL}, nil
}

func TestChatAttachmentsAndSelectionSurviveReload(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "attachments.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	service, err := chat.NewService(chat.ServiceConfig{
		Repository: repository, DefaultTitle: "default", UserRole: "user", AssistantRole: "assistant",
		PendingContent: "pending", StreamActive: "active", StreamDone: "done", StreamError: "error",
		TurnQueued: "queued", TurnRunning: "running", TurnWaitingTask: "waiting_task",
		TurnDone: "done", TurnError: "error",
		PlaceholderHealing:   chat.PlaceholderHealingPolicy{StaleAfter: time.Minute, PromptArgument: "prompt", PromptPreviewCharacters: 8, InterruptedMessage: "interrupted", InterruptedPromptTemplate: "{prompt}"},
		EventFields:          chat.EventFieldPolicy{Type: "type", MessageID: "message_id"},
		EventTypes:           chat.EventTypePolicy{MessageCreated: "created"},
		StreamCleanupTTL:     time.Minute,
		SubscriberBufferSize: 1,
		Handles:              chat.NewHandlePolicy(product.Application().Chat.Context),
		Attachments:          acceptingVerifier{},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID: "owner-a", SessionID: session.ID, Content: "from the 3D view",
		ImageURLs:   []string{"/api/v1/assets/shot"},
		Attachments: []chat.AttachmentRef{{ImageSlot: 1, Kind: chat.AttachmentKindImage, CaptureID: "capture-1", TaskID: "model-task", AssetID: "shot"}},
		Selection:   []chat.AttachmentRef{{Kind: chat.AttachmentKindModel, TaskID: "model-task", AssetID: "model-asset", URL: "/thumb"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []chat.Attachment{
		{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, AssetID: "shot", TaskID: "model-task", CaptureID: "capture-1", ImageSlot: 1},
		{Handle: "@model1", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, AssetID: "model-asset", TaskID: "model-task", URL: "/thumb"},
	}
	if !reflect.DeepEqual(messages[0].Attachments, want) {
		t.Fatalf("user attachments = %+v\nwant %+v", messages[0].Attachments, want)
	}
	if len(messages[1].Attachments) != 0 {
		t.Fatalf("assistant placeholder attachments = %+v", messages[1].Attachments)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil || len(turn.Options.Selection) != 1 || turn.Options.Selection[0].Handle != "@model1" || turn.Options.Selection[0].TaskID != "model-task" {
		t.Fatalf("turn selection = %+v, %v", turn.Options.Selection, err)
	}
	// Updating the assistant message keeps and returns its own attachments.
	reply := messages[1]
	reply.Content = "done"
	reply.ImageURLs = []string{"/api/v1/tasks/t/assets/a"}
	reply.Attachments = []chat.Attachment{{Handle: "@img2", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "t", AssetID: "a", ImageSlot: 1}}
	if err := service.UpdateMessage(ctx, "owner-a", reply); err != nil {
		t.Fatal(err)
	}
	messages, err = service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil || !reflect.DeepEqual(messages[1].Attachments, reply.Attachments) {
		t.Fatalf("assistant attachments = %+v, %v", messages[1].Attachments, err)
	}
	// Another owner reads nothing of this session.
	if _, err := service.ListMessages(ctx, "owner-b", session.ID); err == nil {
		t.Fatal("another owner listed the session's messages")
	}
}

func TestMessagesWithoutAttachmentsStayEmpty(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "plain.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	service, err := chat.NewService(chat.ServiceConfig{
		Repository: repository, DefaultTitle: "default", UserRole: "user", AssistantRole: "assistant",
		PendingContent: "pending", StreamActive: "active", StreamDone: "done", StreamError: "error",
		TurnQueued: "queued", TurnRunning: "running", TurnWaitingTask: "waiting_task",
		TurnDone: "done", TurnError: "error",
		PlaceholderHealing:   chat.PlaceholderHealingPolicy{StaleAfter: time.Minute, PromptArgument: "prompt", PromptPreviewCharacters: 8, InterruptedMessage: "interrupted", InterruptedPromptTemplate: "{prompt}"},
		EventFields:          chat.EventFieldPolicy{Type: "type", MessageID: "message_id"},
		EventTypes:           chat.EventTypePolicy{MessageCreated: "created"},
		StreamCleanupTTL:     time.Minute,
		SubscriberBufferSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "hello", ImageURLs: []string{"/a.png"}}); err != nil {
		t.Fatal(err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil || len(messages[0].Attachments) != 0 {
		t.Fatalf("messages = %+v, %v", messages, err)
	}
}
