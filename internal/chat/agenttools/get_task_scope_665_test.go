package agenttools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

// #665: get_task hands the reader the whole server-held command (owner,
// session, project) and passes its scope refusal to the model in the Schema's
// words, without binding the call to a Task.
func TestGetTaskRelaysTheSessionScopeRefusal665(t *testing.T) {
	var seen chat.ToolCommand
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: &fakeGenerate{},
		ReadTask: func(_ context.Context, command chat.ToolCommand, taskID string) (json.RawMessage, error) {
			seen = command
			return nil, &chat.ReadScopeRefusal{Message: "读不到 " + taskID}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{Name: ToolGetTask, Arguments: json.RawMessage(`{"task_id":"task_sibling"}`)}}
	command.Options.ProjectID = "prj-1"
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil || result.Error != "读不到 task_sibling" || result.TaskID != "" || !result.NoSubmission {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	if seen.SessionID != "s1" || seen.Options.ProjectID != "prj-1" {
		t.Fatalf("reader got %+v", seen)
	}
}
