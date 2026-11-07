package app

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestRecoverTaskResultAcceptsAnAncestorTaskInABranch(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	failed := generation.Task{ID: "task_old", OwnerID: "owner_a", SessionID: "session_1",
		State: generation.TaskFailed, Offering: generation.Offering{CreditsMetered: true},
		Failure: &generation.Failure{Class: generation.FailureIntegrity}}
	tasks := &fakeRefetcher{
		fakeAgentTaskReader: fakeAgentTaskReader{task: failed,
			scopeSessions: map[string]bool{"branch": true}},
		result: generation.RefetchResult{TaskID: "task_refetch"},
	}
	recover := newAgentTaskRecoverer(tasks, application.Chat, application.Content.Messages, texts)
	raw, err := recover(context.Background(), "owner_a", "branch", "task_old")
	if err != nil {
		t.Fatalf("ancestor task was not recoverable in a branch: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("recover returned no view")
	}
	if _, err := recover(context.Background(), "owner_a", "sibling", "task_old"); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("sibling session recovered the task: %v", err)
	}
	if _, err := recover(context.Background(), "owner_b", "branch", "task_old"); err == nil {
		t.Fatal("another owner recovered the task")
	}
}
