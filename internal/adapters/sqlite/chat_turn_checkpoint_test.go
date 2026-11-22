package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestSQLiteTurnCheckpointCASAndWaitingPage(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "turn.db")
	repository, err := sqlitetest.Open(path)
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
	reopened, err := sqlitetest.Open(path)
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

// #382: a turn that reached a terminal state keeps its checkpoint's ids and
// increments but no longer the request with its pictures; a turn that is still
// queued, running or waiting keeps the request, because it resumes from it.
func TestSQLiteFinishTurnDropsTheRequestFromTheCheckpoint(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "turn-finish.db")
	repository, err := sqlitetest.Open(path)
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
	checkpoint := chat.TurnCheckpoint{
		Configuration: &chat.AgentConfigurationSnapshot{Revision: "frozen-v1", SystemPrompt: "private prompt"},
		Round:         2,
		Assistant:     chat.Message{ID: assistant.ID, Role: "assistant", Content: "reply"},
		Request: agents.Request{Model: "m", Messages: []agents.Message{
			{Role: "user", Parts: []agents.ContentPart{{Kind: "text", Text: "draw"}, {Kind: "image", URL: "data:image/png;base64,AAAA"}}},
		}},
		ImageURLs: []string{"data:image/png;base64,AAAA"},
	}
	if _, err := service.SaveTurnCheckpoint(ctx, turn, service.TurnRunningState(), checkpoint, false); err != nil {
		t.Fatal(err)
	}
	// A running turn resumes from the request, so it keeps it.
	running, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(running.Checkpoint.Request.Messages) != 1 || len(running.Checkpoint.ImageURLs) != 1 {
		t.Fatalf("a running turn lost its request: %#v", running.Checkpoint)
	}
	// A turn the person asked to stop and that was finalized keeps its state
	// and the ids of its round, and drops the request.
	if err := service.FinishTurnWithReason(ctx, assistant.ID, chat.TurnEndCompleted); err != nil {
		t.Fatal(err)
	}
	finished, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !service.TurnStateTerminal(finished.State) {
		t.Fatalf("finished turn state = %q", finished.State)
	}
	if len(finished.Checkpoint.Request.Messages) != 0 || len(finished.Checkpoint.ImageURLs) != 0 {
		t.Fatalf("a finished turn kept its request: %#v", finished.Checkpoint)
	}
	if finished.Checkpoint.Configuration == nil || finished.Checkpoint.Configuration.SystemPrompt != "private prompt" ||
		finished.Checkpoint.Round != 2 || finished.Checkpoint.Assistant.ID != assistant.ID {
		t.Fatalf("a finished turn lost its ids: %#v", finished.Checkpoint)
	}
	// The turn no longer runs, so it is no longer an active turn of the session.
	ids, err := repository.ListSessionTurnIDs(ctx, "owner-a", session.ID, []string{service.TurnQueuedState()})
	if err != nil || len(ids) != 0 {
		t.Fatalf("session turn ids after finish = %v, err=%v", ids, err)
	}
}
