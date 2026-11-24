package agentparams

import (
	"encoding/json"
	"testing"
)

// #511: nothing writes the bound task id any more, but rows an older server stored stay private: they never reach a
// parameter projection or a generation request.
func TestLegacyBoundTaskIDStaysPrivate(t *testing.T) {
	store := NewStore()
	store.parameterOnly = true
	store.scopes["s1"] = map[string]json.RawMessage{
		"task_id": json.RawMessage(`"task-9"`),
		"size":    json.RawMessage(`"large"`),
	}
	got := store.Get("s1")
	if _, leaked := got["task_id"]; leaked {
		t.Fatalf("legacy task id leaked into parameters: %v", got)
	}
	if string(got["size"]) != `"large"` {
		t.Fatalf("size = %s", got["size"])
	}
	if !isPrivateAgentStateKey(LegacyAgentTaskIDKey) {
		t.Fatal("legacy task id key is not private")
	}
}
