package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
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

func TestChatPreferencesPersistAndStayOwnerSessionScoped(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "preferences.db")
	repository, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	for _, session := range []chat.Session{
		{ID: "session-a", OwnerID: "owner-a", Title: "a", CreatedAt: now, UpdatedAt: now},
		{ID: "session-b", OwnerID: "owner-a", Title: "b", CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := repository.CreateSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	row := newPreferenceRow(chat.PreferenceKindRejected, chat.PreferenceSourceReject, "asset-1", true, true)
	row.ID = "preference-1"
	row.CreatedAt, row.UpdatedAt = now, now
	stored, found, err := repository.PutPreference(ctx, "owner-a", "session-a", row)
	if err != nil || !found || stored.ID != "preference-1" || stored.ReasonPreset != "style" ||
		len(stored.Alternatives) != 1 || stored.Alternatives[0] != "@img-alt" {
		t.Fatalf("put = %+v, %v, %v", stored, found, err)
	}
	// A foreign owner and a missing session cannot write or read.
	if _, found, _ := repository.PutPreference(ctx, "owner-b", "session-a", row); found {
		t.Fatal("a foreign owner wrote a preference")
	}
	if _, found, _ := repository.ListPreferences(ctx, "owner-b", "session-a"); found {
		t.Fatal("a foreign owner read preferences")
	}
	if _, found, _ := repository.ListPreferences(ctx, "owner-a", "missing"); found {
		t.Fatal("a missing session reported preferences")
	}
	if preferences, found, _ := repository.ListPreferences(ctx, "owner-a", "session-b"); !found || len(preferences) != 0 {
		t.Fatalf("preferences leaked to another session: %+v %v", preferences, found)
	}
	repository.Close()

	reopened, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	preferences, found, err := reopened.ListPreferences(ctx, "owner-a", "session-a")
	if err != nil || !found || len(preferences) != 1 || preferences[0].ReasonText != "reason asset-1" {
		t.Fatalf("after reopen = %+v, %v, %v", preferences, found, err)
	}
	// The latest explicit action replaces the same object's record.
	replacement := newPreferenceRow(chat.PreferenceKindSelected, chat.PreferenceSourcePick, "asset-1", true, true)
	replacement.ID = preferences[0].ID
	replacement.CreatedAt = preferences[0].CreatedAt
	replacement.UpdatedAt = now.Add(time.Minute)
	if _, found, err := reopened.PutPreference(ctx, "owner-a", "session-a", replacement); err != nil || !found {
		t.Fatalf("replace = %v %v", found, err)
	}
	preferences, _, _ = reopened.ListPreferences(ctx, "owner-a", "session-a")
	if len(preferences) != 1 || preferences[0].Kind != chat.PreferenceKindSelected {
		t.Fatalf("the same object was not replaced: %+v", preferences)
	}
	// A weak signal never replaces an explicit record of the same object.
	weak := newPreferenceRow(chat.PreferenceKindSignal, chat.PreferenceSourceRegenerate, "asset-1", false, true)
	weak.ID = "preference-weak"
	weak.CreatedAt, weak.UpdatedAt = now.Add(2*time.Minute), now.Add(2*time.Minute)
	if _, found, err := reopened.PutPreference(ctx, "owner-a", "session-a", weak); err != nil || !found {
		t.Fatalf("weak = %v %v", found, err)
	}
	preferences, _, _ = reopened.ListPreferences(ctx, "owner-a", "session-a")
	if len(preferences) != 2 {
		t.Fatalf("weak signal did not coexist with the explicit record: %+v", preferences)
	}
}
