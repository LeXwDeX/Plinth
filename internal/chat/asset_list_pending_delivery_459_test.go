package chat_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #459: a 3D result can succeed while the platform is still converting its
// other formats as post-success resources (#457's mixed post_success shape).
// The item must not enter review before that delivery is stable: the report is
// stored on the working item, and the runner's delivery tick moves it into
// review once every format is terminal.

// pendingDeliveryTask is the reported Task while one of its formats is still
// converting.
func pendingDeliveryTask(createdAt time.Time, state generation.ResourceStateCode) generation.Task {
	return generation.Task{
		ID: "task-pending", State: generation.TaskSucceeded, CreatedAt: createdAt,
		Resources: map[string]generation.ResourceState{
			"converted_model|converted.fbx|":  {State: state},
			"converted_model|converted.gltf|": {State: generation.ResourceReady},
		},
	}
}

func TestSubmitAssetListItemDoneDefersAnUnstableDelivery459(t *testing.T) {
	runs := workingSubmitRuns()
	createdAt := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	task := pendingDeliveryTask(createdAt, generation.ResourcePending)
	finished := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	task.FinishedAt = &finished
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string) ([]generation.Task, error) {
			return []generation.Task{task}, nil
		})
	result, err := service.SubmitAssetListItem(context.Background(),
		itemSubmitCommand(`{"status":"done"}`))
	if err != nil || result.Error != "" || !result.NoSubmission {
		t.Fatalf("deferred result = %+v err=%v", result, err)
	}
	// The item keeps its state: nothing moved it into review.
	if runs.finish.ItemID != "" {
		t.Fatalf("an unstable delivery entered review: %+v", runs.finish)
	}
	if runs.deferred.Result == nil || !strings.Contains(string(runs.deferred.Result), "task-pending") {
		t.Fatalf("the report was not stored: %+v", runs.deferred)
	}
	// The next check is the kernel's post-success deadline, not "now": the
	// formats settle on their own.
	if want := generation.PostSuccessDeadline(task); !runs.nextActionAt.Equal(want) {
		t.Fatalf("next action = %v, want the post-success deadline %v", runs.nextActionAt, want)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatalf("agent content %q: %v", result.AgentContent, err)
	}
	if content["state"] != assetlist.StateWorking || content["status"] != "done" ||
		content["task_id"] != "task-pending" {
		t.Fatalf("deferred content = %v", content)
	}
	if !strings.Contains(content["message"].(string), "待验收") {
		t.Fatalf("the Agent was not told why: %v", content["message"])
	}
}

func TestSettleReportedItemWaitsThenFinishes459(t *testing.T) {
	runs := workingSubmitRuns()
	createdAt := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	runs.item.Result = json.RawMessage(`{"task_id":"task-pending","assets":[]}`)

	// While the format is still converting the item keeps its state and no
	// review is written.
	pending := pendingDeliveryTask(createdAt, generation.ResourcePending)
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string) ([]generation.Task, error) {
			return []generation.Task{pending}, nil
		})
	item, handled, err := service.SettleReportedItem(context.Background(),
		settleInput(runs, []generation.Task{pending}, time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)))
	if err != nil || !handled {
		t.Fatalf("pending settle = %+v handled=%v err=%v", item, handled, err)
	}
	if runs.finish.ItemID != "" {
		t.Fatalf("an unstable delivery entered review: %+v", runs.finish)
	}

	// Once the slot is terminal the same report is finished into review.
	settled := pendingDeliveryTask(createdAt, generation.ResourceReady)
	service = newAssetListSubmitService(t, runs,
		func(context.Context, string, string) ([]generation.Task, error) {
			return []generation.Task{settled}, nil
		})
	item, handled, err = service.SettleReportedItem(context.Background(),
		settleInput(runs, []generation.Task{settled}, time.Date(2026, 11, 18, 11, 30, 0, 0, time.UTC)))
	if err != nil || !handled {
		t.Fatalf("settled = %+v handled=%v err=%v", item, handled, err)
	}
	if runs.finish.State != assetlist.StateReview || runs.finish.ItemID != "aitem_1" {
		t.Fatalf("finish input = %+v", runs.finish)
	}
	if !strings.Contains(string(runs.finish.Result), "task-pending") {
		t.Fatalf("result = %s", runs.finish.Result)
	}
}

func TestSettleReportedItemIgnoresAWorkingItemWithoutAReport459(t *testing.T) {
	runs := workingSubmitRuns()
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string) ([]generation.Task, error) {
			return nil, nil
		})
	_, handled, err := service.SettleReportedItem(context.Background(), settleInput(runs, nil, time.Now()))
	if err != nil || handled {
		t.Fatalf("settle without a report = %v err=%v", handled, err)
	}
	if runs.finish.ItemID != "" {
		t.Fatalf("an ordinary working item was finished: %+v", runs.finish)
	}
}

func settleInput(runs *submitRuns, tasks []generation.Task, now time.Time) chat.SettleReportedItemInput {
	return chat.SettleReportedItemInput{
		OwnerID: runs.item.OwnerID, ProjectID: "project-a", ListID: runs.item.ListID,
		Item: runs.item, Brief: json.RawMessage(`{}`), Tasks: tasks, Now: now,
	}
}
