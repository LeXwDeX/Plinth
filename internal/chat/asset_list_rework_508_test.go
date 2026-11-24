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

// #508 asset-list-03: a returned item's rework session must report a new
// version. The Task the person sent back is still the newest success of the
// sub-session until the rework produces another, so a done report naming it
// is refused instead of re-entering review with the returned version.
func TestSubmitAssetListItemReworkDoneRefusesTheReturnedTask508(t *testing.T) {
	runs := workingSubmitRuns()
	runs.item.ReturnCount = 1
	runs.item.Review = json.RawMessage(`{"task_id":"task-1"}`)
	returned := succeededTask("task-1", time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
		generation.Asset{ID: "asset-old", Role: "model"})
	tasks := []generation.Task{returned}
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string, generation.TaskID) ([]generation.Task, error) {
			return tasks, nil
		})

	result, err := service.SubmitAssetListItem(context.Background(),
		itemSubmitCommand(`{"status":"done"}`))
	if err != nil || !result.NoSubmission || result.Error == "" ||
		!strings.Contains(result.Error, "打回") {
		t.Fatalf("rework done with the returned task = %+v err=%v", result, err)
	}
	if runs.finish.ItemID != "" || runs.deferred.Result != nil {
		t.Fatalf("the returned version was reported again: finish=%+v deferred=%+v",
			runs.finish, runs.deferred)
	}

	// Once the rework produced a new successful Task, done reports that one.
	tasks = append(tasks, succeededTask("task-2", time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC),
		generation.Asset{ID: "asset-new", Role: "model"}))
	result, err = service.SubmitAssetListItem(context.Background(),
		itemSubmitCommand(`{"status":"done"}`))
	if err != nil || result.Error != "" {
		t.Fatalf("rework done with a new task = %+v err=%v", result, err)
	}
	if runs.finish.State != assetlist.StateReview ||
		!strings.Contains(string(runs.finish.Result), "task-2") {
		t.Fatalf("finish input = %+v", runs.finish)
	}
}

// #508 asset-list-03: only a deferred done report is settled by the runner's
// delivery tick. A working item whose stored result is not a deferred report
// (the returned version of a rework session) continues as an ordinary item,
// so a rework turn that reports nothing does not re-enter review.
func TestSettleReportedItemIgnoresAResultThatIsNotADeferredReport508(t *testing.T) {
	runs := workingSubmitRuns()
	runs.item.Result = json.RawMessage(`{"task_id":"task-1","assets":[]}`)
	runs.item.DeliveryDeferred = false
	task := succeededTask("task-1", time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC))
	service := newAssetListSubmitService(t, runs,
		func(context.Context, string, string, generation.TaskID) ([]generation.Task, error) {
			return []generation.Task{task}, nil
		})
	_, handled, err := service.SettleReportedItem(context.Background(),
		settleInput(runs, []generation.Task{task}, time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)))
	if err != nil || handled {
		t.Fatalf("settle of a stale result = handled %v err=%v; want the ordinary turn path", handled, err)
	}
	if runs.finish.ItemID != "" {
		t.Fatalf("a stale result re-entered review: %+v", runs.finish)
	}
}

// #508 asset-list-06: the runner reads a turn that ended on an approval card
// through the Schema's own end reason, not a word of its own.
func TestTurnEndedAwaitingApprovalReadsTheSchemaReason508(t *testing.T) {
	service := newChatService(t, newMemoryChatRepository())
	if !service.TurnEndedAwaitingApproval(chat.Turn{EndReason: configuredEndReasons.AwaitingApproval}) {
		t.Fatal("a turn that left an approval card was not recognised")
	}
	for _, reason := range []string{configuredEndReasons.Completed, configuredEndReasons.Error, ""} {
		if service.TurnEndedAwaitingApproval(chat.Turn{EndReason: reason}) {
			t.Fatalf("end reason %q read as awaiting approval", reason)
		}
	}
}
