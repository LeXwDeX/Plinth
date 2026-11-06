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

func TestOwnedSessionTaskAcceptsOnlyTheDirectParentTask(t *testing.T) {
	tasks := &parentTaskGateway{task: generation.Task{ID: "task", OwnerID: "owner", SessionID: "parent"}}
	resolver := &Resolver{tasks: tasks, parentSession: func(_ context.Context, _ generation.OwnerID, sessionID string) (string, error) {
		if sessionID == "branch" {
			return "parent", nil
		}
		return "", nil
	}}
	if _, err := resolver.ownedSessionTask(context.Background(), "owner", "branch", "task", false); err != nil {
		t.Fatalf("direct parent task: %v", err)
	}
	if _, err := resolver.ownedSessionTask(context.Background(), "owner", "other", "task", false); err == nil {
		t.Fatal("a non-child session must reject the parent's task")
	}
}

func TestOwnedSessionTaskRejectsTheParentTaskWithoutAParentReader(t *testing.T) {
	tasks := &parentTaskGateway{task: generation.Task{ID: "task", OwnerID: "owner", SessionID: "parent"}}
	resolver := &Resolver{tasks: tasks}
	if _, err := resolver.ownedSessionTask(context.Background(), "owner", "branch", "task", false); err == nil {
		t.Fatal("a branch without a parent reader must not reuse the task")
	}
}
