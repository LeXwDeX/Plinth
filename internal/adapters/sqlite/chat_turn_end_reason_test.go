package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

var endReasonStrings = catalog.ChatTurnEndReasonsSchema{
	Completed:        "configured-end-completed",
	AwaitingApproval: "configured-end-awaiting-approval",
	Stopped:          "configured-end-stopped",
	Background:       "configured-end-background",
	Error:            "configured-end-error",
}

func endReasonService(t *testing.T, repository chat.Repository, reasons catalog.ChatTurnEndReasonsSchema) *chat.Service {
	t.Helper()
	service, err := chat.NewService(chat.ServiceConfig{
		Repository: repository, DefaultTitle: "default", UserRole: "user", AssistantRole: "assistant",
		PendingContent: "pending", StreamActive: "active", StreamDone: "done", StreamError: "error",
		TurnQueued: "queued", TurnRunning: "running", TurnWaitingTask: "waiting_task",
		TurnDone: "turn-done", TurnError: "turn-error", TurnEndReasons: reasons,
		PlaceholderHealing:   chat.PlaceholderHealingPolicy{StaleAfter: time.Minute, PromptArgument: "prompt", PromptPreviewCharacters: 8, InterruptedMessage: "interrupted", InterruptedPromptTemplate: "{prompt}"},
		EventFields:          chat.EventFieldPolicy{Type: "type", MessageID: "message_id"},
		EventTypes:           chat.EventTypePolicy{MessageCreated: "created"},
		StreamCleanupTTL:     time.Minute,
		SubscriberBufferSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// #328: the end reason is stored with the finished turn and read back by every
// turn read. A turn that runs again loses it, and a turn that was never
// finished (or ended before the column existed) reads as empty.
func TestSQLiteTurnEndReasonRoundTripsThroughEveryRead(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "end-reason.db")
	repository, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	service := endReasonService(t, repository, endReasonStrings)
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil || queued.State != "queued" || queued.EndReason != "" {
		t.Fatalf("new turn = %#v, %v", queued, err)
	}
	for _, test := range []struct {
		kind   chat.TurnEndReason
		state  string
		reason string
	}{
		{chat.TurnEndCompleted, "turn-done", endReasonStrings.Completed},
		{chat.TurnEndAwaitingApproval, "turn-done", endReasonStrings.AwaitingApproval},
		{chat.TurnEndStopped, "turn-done", endReasonStrings.Stopped},
		{chat.TurnEndBackground, "turn-done", endReasonStrings.Background},
		{chat.TurnEndError, "turn-error", endReasonStrings.Error},
	} {
		if err := service.MarkTurnRunning(ctx, assistant.ID); err != nil {
			t.Fatal(err)
		}
		running, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
		if err != nil || running.State != "running" || running.EndReason != "" {
			t.Fatalf("running again must clear the reason: %#v, %v", running, err)
		}
		if err := service.FinishTurnWithReason(ctx, assistant.ID, test.kind); err != nil {
			t.Fatal(err)
		}
		finished, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
		if err != nil || finished.State != test.state || finished.EndReason != test.reason {
			t.Fatalf("GetTurn after kind %d = state %q reason %q, %v; want %q %q",
				test.kind, finished.State, finished.EndReason, err, test.state, test.reason)
		}
		states := []string{test.state}
		listed, err := repository.ListTurns(ctx, states)
		if err != nil || len(listed) != 1 || listed[0].EndReason != test.reason {
			t.Fatalf("ListTurns = %#v, %v", listed, err)
		}
		inSession, err := repository.ListSessionTurns(ctx, "owner-a", session.ID, states)
		if err != nil || len(inSession) != 1 || inSession[0].EndReason != test.reason {
			t.Fatalf("ListSessionTurns = %#v, %v", inSession, err)
		}
		page, err := repository.ListTurnsPage(ctx, states, chat.TurnPageCursor{}, 10)
		if err != nil || len(page) != 1 || page[0].EndReason != test.reason {
			t.Fatalf("ListTurnsPage = %#v, %v", page, err)
		}
	}
	// The last end was the error one; it survives a restart.
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := reopened.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil || recovered.State != "turn-error" || recovered.EndReason != endReasonStrings.Error {
		t.Fatalf("after restart = %#v, %v", recovered, err)
	}
}

// A Schema without end reasons stores the empty string: the turn state is the
// only fact, exactly as before the column existed.
func TestSQLiteTurnEndReasonIsEmptyWithoutDeclaredReasons(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "no-reasons.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	service := endReasonService(t, repository, catalog.ChatTurnEndReasonsSchema{})
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.FinishTurnWithReason(ctx, assistant.ID, chat.TurnEndAwaitingApproval); err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil || turn.State != "turn-done" || turn.EndReason != "" {
		t.Fatalf("turn = %#v, %v", turn, err)
	}
}

// Migration 0094 adds a NOT NULL column whose default is the empty string, so
// every historic row reads as "unknown" with no backfill.
func TestSQLiteTurnEndReasonColumnDefaultsToUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "column.db")
	repository, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	var (
		notNull int
		dflt    string
		columnT string
	)
	err = openRawDB(t, path).QueryRow(
		`SELECT "notnull", dflt_value, type FROM pragma_table_info('go_chat_turns') WHERE name = 'end_reason'`,
	).Scan(&notNull, &dflt, &columnT)
	if err != nil || notNull != 1 || dflt != "''" || columnT != "TEXT" {
		t.Fatalf("end_reason column = notnull %d default %q type %q, %v", notNull, dflt, columnT, err)
	}
}
