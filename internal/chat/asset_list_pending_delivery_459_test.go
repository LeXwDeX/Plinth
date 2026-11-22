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
//
// Only slots that map to a declared format hold the item. A background slot
// with no format, such as P2's thumbnail/rendered_image, is not a delivery the
// review waits for. The deferred check runs one post-success observation
// interval ahead (capped by the Catalog), never at the kernel's deadline.

// submitClock is the fake clock of the service these tests build.
var submitClock = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

// postSuccessTask is a succeeded P2-shaped Task: the main model landed and the
// other formats are post-success resources. The 24-hour deadline is
// deliberately far away so a regression to generation.PostSuccessDeadline is
// visible in the assertions.
func postSuccessTask(id string, createdAt time.Time,
	resources map[string]generation.ResourceState) generation.Task {
	return generation.Task{
		ID: generation.TaskID(id), State: generation.TaskSucceeded, CreatedAt: createdAt,
		Offering: generation.Offering{
			ID: "tripo/tripo-p2/text-to-3d",
			PostSuccess: &generation.PostSuccessPolicy{
				Observe: true, IntervalSeconds: 60, DeadlineSeconds: 86400,
			},
		},
		Resources: resources,
	}
}

func pendingFormatTask(createdAt time.Time, state generation.ResourceStateCode) generation.Task {
	return postSuccessTask("task-pending", createdAt, map[string]generation.ResourceState{
		"converted_model|converted.fbx|":  {State: state},
		"converted_model|converted.gltf|": {State: generation.ResourceReady},
	})
}

