package sqlite

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #522: a session remembers the pictures the upstream refused. The list is
// durable, scoped to the owner, empty for a session that never had one, merged
// in one read-modify-write, and a branch starts from its parent's.
func TestChatSessionVisionExclusions(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "parent", "owner", chat.Session{Title: "Original"})
	createApprovalSession(t, r, "other", "owner", chat.Session{Title: "Other"})

	keys, found, err := r.VisionExclusions(ctx, "owner", "parent")
	if err != nil || !found || len(keys) != 0 {
		t.Fatalf("a new session remembers nothing: %v %v %v", keys, found, err)
	}
	add := func(owner, session string, added ...string) bool {
		t.Helper()
		found, err := r.UpdateVisionExclusions(ctx, owner, session, func(stored []string) []string {
			return chat.AppendVisionExclusions(stored, added, 3)
		})
		if err != nil {
			t.Fatal(err)
		}
		return found
	}
	if !add("owner", "parent", "a", "b") || !add("owner", "parent", "c", "d") {
		t.Fatal("the owner's session must be found")
	}
	if keys, _, _ := r.VisionExclusions(ctx, "owner", "parent"); !slices.Equal(keys, []string{"b", "c", "d"}) {
		t.Fatalf("keys = %v, want the newest three in order", keys)
	}
	if add("stranger", "parent", "x") {
		t.Fatal("a foreign owner wrote the list")
	}
	if _, found, _ := r.VisionExclusions(ctx, "stranger", "parent"); found {
		t.Fatal("a foreign owner read the list")
	}
	if add("owner", "missing", "x") {
		t.Fatal("a missing session must report not found")
	}
	if keys, _, _ := r.VisionExclusions(ctx, "owner", "other"); len(keys) != 0 {
		t.Fatalf("the list leaked to another session: %v", keys)
	}
	// A branch starts from the parent's refused pictures: its messages carry
	// the same bytes.
	if _, err := r.db.Exec(`INSERT INTO go_chat_messages(id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at) VALUES ('m1','parent','assistant','one','[]','[]','[]','','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	n := 0
	if _, err := r.BranchSession(ctx, chat.BranchRequest{
		OwnerID: "owner", SessionID: "parent", MessageID: "m1", NewSessionID: "branch",
		TitleTemplate: "{title}", At: time.Now(),
		NewID: func(string) string { n++; return "copy" + string(rune('0'+n)) }, ActiveStates: []string{"running", "queued", "waiting_task"},
	}); err != nil {
		t.Fatal(err)
	}
	if keys, _, _ := r.VisionExclusions(ctx, "owner", "branch"); !slices.Equal(keys, []string{"b", "c", "d"}) {
		t.Fatalf("branch keys = %v, want the parent's", keys)
	}
}
