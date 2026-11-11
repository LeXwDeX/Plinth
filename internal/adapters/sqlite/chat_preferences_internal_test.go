package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func newPreferenceRow(kind, source, assetID string, explicit, active bool) chat.Preference {
	return chat.Preference{
		Kind: kind, Source: source, Direction: "direction " + assetID,
		ReasonPreset: "style", ReasonText: "reason " + assetID,
		TaskID: "task-1", AssetID: assetID, Handle: "@img-" + assetID,
		Alternatives: []string{"@img-alt"}, Explicit: explicit, Active: active,
	}
}

func TestChatPreferenceEditDeleteAndSessionDelete(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "session-a", "owner", chat.Session{Title: "a"})
	row := newPreferenceRow(chat.PreferenceKindRejected, chat.PreferenceSourceReject, "asset-1", true, true)
	row.ID = "preference-1"
	row.CreatedAt, row.UpdatedAt = time.Now(), time.Now()
	if _, found, err := r.PutPreference(ctx, "owner", "session-a", row); err != nil || !found {
		t.Fatalf("put = %v %v", found, err)
	}
	direction, preset, text := "edited direction", "other", "edited reason"
	active := true
	updated, found, err := r.UpdatePreference(ctx, "owner", "session-a", "preference-1", chat.PreferenceEdit{
		Direction: &direction, ReasonPreset: &preset, ReasonText: &text, Active: &active,
	})
	if err != nil || !found || updated.Direction != direction || updated.ReasonPreset != preset ||
		updated.ReasonText != text || !updated.UserEdited {
		t.Fatalf("update = %+v, %v, %v", updated, found, err)
	}
	if _, found, _ := r.UpdatePreference(ctx, "other", "session-a", "preference-1", chat.PreferenceEdit{Direction: &direction}); found {
		t.Fatal("a foreign owner edited a preference")
	}
	if deleted, err := r.DeletePreference(ctx, "other", "session-a", "preference-1"); err != nil || deleted {
		t.Fatalf("foreign delete = %v %v", deleted, err)
	}
	if deleted, err := r.DeletePreference(ctx, "owner", "session-a", "preference-1"); err != nil || !deleted {
		t.Fatalf("delete = %v %v", deleted, err)
	}
	if preferences, _, _ := r.ListPreferences(ctx, "owner", "session-a"); len(preferences) != 0 {
		t.Fatalf("deleted record is still listed: %+v", preferences)
	}
	if deleted, err := r.DeletePreference(ctx, "owner", "session-a", "preference-1"); err != nil || deleted {
		t.Fatalf("deleting an inactive record = %v %v", deleted, err)
	}
	// Deleting the session clears its preference rows, not another session's.
	other := newPreferenceRow(chat.PreferenceKindSelected, chat.PreferenceSourcePick, "asset-2", true, true)
	other.ID = "preference-2"
	other.CreatedAt, other.UpdatedAt = time.Now(), time.Now()
	createApprovalSession(t, r, "session-b", "owner", chat.Session{Title: "b"})
	if _, found, err := r.PutPreference(ctx, "owner", "session-b", other); err != nil || !found {
		t.Fatalf("seed other = %v %v", found, err)
	}
	if deleted, err := r.DeleteSession(ctx, "owner", "session-a"); err != nil || !deleted {
		t.Fatalf("delete session = %v %v", deleted, err)
	}
	var count int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM chat_session_preferences WHERE session_id='session-a'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("session delete left preferences: %d %v", count, err)
	}
	if preferences, _, _ := r.ListPreferences(ctx, "owner", "session-b"); len(preferences) != 1 {
		t.Fatalf("another session's preferences changed: %+v", preferences)
	}
}

func TestBranchCopiesActivePreferencesWithFreshIDs(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "parent", "owner", chat.Session{Title: "Original"})
	if _, err := r.db.Exec(`INSERT INTO go_chat_messages(id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at) VALUES ('m','parent','user','one','[]','[]','[]','','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	active := newPreferenceRow(chat.PreferenceKindRejected, chat.PreferenceSourceReject, "asset-1", true, true)
	active.ID = "preference-active"
	active.CreatedAt, active.UpdatedAt = now, now
	hidden := newPreferenceRow(chat.PreferenceKindSelected, chat.PreferenceSourcePick, "asset-2", true, false)
	hidden.ID = "preference-hidden"
	hidden.CreatedAt, hidden.UpdatedAt = now, now
	if _, _, err := r.PutPreference(ctx, "owner", "parent", active); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.PutPreference(ctx, "owner", "parent", hidden); err != nil {
		t.Fatal(err)
	}
	request := chat.BranchRequest{OwnerID: "owner", SessionID: "parent", MessageID: "m",
		NewSessionID: "branch", TitleTemplate: "{title} branch", At: now,
		ActiveStates: []string{"running", "queued", "waiting_task"},
		NewID:        func(kind string) string { return kind + "-branch" }}
	if _, err := r.BranchSession(ctx, request); err != nil {
		t.Fatal(err)
	}
	preferences, found, err := r.ListPreferences(ctx, "owner", "branch")
	if err != nil || !found || len(preferences) != 1 {
		t.Fatalf("branch preferences = %+v, %v, %v", preferences, found, err)
	}
	copied := preferences[0]
	if copied.ID != "preference-branch" || copied.Kind != chat.PreferenceKindRejected ||
		copied.AssetID != "asset-1" || !copied.CreatedAt.Equal(now) {
		t.Fatalf("copied preference = %+v", copied)
	}
	// The parent keeps its own record; deleting the copy leaves the parent alone.
	if _, err := r.DeletePreference(ctx, "owner", "branch", copied.ID); err != nil {
		t.Fatal(err)
	}
	if parent, _, _ := r.ListPreferences(ctx, "owner", "parent"); len(parent) != 1 {
		t.Fatalf("parent preference changed: %+v", parent)
	}
}
