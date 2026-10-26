package sqlite_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

// B10：agent 状态写穿透 SQLite——重启（重开数据库）后同意门禁与参数
// 仍在；会话删除级联清理 agent 状态。
func TestAgentStatePersistsAcrossRestartAndCascadeDeletes(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "art-ai.db")
	repository, err := sqlite.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	scope := agentparams.ScopeKey("owner-a", "session-1")
	store := agentparams.NewPersistentStore(repository, nil)
	store.Set(scope, map[string]json.RawMessage{
		"task_id": json.RawMessage(`"task-9"`),
	})
	store.SetUser(scope, map[string]json.RawMessage{
		"size": json.RawMessage(`"large"`),
	})
	consentScope := agentparams.ScopeKey("owner-a", "session-1")
	store.Set(consentScope, map[string]json.RawMessage{
		"consent_pending":  json.RawMessage(`{"reason":"credits"}`),
		"consent_decision": json.RawMessage(`"pending"`),
	})
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := sqlite.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := agentparams.NewPersistentStore(reopened, nil)
	state := restarted.Get(scope)
	if string(state["task_id"]) != `"task-9"` ||
		string(state["size"]) != `"large"` ||
		string(state["consent_decision"]) != `"pending"` {
		t.Fatalf("state after restart = %#v", state)
	}

	// 会话删除级联清理。
	if _, err := reopened.DeleteSession(ctx, "owner-a", "session-1"); err != nil {
		t.Fatal(err)
	}
	deleted := restarted.Get(consentScope)
	if len(deleted) != 0 {
		t.Fatalf("agent state survived session deletion: %#v", deleted)
	}
}
