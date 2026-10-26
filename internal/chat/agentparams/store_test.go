package agentparams

import (
	"encoding/json"
	"testing"
)

func TestSetGetClear(t *testing.T) {
	store := NewStore()
	store.Set("s1", map[string]json.RawMessage{"size": json.RawMessage(`"1024x1024"`)})
	if got := string(store.Get("s1")["size"]); got != `"1024x1024"` {
		t.Fatalf("size = %s", got)
	}
	store.Set("s1", map[string]json.RawMessage{"quality": json.RawMessage(`"low"`)})
	if got := len(store.Get("s1")); got != 2 {
		t.Fatalf("merged len = %d", got)
	}
	store.Clear("s1")
	if store.Get("s1") != nil {
		t.Fatal("after clear, Get != nil")
	}
}

func TestScopeIsolation(t *testing.T) {
	store := NewStore()
	store.Set("a", map[string]json.RawMessage{"k": json.RawMessage(`1`)})
	store.Set("b", map[string]json.RawMessage{"k": json.RawMessage(`2`)})
	if string(store.Get("a")["k"]) != "1" || string(store.Get("b")["k"]) != "2" {
		t.Fatal("scopes not isolated")
	}
}

func TestGetReturnsCopy(t *testing.T) {
	store := NewStore()
	store.Set("s", map[string]json.RawMessage{"k": json.RawMessage(`1`)})
	got := store.Get("s")
	got["k"] = json.RawMessage(`99`)
	if string(store.Get("s")["k"]) != "1" {
		t.Fatal("Get returned aliased map")
	}
}

func TestUserHarnessOverridesAgentSuggestionUntilCleared(t *testing.T) {
	store := NewStore()
	scope := ScopeKey("owner", "session")
	store.Set(scope, map[string]json.RawMessage{"size": json.RawMessage(`"small"`)})
	store.SetUser(scope, map[string]json.RawMessage{"size": json.RawMessage(`"large"`)})
	store.Set(scope, map[string]json.RawMessage{"size": json.RawMessage(`"medium"`)})
	if got := string(store.Get(scope)["size"]); got != `"large"` {
		t.Fatalf("user override lost: %s", got)
	}
	store.SetUser(scope, map[string]json.RawMessage{"size": json.RawMessage(`null`)})
	if _, present := store.Get(scope)["size"]; present {
		t.Fatal("clearing a user override retained stale value")
	}
	store.Set(scope, map[string]json.RawMessage{"size": json.RawMessage(`"medium"`)})
	if got := string(store.Get(scope)["size"]); got != `"medium"` {
		t.Fatalf("agent suggestion after clear = %s", got)
	}
	store.Clear(scope)
	if len(store.Get(scope)) != 0 {
		t.Fatal("task switch did not clear scope")
	}
}
