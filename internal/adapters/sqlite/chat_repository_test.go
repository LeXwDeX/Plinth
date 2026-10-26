package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestSQLiteChatRepositoryPersistsOwnerScopedSessionTurnAndEvents(
	t *testing.T,
) {
	databasePath := filepath.Join(t.TempDir(), "chat.db")
	repository, err := sqlite.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 26, 11, 0, 0, 0, time.UTC)
	sequence := 0
	service, err := chat.NewService(chat.ServiceConfig{
		Repository: repository,
		Clock:      func() time.Time { return now },
		NewID: func(kind string) string {
			sequence++
			return kind + "-sqlite-" + string(rune('0'+sequence))
		},
		DefaultTitle:    "configured default",
		UserRole:        "configured-user",
		AssistantRole:   "configured-assistant",
		PendingContent:  "configured-pending",
		StreamActive:    "configured-active",
		StreamDone:      "configured-done",
		StreamError:     "configured-error",
		TurnQueued:      "configured-queued",
		TurnRunning:     "configured-running",
		TurnWaitingTask: "configured-waiting-task",
		TurnDone:        "configured-turn-done",
		TurnError:       "configured-turn-error",
		PlaceholderHealing: chat.PlaceholderHealingPolicy{
			StaleAfter:                time.Minute,
			PromptArgument:            "configured-prompt",
			PromptPreviewCharacters:   5,
			InterruptedMessage:        "configured interrupted",
			InterruptedPromptTemplate: "configured interrupted {prompt}",
		},
		StreamCleanupTTL:     time.Minute,
		SubscriberBufferSize: 8,
		EventFields: chat.EventFieldPolicy{
			Type:      "configured-type",
			MessageID: "configured-message-id",
		},
		EventTypes: chat.EventTypePolicy{
			MessageCreated: "configured-created",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := service.CreateSession(
		ctx,
		"owner-a",
		"Owner A",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID:   "owner-a",
		SessionID: session.ID,
		Content:   "hello",
		ImageURLs: []string{"/configured/image"},
		Options: chat.TurnOptions{
			AIModel:               "configured-model",
			AspectRatio:           "16:9",
			ReferenceImageIndices: []int{2, 4},
		},
	}); err != nil {
		t.Fatal(err)
	}
	recoverable, err := service.RecoverTurns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(recoverable) != 1 ||
		recoverable[0].OwnerID != "owner-a" ||
		recoverable[0].SessionID != session.ID ||
		recoverable[0].Options.AIModel != "configured-model" ||
		recoverable[0].Options.AspectRatio != "16:9" ||
		len(recoverable[0].Options.ReferenceImageIndices) != 2 {
		t.Fatalf("recoverable = %#v", recoverable)
	}
	if _, active, err := service.RequestTurnStop(ctx, "owner-b", session.ID); err != nil || active {
		t.Fatalf("foreign owner stop = %v, %v", active, err)
	}
	if _, active, err := service.RequestTurnStop(ctx, "owner-a", "another-session"); err != nil || active {
		t.Fatalf("foreign session stop = %v, %v", active, err)
	}
	stopping, active, err := service.RequestTurnStop(ctx, "owner-a", session.ID)
	if err != nil || !active || stopping.AssistantMessageID != recoverable[0].AssistantMessageID || !stopping.StopRequested {
		t.Fatalf("durable stop = %#v, active=%v, err=%v", stopping, active, err)
	}
	requested, err := service.TurnStopRequested(ctx, "owner-a", session.ID, stopping.AssistantMessageID)
	if err != nil || !requested {
		t.Fatalf("read stop intent = %v, %v", requested, err)
	}
	if requested, err := service.TurnStopRequested(ctx, "owner-b", session.ID, stopping.AssistantMessageID); !errors.Is(err, chat.ErrMessageNotFound) || requested {
		t.Fatalf("foreign stop read = %v, %v", requested, err)
	}
	if err := service.MarkTurnRunning(
		ctx,
		recoverable[0].AssistantMessageID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListMessages(
		ctx,
		"owner-b",
		session.ID,
	); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("cross-owner list error = %v", err)
	}
	messages, err := service.ListMessages(ctx, "owner-a", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 ||
		messages[0].ImageURLs[0] != "/configured/image" ||
		messages[1].Content != "configured-pending" {
		t.Fatalf("messages = %#v", messages)
	}
	messages[1].ToolCalls = []chat.ToolCall{{ID: "call-1", Name: "configured-generator",
		IdempotencyKey: "chat:" + session.ID + ":call-1"}}
	if err := service.UpdateMessage(ctx, "owner-a", messages[1]); err != nil {
		t.Fatal(err)
	}
	events, err := service.ListEvents(ctx, "owner-a", session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 ||
		events[0].ID != 0 ||
		events[0].Data["configured-message-id"] != messages[1].ID {
		t.Fatalf("events = %#v", events)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := sqlite.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopenedService, err := chat.NewService(chat.ServiceConfig{
		Repository:      reopened,
		DefaultTitle:    "configured default",
		UserRole:        "configured-user",
		AssistantRole:   "configured-assistant",
		PendingContent:  "configured-pending",
		StreamActive:    "configured-active",
		StreamDone:      "configured-done",
		StreamError:     "configured-error",
		TurnQueued:      "configured-queued",
		TurnRunning:     "configured-running",
		TurnWaitingTask: "configured-waiting-task",
		TurnDone:        "configured-turn-done",
		TurnError:       "configured-turn-error",
		PlaceholderHealing: chat.PlaceholderHealingPolicy{
			StaleAfter:                time.Minute,
			PromptArgument:            "configured-prompt",
			PromptPreviewCharacters:   5,
			InterruptedMessage:        "configured interrupted",
			InterruptedPromptTemplate: "configured interrupted {prompt}",
		},
		StreamCleanupTTL:     time.Minute,
		SubscriberBufferSize: 8,
		EventFields: chat.EventFieldPolicy{
			Type:      "configured-type",
			MessageID: "configured-message-id",
		},
		EventTypes: chat.EventTypePolicy{
			MessageCreated: "configured-created",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reopenedService.ListMessages(
		ctx,
		"owner-a",
		session.ID,
	)
	if err != nil || len(persisted) != 2 {
		t.Fatalf("persisted=%#v error=%v", persisted, err)
	}
	if len(persisted[1].ToolCalls) != 1 || persisted[1].ToolCalls[0].IdempotencyKey != "chat:"+session.ID+":call-1" {
		t.Fatalf("frozen call key after restart = %#v", persisted[1].ToolCalls)
	}
	recoveredAfterRestart, err := reopenedService.RecoverTurns(ctx)
	if err != nil || len(recoveredAfterRestart) != 1 ||
		recoveredAfterRestart[0].State != "configured-running" || !recoveredAfterRestart[0].StopRequested {
		t.Fatalf(
			"recovered after restart=%#v error=%v",
			recoveredAfterRestart,
			err,
		)
	}
	if err := reopenedService.FinishTurn(
		ctx,
		recoveredAfterRestart[0].AssistantMessageID,
		false,
	); err != nil {
		t.Fatal(err)
	}
	finished, err := reopenedService.RecoverTurns(ctx)
	if err != nil || len(finished) != 0 {
		t.Fatalf("finished=%#v error=%v", finished, err)
	}

	sessions, err := reopenedService.ListSessions(ctx, "owner-a")
	if err != nil || len(sessions) != 1 || sessions[0].Messages != 2 {
		t.Fatalf("sessions=%#v error=%v", sessions, err)
	}
	if crossOwner, err := reopenedService.ListSessions(
		ctx,
		"owner-b",
	); err != nil || len(crossOwner) != 0 {
		t.Fatalf("cross-owner sessions=%#v error=%v", crossOwner, err)
	}
	if _, err := reopenedService.RenameSession(
		ctx,
		"owner-b",
		session.ID,
		"Forbidden",
	); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("cross-owner rename error=%v", err)
	}
	renamed, err := reopenedService.RenameSession(
		ctx,
		"owner-a",
		session.ID,
		"Persisted title",
	)
	if err != nil || renamed.Title != "Persisted title" {
		t.Fatalf("renamed=%#v error=%v", renamed, err)
	}

	updatedMessage := persisted[0]
	updatedMessage.Content = "updated persisted content"
	updatedMessage.ImageURLs = []string{"/configured/updated-image"}
	updatedMessage.ToolCalls = []chat.ToolCall{{
		ID:        "configured-tool-call",
		Name:      "configured-tool",
		Arguments: []byte(`{"configured":true}`),
		TaskID:    "configured-task",
	}}
	if err := reopenedService.UpdateMessage(
		ctx,
		"owner-b",
		updatedMessage,
	); !errors.Is(err, chat.ErrMessageNotFound) {
		t.Fatalf("cross-owner update error=%v", err)
	}
	if err := reopenedService.UpdateMessage(
		ctx,
		"owner-a",
		updatedMessage,
	); err != nil {
		t.Fatal(err)
	}
	updatedMessages, err := reopenedService.ListMessages(
		ctx,
		"owner-a",
		session.ID,
	)
	if err != nil ||
		updatedMessages[0].Content != updatedMessage.Content ||
		len(updatedMessages[0].ToolCalls) != 1 ||
		updatedMessages[0].ToolCalls[0].TaskID != "configured-task" {
		t.Fatalf("updated messages=%#v error=%v", updatedMessages, err)
	}

	appendedEvent, err := reopened.AppendEvent(ctx, chat.Event{
		SessionID: session.ID,
		Data: map[string]any{
			"configured-type": "configured-appended",
		},
		CreatedAt: now.Add(time.Second),
	})
	if err != nil || appendedEvent.ID <= events[0].ID {
		t.Fatalf("appended event=%#v error=%v", appendedEvent, err)
	}
	paginatedEvents, owned, err := reopened.ListEvents(
		ctx,
		"owner-a",
		session.ID,
		appendedEvent.ID,
	)
	if err != nil || !owned || len(paginatedEvents) != 1 ||
		paginatedEvents[0].Data["configured-type"] != "configured-appended" {
		t.Fatalf(
			"paginated events=%#v owned=%v error=%v",
			paginatedEvents,
			owned,
			err,
		)
	}

	stream, replay, owned, found, err := reopened.ReadStream(
		ctx,
		"owner-a",
		session.ID,
		0,
		now,
	)
	if err != nil || !owned || !found ||
		stream.Status != "configured-active" ||
		len(replay) != 2 {
		t.Fatalf(
			"stream=%#v replay=%#v owned=%v found=%v error=%v",
			stream,
			replay,
			owned,
			found,
			err,
		)
	}
	cleanupAt := now.Add(time.Minute)
	if err := reopened.FinishStream(
		ctx,
		session.ID,
		recoverable[0].AssistantMessageID,
		"configured-done",
		cleanupAt,
	); err != nil {
		t.Fatal(err)
	}
	stream, _, owned, found, err = reopened.ReadStream(
		ctx,
		"owner-a",
		session.ID,
		0,
		cleanupAt.Add(-time.Second),
	)
	if err != nil || !owned || !found || stream.Status != "configured-done" {
		t.Fatalf(
			"finished stream=%#v owned=%v found=%v error=%v",
			stream,
			owned,
			found,
			err,
		)
	}
	if _, _, owned, found, err := reopened.ReadStream(
		ctx,
		"owner-a",
		session.ID,
		0,
		cleanupAt,
	); err != nil || !owned || found {
		t.Fatalf("expired stream owned=%v found=%v error=%v", owned, found, err)
	}

	if err := reopenedService.DeleteMessage(
		ctx,
		"owner-b",
		session.ID,
		updatedMessage.ID,
	); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("cross-owner message delete error=%v", err)
	}
	if err := reopenedService.DeleteMessage(
		ctx,
		"owner-a",
		session.ID,
		updatedMessage.ID,
	); err != nil {
		t.Fatal(err)
	}
	remainingMessages, err := reopenedService.ListMessages(
		ctx,
		"owner-a",
		session.ID,
	)
	if err != nil || len(remainingMessages) != 1 {
		t.Fatalf("remaining messages=%#v error=%v", remainingMessages, err)
	}
	if err := reopenedService.DeleteSession(
		ctx,
		"owner-b",
		session.ID,
	); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("cross-owner session delete error=%v", err)
	}
	if err := reopenedService.DeleteSession(
		ctx,
		"owner-a",
		session.ID,
	); err != nil {
		t.Fatal(err)
	}
	if owns, err := reopened.OwnsSession(
		ctx,
		"owner-a",
		session.ID,
	); err != nil || owns {
		t.Fatalf("deleted session owns=%v error=%v", owns, err)
	}
}

func TestSQLiteChatMessagePagesWalkBackwardsWithoutGapsOrRepeats(t *testing.T) {
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	for _, session := range []chat.Session{
		{ID: "mine", OwnerID: "owner-a", OwnerName: "A", Title: "Long", CreatedAt: now, UpdatedAt: now},
		{ID: "empty", OwnerID: "owner-a", OwnerName: "A", Title: "Empty", CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := repository.CreateSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	// Seven messages; m3..m5 share one timestamp, so rowid decides their order.
	stamps := []time.Duration{0, time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second}
	for index, offset := range stamps {
		message := chat.Message{
			ID: "m" + strconv.Itoa(index+1), SessionID: "mine", Role: "user",
			Content: "text", CreatedAt: now.Add(offset),
		}
		if inserted, err := repository.InsertMessageIfAbsent(ctx, "owner-a", message, now.Add(offset)); err != nil || !inserted {
			t.Fatalf("insert %s: %t %v", message.ID, inserted, err)
		}
	}
	all, err := repository.ListMessages(ctx, "owner-a", "mine")
	if err != nil {
		t.Fatal(err)
	}
	var walked []string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("paging did not end")
		}
		page, err := repository.ListMessagesPage(ctx, "owner-a", "mine", chat.MessageListQuery{Before: cursor, Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(page.Messages))
		for _, message := range page.Messages {
			ids = append(ids, message.ID)
		}
		// Each page is in conversation order and goes before what was read already.
		walked = append(ids, walked...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(walked) != len(all) {
		t.Fatalf("walked %v, want %d messages", walked, len(all))
	}
	for index, message := range all {
		if walked[index] != message.ID {
			t.Fatalf("walked %v differs from ListMessages order at %d", walked, index)
		}
	}
	empty, err := repository.ListMessagesPage(ctx, "owner-a", "empty", chat.MessageListQuery{Limit: 3})
	if err != nil || len(empty.Messages) != 0 || empty.NextCursor != "" {
		t.Fatalf("empty session page = %#v, %v", empty, err)
	}
	if _, err := repository.ListMessagesPage(ctx, "owner-b", "mine", chat.MessageListQuery{Limit: 3}); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign session error = %v", err)
	}
	for _, cursor := range []string{"not base64!", "bm8tc2VwYXJhdG9y", strings.Repeat("A", 2000)} {
		if _, err := repository.ListMessagesPage(ctx, "owner-a", "mine", chat.MessageListQuery{Before: cursor, Limit: 3}); !errors.Is(err, chat.ErrInvalidMessagePage) {
			t.Fatalf("cursor %.20q error = %v", cursor, err)
		}
	}
}
