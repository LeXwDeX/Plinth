package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// submitRuns is the run store of the completion tool: one item, one run and
// the last FinishItem call. The embedded nil interface keeps the rest of
// assetlist.RunStore out of the fixture.
type submitRuns struct {
	assetlist.RunStore
	item      assetlist.Item
	itemErr   error
	run       assetlist.Run
	runFound  bool
	runErr    error
	finish    assetlist.FinishItemInput
	finishErr error
}

func (runs *submitRuns) ItemBySession(_ context.Context, ownerID, sessionID string) (assetlist.Item, bool, error) {
	if runs.itemErr != nil {
		return assetlist.Item{}, false, runs.itemErr
	}
	if ownerID != runs.item.OwnerID || sessionID != runs.item.SessionID {
		return assetlist.Item{}, false, nil
	}
	return runs.item, true, nil
}

func (runs *submitRuns) GetRunByList(_ context.Context, ownerID, projectID, listID string) (assetlist.Run, error) {
	if runs.runErr != nil {
		return assetlist.Run{}, runs.runErr
	}
	if !runs.runFound || runs.run.ListID != listID || runs.run.OwnerID != ownerID ||
		runs.run.ProjectID != projectID {
		return assetlist.Run{}, assetlist.ErrNotFound
	}
	return runs.run, nil
}

func (runs *submitRuns) FinishItem(_ context.Context, input assetlist.FinishItemInput) (assetlist.Item, error) {
	runs.finish = input
	if runs.finishErr != nil {
		return assetlist.Item{}, runs.finishErr
	}
	item := runs.item
	item.State = input.State
	item.StateReason = input.StateReason
	item.Result = input.Result
	return item, nil
}

// RunSummaries keeps list reads undecorated in this fixture.
func (runs *submitRuns) RunSummaries(_ context.Context, _, _ string) (map[string]assetlist.RunSummary, error) {
	return nil, nil
}

func newAssetListSubmitService(t *testing.T, runs *submitRuns,
	tasks chat.AssetListTaskSource) *chat.Service {
	t.Helper()
	schema := defaultAssetListSchema(t)
	listService, err := assetlist.New(assetlist.Config{
		Repository:      newAssetListRepo(),
		Runs:            runs,
		Schema:          schema,
		ProjectMaxItems: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := newChatService(t, newMemoryChatRepository())
	service.SetAssetLists(chat.AssetListPolicy{
		Service: listService, Schema: schema, Tasks: tasks,
	})
	return service
}

func workingSubmitRuns() *submitRuns {
	return &submitRuns{
		item: assetlist.Item{
			ID: "aitem_1", ListID: "alist_1", OwnerID: "owner-a",
			SessionID: "session-item", Name: "Sword", State: assetlist.StateWorking,
		},
		run: assetlist.Run{
			ID: "arun_1", OwnerID: "owner-a", ProjectID: "project-a",
			ListID: "alist_1", Status: assetlist.RunRunning,
		},
		runFound: true,
	}
}

func itemSubmitCommand(arguments string) chat.ToolCommand {
	return chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-item", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "call-a", Name: "submit_asset_list_item",
			Arguments: []byte(arguments)},
		Options: chat.TurnOptions{AssetListItemID: "aitem_1", ProjectID: "project-a"},
	}
}

func succeededTask(id string, createdAt time.Time, assets ...generation.Asset) generation.Task {
	return generation.Task{
		ID: generation.TaskID(id), State: generation.TaskSucceeded,
		CreatedAt: createdAt, Assets: assets,
	}
}

