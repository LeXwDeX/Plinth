package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedBulkChatSession gives a session the rows that make a deletion long: many
// messages, each with the turn that produced it (checkpoints are the largest
// rows of the database), and a long stream replay.
func seedBulkChatSession(t *testing.T, r *Repository, ownerID, sessionID string, messages, events int) {
	t.Helper()
	const stamp = "2026-10-22T00:00:00Z"
	if _, err := r.db.Exec(`INSERT INTO go_chat_sessions (id, owner_id, owner_name, title, created_at, updated_at)
		VALUES (?, ?, 'owner', 'title', ?, ?)`, sessionID, ownerID, stamp, stamp); err != nil {
		t.Fatalf("seed session %s: %v", sessionID, err)
	}
	checkpoint := `{"padding":"` + strings.Repeat("x", 2000) + `"}`
	tx, err := r.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for i := 0; i < messages; i++ {
		id := fmt.Sprintf("%s-message-%d", sessionID, i)
		if _, err := tx.Exec(`INSERT INTO go_chat_messages (id, session_id, role, content, image_urls, tool_calls, created_at)
			VALUES (?, ?, 'assistant', 'content', '[]', '[]', ?)`, id, sessionID, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO go_chat_turns (assistant_message_id, session_id, owner_id, options_json, state, created_at, updated_at, checkpoint_json)
			VALUES (?, ?, ?, '{}', 'done', ?, ?, ?)`, id, sessionID, ownerID, stamp, stamp, checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < events; i++ {
		if _, err := tx.Exec(`INSERT INTO go_chat_events (session_id, event_id, data_json, created_at)
			VALUES (?, ?, '{}', ?)`, sessionID, i, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO go_chat_streams (session_id, assistant_message_id, status, cleanup_at)
		VALUES (?, ?, 'done', ?)`, sessionID, fmt.Sprintf("%s-message-0", sessionID), stamp); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func countSessionRows(t *testing.T, r *Repository, sessionID string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for table, query := range map[string]string{
		"sessions": `SELECT COUNT(*) FROM go_chat_sessions WHERE id = ?`,
		"messages": `SELECT COUNT(*) FROM go_chat_messages WHERE session_id = ?`,
		"turns":    `SELECT COUNT(*) FROM go_chat_turns WHERE session_id = ?`,
		"events":   `SELECT COUNT(*) FROM go_chat_events WHERE session_id = ?`,
		"streams":  `SELECT COUNT(*) FROM go_chat_streams WHERE session_id = ?`,
	} {
		var count int
		if err := r.db.QueryRow(query, sessionID).Scan(&count); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		counts[table] = count
	}
	return counts
}

// The repository keeps one connection, so any open transaction makes every
// other read wait for it (the log of #384: skill-runs, consent, params and
// /readyz all returned only when a session delete ended, 4.6 to 7.6 seconds
// later). A deletion therefore removes the bulk rows (messages with their
// turns, and the stream replay) in short transactions of its own and frees the
// connection between them; only the small remainder goes in the final one.
func TestDeleteSessionFreesTheConnectionBetweenBatches(t *testing.T) {
	r, err := openTestRepository(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	seedBulkChatSession(t, r, "owner-a", "big", 120, 700)
	seedBulkChatSession(t, r, "owner-a", "other", 10, 50)

	r.deleteBatchRows = 25
	batches := 0
	r.afterDeleteBatch = func() {
		batches++
		// A read made now would queue behind a deletion that still held the
		// connection; a short deadline turns that wait into a failure.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		owned, err := r.OwnsSession(ctx, "owner-a", "other")
		if err != nil || !owned {
			t.Errorf("read between deletion batches = %v, %v; the connection was not free", owned, err)
		}
	}
	deleted, err := r.DeleteSession(context.Background(), "owner-a", "big")
	if err != nil || !deleted {
		t.Fatalf("DeleteSession = %v, %v", deleted, err)
	}
	// 120 messages and 700 events in batches of 25: five message batches and
	// twenty-eight event batches.
	if batches < 20 {
		t.Fatalf("deletion ran in %d batches, want it split into many short transactions", batches)
	}
	for table, count := range countSessionRows(t, r, "big") {
		if count != 0 {
			t.Errorf("%s rows of the deleted session = %d, want 0", table, count)
		}
	}
	other := countSessionRows(t, r, "other")
	if other["sessions"] != 1 || other["messages"] != 10 || other["turns"] != 10 || other["events"] != 50 || other["streams"] != 1 {
		t.Fatalf("another session's rows were touched: %v", other)
	}
}

// A session that is not the owner's is refused before anything is deleted.
func TestDeleteSessionOfAnotherOwnerDeletesNothing(t *testing.T) {
	r, err := openTestRepository(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	seedBulkChatSession(t, r, "owner-a", "mine", 30, 100)
	r.deleteBatchRows = 10
	deleted, err := r.DeleteSession(context.Background(), "owner-b", "mine")
	if err != nil || deleted {
		t.Fatalf("DeleteSession of another owner's session = %v, %v", deleted, err)
	}
	if rows := countSessionRows(t, r, "mine"); rows["messages"] != 30 || rows["events"] != 100 || rows["sessions"] != 1 {
		t.Fatalf("a refused deletion removed rows: %v", rows)
	}
}
