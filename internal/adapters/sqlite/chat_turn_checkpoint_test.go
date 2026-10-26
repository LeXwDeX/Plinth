package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestSQLiteTurnCheckpointCASAndWaitingPage(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "turn.db")
	repository, err := sqlite.Open(path)
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
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.MarkTurnRunning(ctx, assistant.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := chat.TurnCheckpoint{Configuration: &chat.AgentConfigurationSnapshot{Revision: "frozen-v1", SystemPrompt: "private prompt", Skills: []catalog.SkillSchema{{ID: "style", Prompt: "original style"}}, AgentTools: []catalog.ChatAgentToolSchema{{Name: "mcp_read", Description: "read only"}}}, WaitingTaskID: "task-a", WaitingCallID: "call-a", WaitDeadline: time.Now().Add(time.Hour)}
	version, err := service.SaveTurnCheckpoint(ctx, turn, service.TurnWaitingTaskState(), checkpoint, false)
	if err != nil || version != 1 {
		t.Fatalf("save checkpoint version=%d err=%v", version, err)
	}
	if _, err := service.SaveTurnCheckpoint(ctx, turn, service.TurnWaitingTaskState(), checkpoint, false); !errors.Is(err, chat.ErrTurnCheckpointConflict) {
		t.Fatalf("stale checkpoint CAS = %v", err)
	}
	page, err := service.ListWakeCandidates(ctx, chat.TurnPageCursor{}, 1)
	if err != nil || len(page) != 1 || page[0].Checkpoint.WaitingTaskID != "task-a" || page[0].CheckpointVersion != 1 {
		t.Fatalf("waiting page = %#v, err=%v", page, err)
	}
	if _, err := service.GetTurn(ctx, "owner-b", session.ID, assistant.ID); !errors.Is(err, chat.ErrMessageNotFound) {
		t.Fatalf("cross owner turn read = %v", err)
	}
	if _, active, err := service.RequestTurnStop(ctx, "owner-a", session.ID); err != nil || !active {
		t.Fatalf("stop = %v, %v", active, err)
	}
	waiting, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveTurnCheckpoint(ctx, waiting, service.TurnWaitingTaskState(), checkpoint, false); !errors.Is(err, chat.ErrTurnCheckpointConflict) {
		t.Fatalf("stop must win checkpoint CAS: %v", err)
	}
	if _, err := service.SaveTurnCheckpoint(ctx, waiting, service.TurnQueuedState(), checkpoint, true); err != nil {
		t.Fatalf("stop wake: %v", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := reopened.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	frozen := recovered.Checkpoint.Configuration
	if frozen == nil || frozen.Revision != "frozen-v1" || frozen.SystemPrompt != "private prompt" || len(frozen.Skills) != 1 || frozen.Skills[0].Prompt != "original style" || len(frozen.AgentTools) != 1 || frozen.AgentTools[0].Name != "mcp_read" {
		t.Fatalf("restart lost frozen Agent declarations: %#v", frozen)
	}
}