// A done report needs a successful Task of the item's own session; the server
// snapshots it into result_json and moves the item to review.
func TestSubmitAssetListItemDoneStoresTheTaskSnapshot(t *testing.T) {
	runs := workingSubmitRuns()
	newer := succeededTask("task-2", time.Date(2026, 11, 18, 10, 5, 0, 0, time.UTC),
		generation.Asset{ID: "asset-1", Role: "model", Variant: "glb"})
	older := succeededTask("task-1", time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
		generation.Asset{ID: "asset-old", Role: "model"})
	failed := generation.Task{
		ID: "task-0", State: generation.TaskFailed,
		CreatedAt: time.Date(2026, 11, 18, 10, 8, 0, 0, time.UTC),
	}
	var sourceSession string
	service := newAssetListSubmitService(t, runs,
		func(_ context.Context, ownerID, sessionID string) ([]generation.Task, error) {
			if ownerID != "owner-a" {
				t.Fatalf("task source owner = %q", ownerID)
			}
			sourceSession = sessionID
			return []generation.Task{failed, older, newer}, nil
		})
	result, err := service.SubmitAssetListItem(context.Background(),
		itemSubmitCommand(`{"status":"done"}`))
	if err != nil || result.Error != "" || !result.NoSubmission {
		t.Fatalf("done result = %+v err=%v", result, err)
	}
	if sourceSession != "session-item" {
		t.Fatalf("task source session = %q; want the item's own session", sourceSession)
	}
	if runs.finish.ItemID != "aitem_1" || runs.finish.ListID != "alist_1" ||
		runs.finish.State != assetlist.StateReview {
		t.Fatalf("finish input = %+v", runs.finish)
	}
	var snapshot struct {
		TaskID string `json:"task_id"`
		Assets []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Role string `json:"role"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(runs.finish.Result, &snapshot); err != nil {
		t.Fatalf("result_json %q: %v", runs.finish.Result, err)
	}
	if snapshot.TaskID != "task-2" || len(snapshot.Assets) != 1 ||
		snapshot.Assets[0].ID != "asset-1" || snapshot.Assets[0].Role != "model" ||
		snapshot.Assets[0].Name != "model/glb" {
		t.Fatalf("result snapshot = %+v", snapshot)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatalf("agent content %q: %v", result.AgentContent, err)
	}
	if content["item_id"] != "aitem_1" || content["state"] != assetlist.StateReview ||
		content["status"] != "done" || content["task_id"] != "task-2" {
		t.Fatalf("done content = %v", content)
	}
}

// A done report without any successful Task is refused and changes nothing.
func TestSubmitAssetListItemDoneRequiresASuccessfulTask(t *testing.T) {
	runs := workingSubmitRuns()
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string) ([]generation.Task, error) {
			return []generation.Task{{ID: "task-1", State: generation.TaskFailed}}, nil
		})
	result, err := service.SubmitAssetListItem(context.Background(),
		itemSubmitCommand(`{"status":"done"}`))
	if err != nil || !result.NoSubmission || result.Error == "" ||
		!strings.Contains(result.Error, "没有成功的任务") {
		t.Fatalf("no successful task result = %+v err=%v", result, err)
	}
	if runs.finish.ItemID != "" {
		t.Fatalf("a refused report finished the item: %+v", runs.finish)
	}
}

// A failed report prefers the session's failed Task and stores the classified
// category, not the raw reason.
func TestSubmitAssetListItemFailedUsesTheFailureClassification(t *testing.T) {
	runs := workingSubmitRuns()
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string) ([]generation.Task, error) {
			return []generation.Task{{
				ID: "task-1", State: generation.TaskFailed,
				CreatedAt: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
				Failure: &generation.Failure{
					Class: generation.FailureRateLimited, Retryable: true,
				},
			}}, nil
		})
	result, err := service.SubmitAssetListItem(context.Background(),
		itemSubmitCommand(`{"status":"failed","reason":"the agent gave up"}`))
	if err != nil || result.Error != "" || !result.NoSubmission {
		t.Fatalf("failed result = %+v err=%v", result, err)
	}
	if runs.finish.State != assetlist.StateFailed ||
		runs.finish.StateReason != string(generation.CategoryUpstreamCapacity) {
		t.Fatalf("finish input = %+v", runs.finish)
	}
	if string(runs.finish.Result) != "" {
		t.Fatalf("failed report stored a result snapshot: %s", runs.finish.Result)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatalf("agent content %q: %v", result.AgentContent, err)
	}
	if content["state"] != assetlist.StateFailed || content["status"] != "failed" ||
		content["task_id"] != "task-1" {
		t.Fatalf("failed content = %v", content)
	}
}

// Without a failed Task the tool's own reason becomes state_reason.
func TestSubmitAssetListItemFailedUsesTheReasonWithoutATask(t *testing.T) {
	runs := workingSubmitRuns()
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string) ([]generation.Task, error) {
			return nil, nil
		})
	result, err := service.SubmitAssetListItem(context.Background(),
		itemSubmitCommand(`{"status":"failed","reason":"route unavailable"}`))
	if err != nil || result.Error != "" || !result.NoSubmission {
		t.Fatalf("failed result = %+v err=%v", result, err)
	}
	if runs.finish.State != assetlist.StateFailed ||
		runs.finish.StateReason != "route unavailable" {
		t.Fatalf("finish input = %+v", runs.finish)
	}
}

// Only the item's own session and the frozen item id count.
func TestSubmitAssetListItemRefusesAnOrdinarySession(t *testing.T) {
	runs := workingSubmitRuns()
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string) ([]generation.Task, error) {
			t.Fatal("an ordinary session must not read tasks")
			return nil, nil
		})
	command := itemSubmitCommand(`{"status":"done"}`)
	command.Options.AssetListItemID = ""
	result, err := service.SubmitAssetListItem(context.Background(), command)
	if err != nil || !result.NoSubmission || result.Error == "" {
		t.Fatalf("ordinary session result = %+v err=%v", result, err)
	}
	command = itemSubmitCommand(`{"status":"done"}`)
	command.Options.AssetListItemID = "aitem_other"
	result, err = service.SubmitAssetListItem(context.Background(), command)
	if err != nil || !result.NoSubmission || result.Error == "" {
		t.Fatalf("mismatched item result = %+v err=%v", result, err)
	}
	if runs.finish.ItemID != "" {
		t.Fatalf("a refused report finished the item: %+v", runs.finish)
	}
}

// A repeated report returns the terminal state it already reached; another
// request against the same item is refused.
func TestSubmitAssetListItemRepeatIsIdempotent(t *testing.T) {
	runs := workingSubmitRuns()
	runs.item.State = assetlist.StateReview
	runs.item.Result = json.RawMessage(`{"task_id":"task-2","assets":[]}`)
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string) ([]generation.Task, error) {
			return nil, nil
		})
	result, err := service.SubmitAssetListItem(context.Background(),
		itemSubmitCommand(`{"status":"done"}`))
	if err != nil || result.Error != "" || !result.NoSubmission {
		t.Fatalf("repeat done result = %+v err=%v", result, err)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatalf("agent content %q: %v", result.AgentContent, err)
	}
	if content["state"] != assetlist.StateReview || content["status"] != "done" ||
		content["task_id"] != "task-2" {
		t.Fatalf("repeat done content = %v", content)
	}
	if runs.finish.ItemID != "" {
		t.Fatalf("an idempotent repeat finished the item again: %+v", runs.finish)
	}
	result, err = service.SubmitAssetListItem(context.Background(),
		itemSubmitCommand(`{"status":"failed","reason":"late"}`))
	if err != nil || !result.NoSubmission || result.Error == "" {
		t.Fatalf("conflicting report result = %+v err=%v", result, err)
	}
}

// A stopped or completed run does not take item reports.
func TestSubmitAssetListItemRefusesATerminalRun(t *testing.T) {
	for _, status := range []string{assetlist.RunStopped, assetlist.RunCompleted} {
		runs := workingSubmitRuns()
		runs.run.Status = status
		service := newAssetListSubmitService(t, runs,
			func(context.Context, string, string) ([]generation.Task, error) {
				return nil, nil
			})
		result, err := service.SubmitAssetListItem(context.Background(),
			itemSubmitCommand(`{"status":"done"}`))
		if err != nil || !result.NoSubmission || result.Error == "" {
			t.Fatalf("%s run result = %+v err=%v", status, result, err)
		}
		if runs.finish.ItemID != "" {
			t.Fatalf("%s run finished the item: %+v", status, runs.finish)
		}
	}
}

// A paused run still lets an in-flight turn report its outcome.
func TestSubmitAssetListItemAllowsAPausedRun(t *testing.T) {
	runs := workingSubmitRuns()
	runs.run.Status = assetlist.RunPaused
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string) ([]generation.Task, error) {
			return []generation.Task{succeededTask("task-1",
				time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC))}, nil
		})
	result, err := service.SubmitAssetListItem(context.Background(),
		itemSubmitCommand(`{"status":"done"}`))
	if err != nil || result.Error != "" || runs.finish.State != assetlist.StateReview {
		t.Fatalf("paused run result = %+v finish=%+v err=%v", result, runs.finish, err)
	}
}

// Strict decoding refuses unknown fields and undeclared statuses.
func TestSubmitAssetListItemRefusesInvalidArguments(t *testing.T) {
	for _, arguments := range []string{
		`{"status":"perhaps"}`,
		`{"status":"done","task_id":"task-1"}`,
		`{}`,
		`not json`,
	} {
		runs := workingSubmitRuns()
		service := newAssetListSubmitService(t, runs,
			func(context.Context, string, string) ([]generation.Task, error) {
				return nil, nil
			})
		result, err := service.SubmitAssetListItem(context.Background(),
			itemSubmitCommand(arguments))
		if err != nil || !result.NoSubmission || result.Error == "" {
			t.Fatalf("%s result = %+v err=%v", arguments, result, err)
		}
		if runs.finish.ItemID != "" {
			t.Fatalf("%s finished the item: %+v", arguments, runs.finish)
		}
	}
}

// An internal failure becomes the Schema's generic refusal, never its text.
func TestSubmitAssetListItemDoesNotLeakInternalErrors(t *testing.T) {
	runs := workingSubmitRuns()
	runs.itemErr = errors.New("database is locked")
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string) ([]generation.Task, error) {
			return nil, nil
		})
	result, err := service.SubmitAssetListItem(context.Background(),
		itemSubmitCommand(`{"status":"done"}`))
	if err != nil || !result.NoSubmission || result.Error == "" ||
		strings.Contains(result.Error, "database") ||
		strings.Contains(result.AgentContent, "database") {
		t.Fatalf("internal error leaked: %+v err=%v", result, err)
	}

	runs = workingSubmitRuns()
	service = newAssetListSubmitService(t, runs,
		func(context.Context, string, string) ([]generation.Task, error) {
			return nil, errors.New("query failed")
		})
	result, err = service.SubmitAssetListItem(context.Background(),
		itemSubmitCommand(`{"status":"done"}`))
	if err != nil || !result.NoSubmission || result.Error == "" ||
		strings.Contains(result.Error, "query") {
		t.Fatalf("task source error leaked: %+v err=%v", result, err)
	}
}
