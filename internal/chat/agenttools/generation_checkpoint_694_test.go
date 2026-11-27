package agenttools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

// #694: each approval mode stops a reply at its own generation checkpoint;
// a mode the Schema does not name keeps max_generation_calls_per_turn. The
// refusal carries the checkpoint, so the orchestrator ends the reply there.
func TestGenerationCheckpointFollowsTheApprovalMode(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want int
	}{
		{"auto", 5},
		{"budget", 3},
		{"manual", 2},
		{"", 2},
		{"unknown", 2},
	} {
		gen := &fakeGenerate{}
		executor, err := New(Config{
			Params:                    agentparams.NewStore(),
			Generate:                  gen,
			MaxGenerationCallsPerTurn: 2,
			GenerationCheckpoint:      map[string]int{"auto": 5, "budget": 3},
			ConsentLimitContent:       testConsentLimit,
		})
		if err != nil {
			t.Fatal(err)
		}
		var refused chat.ToolResult
		for index := 1; index <= tc.want+1; index++ {
			result, err := executor.Execute(context.Background(), chat.ToolCommand{
				OwnerID: testOwner, SessionID: "s1", AssistantMessageID: "msg_turn",
				ApprovalMode: tc.mode,
				Call: agents.ToolCall{ID: fmt.Sprintf("call-%d", index), Name: "generate_image",
					Arguments: json.RawMessage(`{"prompt":"a"}`)},
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if index <= tc.want && result.GenerationCheckpoint != 0 {
				t.Fatalf("%q: call %d stopped early at %d", tc.mode, index, result.GenerationCheckpoint)
			}
			refused = result
		}
		if gen.callCount != tc.want || refused.GenerationCheckpoint != tc.want ||
			refused.AgentContent != testConsentLimit || !refused.NoSubmission {
			t.Fatalf("%q: generations = %d, refusal = %+v; want checkpoint %d", tc.mode, gen.callCount, refused, tc.want)
		}
	}
}
