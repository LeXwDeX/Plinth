package agenttools

import (
	"fmt"
	"testing"
)

// #665: the per-session generation counts are bounded: past maxUsageScopes
// the scope whose last call is the oldest goes, the newest stay.
func TestGenerationUsageIsBounded(t *testing.T) {
	executor := &Executor{maxGeneration: 5, usage: map[string]generationUsage{}}
	for index := range maxUsageScopes + 10 {
		scope := fmt.Sprintf("session-%d", index)
		if executor.generationLimitReachedForCall(scope, "turn", "call", 0) {
			t.Fatalf("%s: limit reached on its first call", scope)
		}
	}
	if len(executor.usage) > maxUsageScopes {
		t.Fatalf("usage holds %d scopes, want at most %d", len(executor.usage), maxUsageScopes)
	}
	if _, kept := executor.usage[fmt.Sprintf("session-%d", maxUsageScopes+9)]; !kept {
		t.Fatal("the newest scope was evicted")
	}
	if _, kept := executor.usage["session-0"]; kept {
		t.Fatal("the oldest scope was kept")
	}
}
