package references

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

type parentTaskGateway struct{ task generation.Task }

func (gateway *parentTaskGateway) Get(context.Context, generation.OwnerID, generation.TaskID) (generation.Task, error) {
	return gateway.task, nil
}

func (gateway *parentTaskGateway) OpenAsset(context.Context, generation.OwnerID, generation.TaskID, generation.AssetID) (generation.AssetContent, error) {
	return generation.AssetContent{}, nil
}

// TestOwnedSessionTaskUsesTheBranchScope: a branch may read an ancestor Task
// (parent or grandparent) through its refs, and a sibling session sharing the
// same ancestor must not. This is the same helper the generation kernel uses,
// so the attachment verifier cannot drift from source_task admission.
func TestOwnedSessionTaskUsesTheBranchScope(t *testing.T) {
	tasks := &parentTaskGateway{task: generation.Task{ID: "task", OwnerID: "owner", SessionID: "grandparent"}}
	scope := func(_ context.Context, owner generation.OwnerID, sessionID string, taskID generation.TaskID) (bool, error) {
		return owner == "owner" && taskID == "task" && (sessionID == "parent" || sessionID == "branch"), nil
	}
	resolver := &Resolver{tasks: tasks, taskInSession: scope}
	for _, session := range []string{"parent", "branch"} {
		if _, err := resolver.ownedSessionTask(context.Background(), "owner", session, "task", false); err != nil {
			t.Fatalf("%s may read its ancestor task: %v", session, err)
		}
	}
	if _, err := resolver.ownedSessionTask(context.Background(), "owner", "sibling", "task", false); err == nil {
		t.Fatal("a sibling session must reject the ancestor task")
	}
	if _, err := resolver.ownedSessionTask(context.Background(), "other", "branch", "task", false); err == nil {
		t.Fatal("another owner must reject the task")
	}
}

func TestOwnedSessionTaskRejectsTheParentTaskWithoutAScope(t *testing.T) {
	tasks := &parentTaskGateway{task: generation.Task{ID: "task", OwnerID: "owner", SessionID: "parent"}}
	resolver := &Resolver{tasks: tasks}
	if _, err := resolver.ownedSessionTask(context.Background(), "owner", "branch", "task", false); err == nil {
		t.Fatal("a branch without a scope must not reuse the task")
	}
}
