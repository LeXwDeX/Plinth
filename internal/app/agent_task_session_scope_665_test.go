package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// scopeTasks holds several Tasks of one owner; a missing one reads as not
// found, like the generation module.
type scopeTasks map[generation.TaskID]generation.Task

func (tasks scopeTasks) Get(_ context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	task, found := tasks[id]
	if !found || task.OwnerID != owner {
		return generation.Task{}, &generation.Error{Code: generation.ErrNotFound, Operation: "get"}
	}
	return task, nil
}

// #665: get_task reads a Task of the session, of an ancestor of its branch or
// registered in its project; a sibling session's Task, another project's and
// one that does not exist get the same Schema refusal.
func TestGetTaskReadsOnlyTheSessionScope665(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	tasks := scopeTasks{}
	for id, session := range map[generation.TaskID]string{"task-own": "branch", "task-parent": "parent",
		"task-project": "other", "task-sibling": "sibling", "task-other-project": "elsewhere"} {
		tasks[id] = generation.Task{ID: id, OwnerID: "owner-a", SessionID: session, State: generation.TaskSucceeded}
	}
	scope := &chat.AgentReadScope{
		TaskInSession: func(_ context.Context, _ generation.OwnerID, session string, id generation.TaskID) (bool, error) {
			return tasks[id].SessionID == session || session == "branch" && id == "task-parent", nil
		},
		ProjectReference: func(_ context.Context, _ string, projectID string, reference project.Reference) (bool, error) {
			return projectID == "prj-1" && reference.TaskID == "task-project" ||
				projectID == "prj-2" && reference.TaskID == "task-other-project", nil
		},
		Refused: application.Chat.Presentation.Messages.AgentReadOutOfScope,
		Texts:   texts,
	}
	if scope.Refused == "" {
		t.Fatal("the default catalog declares no agent_read_out_of_scope")
	}
	read := newSessionAgentTaskReader(tasks, scope, application.Chat, application.Content.Messages, texts)
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "branch"}
	command.Options.ProjectID = "prj-1"
	for _, id := range []string{"task-own", "task-parent", "task-project"} {
		raw, err := read(context.Background(), command, id)
		if err != nil || !strings.Contains(string(raw), id) {
			t.Fatalf("%s = %s err=%v", id, raw, err)
		}
	}
	for _, id := range []string{"task-sibling", "task-other-project", "task-missing"} {
		_, err := read(context.Background(), command, id)
		var refusal *chat.ReadScopeRefusal
		want := strings.ReplaceAll(scope.Refused, "{id}", id)
		if !errors.As(err, &refusal) || refusal.Message != want {
			t.Fatalf("%s: err = %v, want the Schema refusal", id, err)
		}
	}
	command.Options.Locale = "en"
	_, err = read(context.Background(), command, "task-sibling")
	if err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Fatalf("English refusal = %v", err)
	}
}

// #665: the default tool result bound leaves the largest single-capability
// list_offerings answer of the default catalog untouched.
func TestDefaultToolResultBoundFitsEveryCapabilityListing665(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	limit := application.Chat.Agent.MaxToolResultCharacters
	for _, capability := range application.Capabilities {
		raw := buildAgentOfferingsProjection(application.Capabilities, product, nil, string(capability.ID), "", true, "")
		if size := utf8.RuneCount(raw); size > limit*3/4 {
			t.Errorf("%s listing has %d characters, too close to the bound %d", capability.ID, size, limit)
		}
	}
}
