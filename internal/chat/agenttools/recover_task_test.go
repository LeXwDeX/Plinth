package agenttools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

// recover_task_result (#295) takes owner and session from the server-held
// command, never from the arguments, and never submits a generation.
func TestRecoverTaskResultUsesTheCommandScope(t *testing.T) {
	generate := &fakeGenerate{}
	var got [3]string
	executor, err := New(Config{
		Params:   agentparams.NewStore(),
		Generate: generate,
		RecoverTask: func(_ context.Context, ownerID, sessionID, taskID string) (json.RawMessage, error) {
			got = [3]string{ownerID, sessionID, taskID}
			return json.RawMessage(`{"started":true,"recovery_task_id":"task_2"}`), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: "owner", SessionID: "session",
		Call: agents.ToolCall{Name: ToolRecoverTaskResult, Arguments: json.RawMessage(`{"task_id":"task_1","owner_id":"other","session_id":"other"}`)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != [3]string{"owner", "session", "task_1"} {
		t.Fatalf("recover scope = %v", got)
	}
	if result.AgentContent != `{"started":true,"recovery_task_id":"task_2"}` || !result.NoSubmission || result.TaskID != "" {
		t.Fatalf("result = %+v", result)
	}
	invalid, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: "owner", SessionID: "session",
		Call: agents.ToolCall{Name: ToolRecoverTaskResult, Arguments: json.RawMessage(`{}`)},
	}, nil)
	if err != nil || invalid.Error == "" {
		t.Fatalf("missing task_id = %+v, %v", invalid, err)
	}
}