func TestSubmitAssetListItemDoneDefersAnUnstableDelivery459(t *testing.T) {
	runs := workingSubmitRuns()
	createdAt := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	finished := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	// One format slot and one background thumbnail slot are still pending.
	task := postSuccessTask("task-pending", createdAt, map[string]generation.ResourceState{
		"converted_model|converted.fbx|":  {State: generation.ResourcePending},
		"converted_model|converted.gltf|": {State: generation.ResourceReady},
		"thumbnail|rendered_image|":       {State: generation.ResourcePending},
	})
	task.FinishedAt = &finished
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string, generation.TaskID) ([]generation.Task, error) {
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
	// The next check is one observation interval ahead, not "now" and not the
	// kernel's 24-hour post-success deadline.
	if want := submitClock.Add(60 * time.Second); !runs.nextActionAt.Equal(want) {
		t.Fatalf("next action = %v, want one observation interval %v", runs.nextActionAt, want)
	}
	if runs.nextActionAt.Equal(generation.PostSuccessDeadline(task)) {
		t.Fatalf("next action = the post-success deadline %v, want the observation interval",
			runs.nextActionAt)
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

func TestSubmitAssetListItemDoneCapsTheDeferredCheck459(t *testing.T) {
	runs := workingSubmitRuns()
	createdAt := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	task := pendingFormatTask(createdAt, generation.ResourcePending)
	schema := defaultAssetListSchema(t)
	// The Offering's observation interval is 60s; the Catalog caps the wait
	// at 30s, so the item is checked again well before the interval.
	schema.DeliverySettleMaxSeconds = 30
	service := newAssetListSubmitServiceWithSchema(t, runs, schema,
		func(context.Context, string, string, generation.TaskID) ([]generation.Task, error) {
			return []generation.Task{task}, nil
		})
	if _, err := service.SubmitAssetListItem(context.Background(),
		itemSubmitCommand(`{"status":"done"}`)); err != nil {
		t.Fatal(err)
	}
	if want := submitClock.Add(30 * time.Second); !runs.nextActionAt.Equal(want) {
		t.Fatalf("next action = %v, want the Catalog cap %v", runs.nextActionAt, want)
	}
}

func TestSubmitAssetListItemDoneFinishesWhenOnlyABackgroundSlotIsPending459(t *testing.T) {
	runs := workingSubmitRuns()
	createdAt := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	// P2's thumbnail is a background output with no declared format: it is
	// still pending after the model landed and must not hold the item.
	task := postSuccessTask("task-thumb", createdAt, map[string]generation.ResourceState{
		"converted_model|converted.fbx|": {State: generation.ResourceReady},
		"thumbnail|rendered_image|":      {State: generation.ResourcePending},
	})
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string, generation.TaskID) ([]generation.Task, error) {
			return []generation.Task{task}, nil
		})
	result, err := service.SubmitAssetListItem(context.Background(),
		itemSubmitCommand(`{"status":"done"}`))
	if err != nil || result.Error != "" {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	if runs.deferred.Result != nil || !runs.nextActionAt.IsZero() {
		t.Fatalf("the item was deferred for a background slot: %+v %v",
			runs.deferred, runs.nextActionAt)
	}
	if runs.finish.State != assetlist.StateReview || runs.finish.ItemID != "aitem_1" {
		t.Fatalf("finish input = %+v", runs.finish)
	}
}

func TestSettleReportedItemWaitsThenFinishes459(t *testing.T) {
	runs := workingSubmitRuns()
	createdAt := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	runs.item.Result = json.RawMessage(`{"task_id":"task-pending","assets":[]}`)

	// While a format is still converting the item keeps its state, no review
	// is written, and the next check moves one observation interval ahead.
	pending := pendingFormatTask(createdAt, generation.ResourcePending)
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string, generation.TaskID) ([]generation.Task, error) {
			return []generation.Task{pending}, nil
		})
	now := time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)
	item, handled, err := service.SettleReportedItem(context.Background(),
		settleInput(runs, []generation.Task{pending}, now))
	if err != nil || !handled {
		t.Fatalf("pending settle = %+v handled=%v err=%v", item, handled, err)
	}
	if runs.finish.ItemID != "" {
		t.Fatalf("an unstable delivery entered review: %+v", runs.finish)
	}
	if want := now.Add(60 * time.Second); !runs.nextActionAt.Equal(want) {
		t.Fatalf("next action = %v, want one observation interval %v", runs.nextActionAt, want)
	}

	// Every terminal slot state finishes the same report into review: ready
	// delivered its file, and failed/incomplete/abandoned/denied are terminal
	// too (the format is missing and can be completed again).
	for index, state := range []generation.ResourceStateCode{
		generation.ResourceReady, generation.ResourceFailed,
		generation.ResourceIncomplete, generation.ResourceAbandoned,
	} {
		settled := pendingFormatTask(createdAt, state)
		runs.finish = assetlist.FinishItemInput{}
		item, handled, err = service.SettleReportedItem(context.Background(),
			settleInput(runs, []generation.Task{settled},
				now.Add(time.Duration(index+1)*time.Minute)))
		if err != nil || !handled {
			t.Fatalf("%s settle = %+v handled=%v err=%v", state, item, handled, err)
		}
		if runs.finish.State != assetlist.StateReview || runs.finish.ItemID != "aitem_1" {
			t.Fatalf("%s finish input = %+v", state, runs.finish)
		}
		if !strings.Contains(string(runs.finish.Result), "task-pending") {
			t.Fatalf("%s result = %s", state, runs.finish.Result)
		}
	}
}

func TestSettleReportedItemFinishesWhenOnlyABackgroundSlotIsPending459(t *testing.T) {
	runs := workingSubmitRuns()
	createdAt := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	runs.item.Result = json.RawMessage(`{"task_id":"task-thumb","assets":[]}`)
	task := postSuccessTask("task-thumb", createdAt, map[string]generation.ResourceState{
		"converted_model|converted.fbx|": {State: generation.ResourceReady},
		"thumbnail|rendered_image|":      {State: generation.ResourcePending},
	})
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string, generation.TaskID) ([]generation.Task, error) {
			return []generation.Task{task}, nil
		})
	item, handled, err := service.SettleReportedItem(context.Background(),
		settleInput(runs, []generation.Task{task},
			time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)))
	if err != nil || !handled {
		t.Fatalf("settle = %+v handled=%v err=%v", item, handled, err)
	}
	if runs.finish.State != assetlist.StateReview {
		t.Fatalf("a background slot held the item back: %+v", runs.finish)
	}
	if !runs.nextActionAt.IsZero() {
		t.Fatalf("a finished item was rescheduled: %v", runs.nextActionAt)
	}
}

func TestSettleReportedItemIgnoresAWorkingItemWithoutAReport459(t *testing.T) {
	runs := workingSubmitRuns()
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string, generation.TaskID) ([]generation.Task, error) {
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
