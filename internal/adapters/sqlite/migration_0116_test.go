package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #665: 0116 adds go_chat_turn_keys, the Idempotency-Key records of the sends
// that started a turn. A database from before it keeps its sessions and
// messages, and a keyed turn can start in an existing session right away.
func TestMigration0116AddsTurnKeysToAnExistingDatabase665(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "turn-keys.db")
	r, err := openTestRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	if _, err := r.CreateSession(ctx, chat.Session{ID: "session-old", OwnerID: "owner-a", OwnerName: "A",
		Title: "old", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// Put the database back where 0115 left it.
	for _, statement := range []string{
		`DELETE FROM go_schema_migrations WHERE version = 116`,
		`DROP TABLE go_chat_turn_keys`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := openTestRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	var head int
	if err := upgraded.db.QueryRow(`SELECT MAX(version) FROM go_schema_migrations`).Scan(&head); err != nil || head != MigrationHead() || head < 116 {
		t.Fatalf("head = %d err=%v", head, err)
	}
	if _, found, err := upgraded.Session(ctx, "owner-a", "session-old"); err != nil || !found {
		t.Fatalf("existing session after upgrade found=%v err=%v", found, err)
	}
	key := startKeyedRepositoryTurn(t, upgraded, "session-old", "assistant-1", "send-1", now)
	record, found, err := upgraded.TurnKey(ctx, "owner-a", "session-old", "send-1", now)
	if err != nil || !found || record != key {
		t.Fatalf("record = %+v found=%v err=%v; want %+v", record, found, err, key)
	}
}

func startKeyedRepositoryTurn(t *testing.T, r *Repository, session, assistant, key string, now time.Time) chat.TurnKey {
	t.Helper()
	record, err := tryKeyedRepositoryTurn(r, session, assistant, key, now)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func tryKeyedRepositoryTurn(r *Repository, session, assistant, key string, now time.Time) (chat.TurnKey, error) {
	record := chat.TurnKey{OwnerID: "owner-a", SessionID: session, Key: key, Fingerprint: "digest",
		UserMessageID: "user-" + assistant, AssistantMessageID: assistant, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	user := chat.Message{ID: record.UserMessageID, SessionID: session, Role: "user", Content: "hi", CreatedAt: now}
	reply := chat.Message{ID: assistant, SessionID: session, Role: "assistant", Content: "…", CreatedAt: now}
	_, _, err := r.StartTurn(context.Background(), "owner-a", user, reply,
		chat.Event{SessionID: session, Data: map[string]any{"type": "created"}, CreatedAt: now},
		chat.Stream{SessionID: session, AssistantMessageID: assistant, Status: "active"},
		chat.Turn{OwnerID: "owner-a", SessionID: session, AssistantMessageID: assistant, State: "done",
			CreatedAt: now, UpdatedAt: now, Key: &record})
	return record, err
}

// The write path refuses a live key with the record it holds and writes
// nothing; the record goes with its reply message and with its session.
func TestTurnKeyRecordIsTakenOnceAndCascades665(t *testing.T) {
	ctx := context.Background()
	r, err := openTestRepository(filepath.Join(t.TempDir(), "turn-keys.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	if _, err := r.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "A",
		Title: "a", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	first := startKeyedRepositoryTurn(t, r, "session-a", "assistant-1", "send-1", now)
	_, err = tryKeyedRepositoryTurn(r, "session-a", "assistant-2", "send-1", now.Add(time.Minute))
	var taken *chat.TurnKeyTakenError
	if !errors.As(err, &taken) || taken.Existing != first {
		t.Fatalf("second write err=%v, want TurnKeyTakenError with %+v", err, first)
	}
	messages, err := r.ListMessages(ctx, "owner-a", "session-a")
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages after a taken key = %d err=%v, want 2", len(messages), err)
	}

	// Deleting the reply removes its key.
	if _, err := r.db.Exec(`DELETE FROM go_chat_messages WHERE id = 'assistant-1'`); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := r.TurnKey(ctx, "owner-a", "session-a", "send-1", now); found {
		t.Fatal("key outlived its reply message")
	}
	startKeyedRepositoryTurn(t, r, "session-a", "assistant-3", "send-2", now)
	if deleted, err := r.DeleteSession(ctx, "owner-a", "session-a"); err != nil || !deleted {
		t.Fatalf("delete session deleted=%v err=%v", deleted, err)
	}
	var left int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM go_chat_turn_keys`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("keys left after the session was deleted = %d err=%v", left, err)
	}
}
