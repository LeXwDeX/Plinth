package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/llmsettings"
)

// A database at 0065 (before #322 Scope 2/5) reaches the current head when
// this binary opens it: the settings history starts empty and existing
// sessions follow the default chat model.
func TestMigration0066UpgradesExistingSessionsToFollowTheDefaultModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "llm.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at)
 VALUES ('legacy','owner-a','owner','old','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DELETE FROM go_schema_migrations WHERE version >= 66`,
		`DROP TABLE llm_settings_versions`,
		`DROP TABLE llm_connection_checks`,
		`ALTER TABLE go_chat_sessions DROP COLUMN chat_model`,
		`ALTER TABLE agent_generation_operations DROP COLUMN reference_count`,
		`ALTER TABLE agent_generation_operations DROP COLUMN reference_total`,
		`ALTER TABLE agent_generation_operations DROP COLUMN reference_source`,
		`DROP INDEX IF EXISTS generation_attempts_delivery_completion`,
		`ALTER TABLE generation_attempts DROP COLUMN delivery_completion_json`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	var head int
	if err := reopened.db.QueryRow(`SELECT MAX(version) FROM go_schema_migrations`).Scan(&head); err != nil || head < 66 {
		t.Fatalf("migration head = %d err=%v", head, err)
	}
	state, found, err := reopened.SessionContext(context.Background(), "owner-a", "legacy")
	if err != nil || !found || state.ChatModel != "" {
		t.Fatalf("legacy context = %+v found=%v err=%v", state, found, err)
	}
	if _, err := reopened.LatestLLMSettings(context.Background()); !errors.Is(err, llmsettings.ErrNotFound) {
		t.Fatalf("settings on an upgraded database: %v", err)
	}
}

func TestLLMSettingsVersionsAppendWithRevisionCheck(t *testing.T) {
	ctx := context.Background()
	r, err := Open(filepath.Join(t.TempDir(), "llm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	at := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	first := llmsettings.Version{Settings: llmsettings.Settings{DefaultModel: "LLM", Models: map[string]llmsettings.ModelSettings{
		"LLM": {Enabled: true, EffortLevels: []string{"low", "high"}, DefaultEffort: "high"},
	}}, CreatedAt: at, ActorID: "admin-1", Source: llmsettings.SourcePublish}
	stored, err := r.AppendLLMSettings(ctx, "0", first)
	if err != nil || stored.Revision != "1" {
		t.Fatalf("first append: %+v %v", stored, err)
	}
	if _, err := r.AppendLLMSettings(ctx, "0", first); !errors.Is(err, llmsettings.ErrConflict) {
		t.Fatalf("stale append: %v", err)
	}
	if _, err := r.AppendLLMSettings(ctx, "not-a-number", first); !errors.Is(err, llmsettings.ErrConflict) {
		t.Fatalf("malformed revision: %v", err)
	}
	second := first
	second.Source = llmsettings.SourceReset
	second.CreatedAt = at.Add(time.Minute)
	if stored, err := r.AppendLLMSettings(ctx, "1", second); err != nil || stored.Revision != "2" {
		t.Fatalf("second append: %+v %v", stored, err)
	}
	latest, err := r.LatestLLMSettings(ctx)
	if err != nil || latest.Revision != "2" || latest.Source != llmsettings.SourceReset || !latest.CreatedAt.Equal(second.CreatedAt) {
		t.Fatalf("latest = %+v %v", latest, err)
	}
	version, err := r.LLMSettingsVersion(ctx, "1")
	if err != nil || version.Settings.Models["LLM"].DefaultEffort != "high" || version.ActorID != "admin-1" {
		t.Fatalf("version 1 = %+v %v", version, err)
	}
	if _, err := r.LLMSettingsVersion(ctx, "9"); !errors.Is(err, llmsettings.ErrNotFound) {
		t.Fatalf("missing version: %v", err)
	}
	history, err := r.LLMSettingsHistory(ctx, 1)
	if err != nil || len(history) != 1 || history[0].Revision != "2" {
		t.Fatalf("history = %+v %v", history, err)
	}
}

func TestLLMConnectionChecksKeepTheNewestPerConnection(t *testing.T) {
	ctx := context.Background()
	r, err := Open(filepath.Join(t.TempDir(), "llm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	at := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	for _, check := range []llmsettings.Check{
		{ConnectionID: "LLM", Model: "deepseek", CheckedAt: at, Outcome: llmsettings.OutcomeUnauthorized, Vision: llmsettings.VisionUnknown, ActorID: "admin"},
		{ConnectionID: "LLM", Model: "deepseek", CheckedAt: at.Add(time.Minute), Reachable: true, Outcome: llmsettings.OutcomeOK, Vision: llmsettings.VisionSupported, ActorID: "admin"},
		{ConnectionID: "GLM", Model: "glm-5.3", CheckedAt: at, Reachable: true, Outcome: llmsettings.OutcomeOK, Vision: llmsettings.VisionUnsupported, ActorID: "admin"},
	} {
		if err := r.SaveLLMConnectionCheck(ctx, check); err != nil {
			t.Fatal(err)
		}
	}
	checks, err := r.LLMConnectionChecks(ctx)
	if err != nil || len(checks) != 2 {
		t.Fatalf("checks = %+v %v", checks, err)
	}
	for _, check := range checks {
		if check.ConnectionID == "LLM" && (!check.Reachable || check.Vision != llmsettings.VisionSupported || !check.CheckedAt.Equal(at.Add(time.Minute))) {
			t.Fatalf("LLM check = %+v", check)
		}
	}
}

func TestSessionChatModelIsOwnerScoped(t *testing.T) {
	ctx := context.Background()
	r, err := Open(filepath.Join(t.TempDir(), "llm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	createApprovalSession(t, r, "s1", "owner-a", chat.Session{})
	createApprovalSession(t, r, "s2", "owner-b", chat.Session{})
	before, _, _ := r.Session(ctx, "owner-a", "s1")
	if found, err := r.SetSessionChatModel(ctx, "owner-a", "s1", "GLM"); err != nil || !found {
		t.Fatalf("set model: %v %v", found, err)
	}
	if found, err := r.SetSessionChatModel(ctx, "owner-b", "s1", "LLM"); err != nil || found {
		t.Fatalf("foreign write: %v %v", found, err)
	}
	state, found, err := r.SessionContext(ctx, "owner-a", "s1")
	if err != nil || !found || state.ChatModel != "GLM" {
		t.Fatalf("state = %+v %v %v", state, found, err)
	}
	if other, _, _ := r.SessionContext(ctx, "owner-b", "s2"); other.ChatModel != "" {
		t.Fatalf("other session = %+v", other)
	}
	// The choice does not reorder the session list.
	if after, _, _ := r.Session(ctx, "owner-a", "s1"); !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("updated_at moved: %v -> %v", before.UpdatedAt, after.UpdatedAt)
	}
}
