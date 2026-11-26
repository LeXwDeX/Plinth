package chat_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// testReadScope reads branch refs from refs (session → Task IDs) and project
// registrations from registered (project → references).
func testReadScope(refs map[string][]generation.TaskID, registered map[string][]project.Reference) *chat.AgentReadScope {
	return &chat.AgentReadScope{
		TaskInSession: func(_ context.Context, owner generation.OwnerID, session string, task generation.TaskID) (bool, error) {
			for _, id := range refs[session] {
				if id == task && owner == "owner-a" {
					return true, nil
				}
			}
			return false, nil
		},
		ProjectReference: func(_ context.Context, owner, projectID string, wanted project.Reference) (bool, error) {
			for _, reference := range registered[projectID] {
				if owner == "owner-a" && (wanted.InputAssetID != "" && reference.InputAssetID == wanted.InputAssetID ||
					wanted.TaskID != "" && reference.TaskID == wanted.TaskID) {
					return true, nil
				}
			}
			return false, nil
		},
		Refused: "读不到 {id}",
	}
}

// #665: get_task and the pictures compare_model_reference names by ID read the
// session's own Tasks, a branch's ancestors, the session's list item
// references and its project's registrations; another session of the owner
// and another project are refused.
func TestAgentReadScopeAllowsTheSessionChainAndProjectOnly665(t *testing.T) {
	scope := testReadScope(map[string][]generation.TaskID{"branch": {"task-parent"}},
		map[string][]project.Reference{"prj-1": {{TaskID: "task-project", AssetID: "a"}, {InputAssetID: "upload-project"}},
			"prj-2": {{TaskID: "task-other-project", AssetID: "b"}, {InputAssetID: "upload-other-project"}}})
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "branch"}
	command.Options.ProjectID = "prj-1"
	command.Options.AssetListReferences = []project.Reference{{TaskID: "task-item", AssetID: "c"}}
	command.History = []chat.Message{{Attachments: []chat.Attachment{{AssetID: "upload-history"},
		{TaskID: "task-studio", AssetID: "studio-picture"}}}}
	ctx := context.Background()
	task := func(id, session string) generation.Task {
		return generation.Task{ID: generation.TaskID(id), OwnerID: "owner-a", SessionID: session}
	}
	for name, test := range map[string]struct {
		task generation.Task
		want bool
	}{
		"same session":         {task("task-own", "branch"), true},
		"ancestor branch":      {task("task-parent", "parent"), true},
		"same project":         {task("task-project", "other"), true},
		"list item reference":  {task("task-item", "proposal"), true},
		"sibling session":      {task("task-sibling", "sibling"), false},
		"other project":        {task("task-other-project", "other"), false},
		"no session":           {task("task-loose", ""), false},
		"attached studio task": {task("task-studio", ""), true},
		"other owner":          {generation.Task{ID: "task-own", OwnerID: "owner-b", SessionID: "branch"}, false},
	} {
		got, err := scope.TaskReadable(ctx, command, test.task)
		if err != nil || got != test.want {
			t.Errorf("task %s = %v err=%v want %v", name, got, err, test.want)
		}
	}
	asset := func(id, task, session string) generation.InputAsset {
		return generation.InputAsset{ID: generation.AssetID(id), OwnerID: "owner-a",
			TaskID: generation.TaskID(task), SessionID: session}
	}
	for name, test := range map[string]struct {
		asset generation.InputAsset
		want  bool
	}{
		"history upload":       {asset("upload-history", "", ""), true},
		"same session output":  {asset("out-own", "task-own", "branch"), true},
		"ancestor output":      {asset("out-parent", "task-parent", "parent"), true},
		"project upload":       {asset("upload-project", "", ""), true},
		"project task output":  {asset("out-project", "task-project", "other"), true},
		"sibling output":       {asset("out-sibling", "task-sibling", "sibling"), false},
		"loose upload":         {asset("upload-loose", "", ""), false},
		"other project upload": {asset("upload-other-project", "", ""), false},
		"other project output": {asset("out-other", "task-other-project", "other"), false},
	} {
		got, err := scope.InputAssetReadable(ctx, command, test.asset)
		if err != nil || got != test.want {
			t.Errorf("asset %s = %v err=%v want %v", name, got, err, test.want)
		}
	}
	if refusal := scope.Refusal(command, "task-x", "fallback"); refusal.Message != "读不到 task-x" {
		t.Fatalf("refusal = %q", refusal.Message)
	}
	var none *chat.AgentReadScope
	if refusal := none.Refusal(command, "task-x", "fallback"); refusal.Message != "fallback" {
		t.Fatalf("nil scope refusal = %q", refusal.Message)
	}
}
