package chat

import (
	"encoding/json"
	"testing"
)

// #665 third review: the turn's generation count is rebuilt from the units
// persisted on the checkpoint (a plan counts its steps, a revision with no
// Task counts too), and only a task-bound call without a record falls back to
// one; the call being run is never part of its own prior count.
func TestPriorGenerationUnitsRebuildsFromTheCheckpoint665(t *testing.T) {
	var checkpoint TurnCheckpoint
	checkpoint.recordGenerationUnits("plan", 5)
	checkpoint.recordGenerationUnits("revise", 1)
	checkpoint.recordGenerationUnits("refused", 0)
	checkpoint.recordGenerationUnits("generate", 1)
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	var restored TurnCheckpoint
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	bound := map[string]string{"plan": "task-plan", "generate": "task-g", "legacy": "task-l"}
	if got := restored.priorGenerationUnits("next", bound); got != 8 {
		t.Fatalf("prior = %d, want 5+1+1 recorded and 1 legacy task-bound", got)
	}
	if got := restored.priorGenerationUnits("plan", bound, map[string]string{"legacy": "task-l"}); got != 3 {
		t.Fatalf("prior of the plan itself = %d, want 3", got)
	}
	var legacy TurnCheckpoint
	if got := legacy.priorGenerationUnits("next", map[string]string{"a": "t1", "b": "t2"}); got != 2 {
		t.Fatalf("legacy prior = %d, want one per task-bound call", got)
	}
}
