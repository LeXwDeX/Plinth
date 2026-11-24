package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
)

// The production-list completion tool (#328): inside an item's own sub-session
// the Agent reports whether the item is done or failed. The server decides
// what "done" means: a successful Task of this session must exist and its
// public facts are snapshotted into result_json. The Agent cannot name a Task
// or an asset, and a refusal never leaks an internal error text.

// AssetListTaskSource reads the terminal Tasks created in one sub-session,
// newest first, scoped to the owner. When taskID is not empty it reads that one
// Task by id instead, so a stored report whose Task is older than the newest
// page is still found (an item's sub-session can create more than a hundred
// terminal Tasks); a Task that is gone yields no Tasks. Branch ancestors are
// deliberately not included: an item's outcome is the work of its own session.
// The application supplies it from generation.Module.
type AssetListTaskSource func(ctx context.Context, ownerID, sessionID string, taskID generation.TaskID) ([]generation.Task, error)

// Completion statuses the tool accepts. A status other than these is refused
// before any list state is read.
const (
	assetListItemStatusDone   = "done"
	assetListItemStatusFailed = "failed"
)

// submittedAssetListItemArguments is the tool's declared object. The item,
// owner and session never come from the arguments: the server holds them in
// the tool command and in the turn's frozen AssetListItemID.
type submittedAssetListItemArguments struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// assetListItemResultAsset is one asset reference of the result snapshot. The
// generation Asset carries no display name, so name is the role (with its
// variant) the same way the work ledger names a result.
type assetListItemResultAsset struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// assetListItemResultSnapshot is what result_json stores when the item is
// reported done. It is a server-built snapshot, never an Agent payload.
type assetListItemResultSnapshot struct {
	TaskID string                     `json:"task_id"`
	Assets []assetListItemResultAsset `json:"assets"`
}

// assetListItemReportContent is the Agent-facing result of a stored report.
type assetListItemReportContent struct {
	ItemID  string `json:"item_id"`
	State   string `json:"state"`
	Status  string `json:"status"`
	TaskID  string `json:"task_id,omitempty"`
	Message string `json:"message"`
}

// SubmitAssetListItem validates and stores the outcome of the production-list
// item whose sub-session this turn belongs to (#328). Only a staff-verified
// AssetListItemID counts; an ordinary session, another item's session or an
// item that is no longer working is refused with the Schema's copy. Repeating
// the same report after the item reached the matching terminal state returns
// that state instead of failing, so a retried tool call is idempotent.
func (service *Service) SubmitAssetListItem(ctx context.Context, command ToolCommand) (ToolResult, error) {
	policy := service.assetLists
	if policy == nil || policy.Service == nil || policy.Tasks == nil {
		return assetListRefusal("asset lists are unavailable"), nil
	}
	messages := policy.Schema.Messages
	reportFailed := assetListMessage(messages.ItemReportFailed,
		"the item report could not be stored")
	var arguments submittedAssetListItemArguments
	decoder := json.NewDecoder(strings.NewReader(string(command.Call.Arguments)))
	decoder.DisallowUnknownFields()
	if len(command.Call.Arguments) == 0 || decoder.Decode(&arguments) != nil ||
		decoder.Decode(new(any)) != io.EOF {
		return assetListRefusal(reportFailed), nil
	}
	status := strings.TrimSpace(arguments.Status)
	if status != assetListItemStatusDone && status != assetListItemStatusFailed {
		return assetListRefusal(reportFailed), nil
	}
	itemID := strings.TrimSpace(command.Options.AssetListItemID)
	if itemID == "" {
		return assetListRefusal(assetListMessage(messages.ItemNotInSession,
			"the session is not a production-list item's session")), nil
	}
	item, found, err := policy.Service.ItemBySession(ctx, command.OwnerID, command.SessionID)
	if err != nil {
		return assetListRefusal(reportFailed), nil
	}
	if !found || item.ID != itemID {
		return assetListRefusal(assetListMessage(messages.ItemNotInSession,
			"the session is not a production-list item's session")), nil
	}
	// A repeated report is in the terminal state it asks for; returning that
	// state keeps a retried tool call idempotent even after the run stopped.
	if item.State != assetlist.StateWorking {
		if (status == assetListItemStatusDone && item.State == assetlist.StateReview) ||
			(status == assetListItemStatusFailed && item.State == assetlist.StateFailed) {
			return assetListItemReported(item, status, AssetListItemResultTaskID(item.Result),
				messages), nil
		}
		return assetListRefusal(assetListMessage(messages.ItemNotWorking,
			"the production-list item is not working")), nil
	}
	// A paused run still lets an in-flight turn report its outcome; a terminal
	// run does not. An absent run (a deployment without the runner) is not a
	// reason to strand the item.
	run, err := policy.Service.RunByList(ctx, command.OwnerID,
		strings.TrimSpace(command.Options.ProjectID), item.ListID)
	if err != nil && !errors.Is(err, assetlist.ErrNotFound) {
		return assetListRefusal(reportFailed), nil
	}
	if err == nil && (run.Status == assetlist.RunStopped || run.Status == assetlist.RunCompleted) {
		return assetListRefusal(reportFailed), nil
	}
	tasks, err := policy.Tasks(ctx, command.OwnerID, command.SessionID, "")
	if err != nil {
		return assetListRefusal(reportFailed), nil
	}
	switch status {
	case assetListItemStatusDone:
		task, ok := latestAssetListTask(tasks, generation.TaskSucceeded)
		if !ok {
			return assetListRefusal(assetListMessage(messages.ItemNoSuccessfulTask,
				"the session has no successful task")), nil
		}
		// A rework session reports a new version (#508): until the rework
		// produced another success, the newest success of the sub-session is
		// the Task the person sent back, and reporting it again would put the
		// returned version back into review.
		if item.ReturnCount > 0 && string(task.ID) == assetListReviewedTaskID(item.Review) {
			return assetListRefusal(assetListMessage(messages.ItemNoNewTask,
				"the returned version cannot be reported again; produce a new result first")), nil
		}
		// A success can still owe formats: the platform converts the other
		// formats as post-success resources after the main model landed
		// (#457). The item must not enter review before that delivery is
		// stable, so the report is stored on the working item and the runner
		// moves it into review when the formats settle (#459). Only format
		// slots count: a background slot that maps to no format (P2's
		// thumbnail/rendered_image) must not hold the item back.
		if len(modeldelivery.PendingResourceFormats(policy.Delivery, task)) > 0 {
			return service.deferReportedItem(ctx, policy, item, task, messages)
		}
		brief, _ := json.Marshal(command.Options.ProductionBrief)
		finished, err := service.FinishReportedItem(ctx, FinishReportedItemInput{
			OwnerID:   command.OwnerID,
			SessionID: command.SessionID,
			ProjectID: strings.TrimSpace(command.Options.ProjectID),
			ListID:    item.ListID,
			ItemID:    item.ID,
			Kind:      item.Kind,
			Brief:     brief,
			Round:     item.Round,
			Task:      task,
			Tasks:     tasks,
			At:        service.clock(),
		})
		if err != nil {
			return assetListRefusal(reportFailed), nil
		}
		return assetListItemReported(finished, status, string(task.ID), messages), nil
	case assetListItemStatusFailed:
		stateReason := strings.TrimSpace(arguments.Reason)
		taskID := ""
		if task, ok := latestAssetListTask(tasks, generation.TaskFailed); ok {
			taskID = string(task.ID)
			if task.Failure != nil {
				report := generation.ClassifyFailure(task.Failure.Class,
					task.Failure.Retryable, task.Failure.Ambiguous)
				stateReason = string(report.Category)
			}
		}
		finished, err := policy.Service.FinishItem(ctx, assetlist.FinishItemInput{
			OwnerID:     command.OwnerID,
			ListID:      item.ListID,
			ItemID:      item.ID,
			State:       assetlist.StateFailed,
			StateReason: stateReason,
			At:          service.clock(),
		})
		if err != nil {
			return assetListRefusal(reportFailed), nil
		}
		return assetListItemReported(finished, status, taskID, messages), nil
	}
	return assetListRefusal(reportFailed), nil
}

// FinishReportedItemInput is one done report of a working item, ready to be
// stored: the Task the report names and the sub-session's Tasks for the step
// summary, the item's frozen production brief and the state it was reported in.
type FinishReportedItemInput struct {
	OwnerID string
	// SessionID is the sub-session the report belongs to: the turn's session
	// for the completion tool, the item's bound session for the runner. It
	// scopes the self-check record and the purpose thresholds the snapshot
	// resolves, so it is never derived from a stored Task.
	SessionID string
	ProjectID string
	ListID    string
	ItemID    string
	Kind      string
	Brief     json.RawMessage
	Round     int
	Task      generation.Task
	Tasks     []generation.Task
	At        time.Time
}

// FinishReportedItem stores a done report and moves the item into review
// (#329b). The result snapshot is server-built and the review snapshot is
// generated in the same step that moves the item to waiting for the person; its
// ports resolve the purpose thresholds and inspect the GLB/picture, and a
// failure leaves the report stored with an empty snapshot, never a stuck item.
//
// Both the completion tool and the runner's delivery tick call it (#459), so a
// report deferred for an unstable delivery is finished exactly like one that
// arrived after the formats settled.
func (service *Service) FinishReportedItem(
	ctx context.Context,
	input FinishReportedItemInput,
) (assetlist.Item, error) {
	policy := service.assetLists
	if policy == nil || policy.Service == nil || policy.Tasks == nil {
		return assetlist.Item{}, assetlist.ErrNotFound
	}
	result, err := json.Marshal(assetListItemResultSnapshot{
		TaskID: string(input.Task.ID),
		Assets: assetListItemResultAssets(input.Task.Assets),
	})
	if err != nil {
		return assetlist.Item{}, err
	}
	review, snapshotErr := policy.Service.ReviewSnapshot(ctx, assetlist.ReviewSnapshotInput{
		OwnerID:   input.OwnerID,
		SessionID: input.SessionID,
		ProjectID: strings.TrimSpace(input.ProjectID),
		ListID:    input.ListID,
		ItemID:    input.ItemID,
		Kind:      input.Kind,
		Brief:     input.Brief,
		Round:     input.Round,
		Task:      input.Task,
		Tasks:     input.Tasks,
		At:        input.At,
	})
	if snapshotErr != nil || len(review) == 0 {
		review = nil
	}
	return policy.Service.FinishItem(ctx, assetlist.FinishItemInput{
		OwnerID: input.OwnerID,
		ListID:  input.ListID,
		ItemID:  input.ItemID,
		State:   assetlist.StateReview,
		Result:  result,
		Review:  review,
		At:      input.At,
	})
}

// SettleReportedItemInput is one working item whose done report may be waiting
// for its delivery to settle (#459). The report itself is the item's stored
// result_json; Tasks is the sub-session's Task source.
type SettleReportedItemInput struct {
	OwnerID   string
	ProjectID string
	ListID    string
	Item      assetlist.Item
	Brief     json.RawMessage
	Tasks     []generation.Task
	Now       time.Time
}

// SettleReportedItem finishes a working item whose done report is stored, once
// the delivery that report names is stable (#459). It reports false when the
// item has no stored report (an ordinary working item, which the loop
// continues), and true when it finished the item or left it waiting: the loop
// must not open another turn for it either way.
//
// This is the one place that advances a deferred report, so the automatic
// completion reconciler of #461 can settle an item the same way once it has
// finished the formats it was completing.
func (service *Service) SettleReportedItem(
	ctx context.Context,
	input SettleReportedItemInput,
) (assetlist.Item, bool, error) {
	policy := service.assetLists
	if policy == nil || policy.Service == nil || policy.Tasks == nil {
		return input.Item, false, nil
	}
	// Only a deferred done report is settled here (#508). A stored result
	// without the deferral mark is not a report of this work session (an
	// older row's returned version), so the item continues as an ordinary one
	// instead of re-entering review with it.
	if !input.Item.DeliveryDeferred {
		return input.Item, false, nil
	}
	taskID := AssetListItemResultTaskID(input.Item.Result)
	if taskID == "" {
		return input.Item, false, nil
	}
	var task generation.Task
	found := false
	for _, candidate := range input.Tasks {
		if string(candidate.ID) == taskID {
			task, found = candidate, true
			break
		}
	}
	if !found {
		// The reported Task is gone (deleted with its session): there is no
		// delivery left to wait for, so the item continues as an ordinary one.
		return input.Item, false, nil
	}
	if len(modeldelivery.PendingResourceFormats(policy.Delivery, task)) > 0 {
		// Still converting: the item keeps its state, and its next check moves
		// one observation interval ahead (capped by the Catalog) instead of
		// waiting for the kernel's deadline.
		if _, err := policy.Service.RescheduleDeferredReport(ctx,
			assetlist.RescheduleDeferredReportInput{
				OwnerID:         input.OwnerID,
				ListID:          input.ListID,
				ItemID:          input.Item.ID,
				NextActionAt:    assetListNextDeliveryCheck(policy, task, input.Now),
				ExpectedVersion: input.Item.Version,
				At:              input.Now,
			}); err != nil {
			return input.Item, true, err
		}
		return input.Item, true, nil
	}
	finished, err := service.FinishReportedItem(ctx, FinishReportedItemInput{
		OwnerID:   input.OwnerID,
		SessionID: input.Item.SessionID,
		ProjectID: input.ProjectID,
		ListID:    input.ListID,
		ItemID:    input.Item.ID,
		Kind:      input.Item.Kind,
		Brief:     input.Brief,
		Round:     input.Item.Round,
		Task:      task,
		Tasks:     input.Tasks,
		At:        input.Now,
	})
	if err != nil {
		return input.Item, true, err
	}
	return finished, true, nil
}

// assetListItemDeliveryPending stores a done report whose delivery is not
// stable yet (#459): the formats the task still owes are still converting, so
// the item keeps its state and its next scheduling instant moves one
// post-success observation interval ahead (capped by the Catalog). The
// runner's delivery tick moves the item into review once every format is
// terminal; the report is never lost.
func (service *Service) deferReportedItem(
	ctx context.Context,
	policy *AssetListPolicy,
	item assetlist.Item,
	task generation.Task,
	messages catalog.AssetListMessagesSchema,
) (ToolResult, error) {
	result, err := json.Marshal(assetListItemResultSnapshot{
		TaskID: string(task.ID),
		Assets: assetListItemResultAssets(task.Assets),
	})
	if err != nil {
		return assetListRefusal(assetListMessage(messages.ItemReportFailed,
			"the item report could not be stored")), nil
	}
	now := service.clock()
	if _, err := policy.Service.DeferReport(ctx, assetlist.DeferReportInput{
		OwnerID:         item.OwnerID,
		ListID:          item.ListID,
		ItemID:          item.ID,
		Result:          result,
		NextActionAt:    assetListNextDeliveryCheck(policy, task, now),
		ExpectedVersion: item.Version,
		At:              now,
	}); err != nil {
		return assetListRefusal(assetListMessage(messages.ItemReportFailed,
			"the item report could not be stored")), nil
	}
	message := strings.NewReplacer("{name}", item.Name).Replace(
		assetListMessage(messages.ItemDeliveryPending,
			"the item's formats are still converting, so it does not enter review yet"))
	content, err := json.Marshal(assetListItemReportContent{
		ItemID:  item.ID,
		State:   item.State,
		Status:  assetListItemStatusDone,
		TaskID:  string(task.ID),
		Message: message,
	})
	if err != nil {
		return ToolResult{Error: message, NoSubmission: true}, nil
	}
	summary, _ := json.Marshal(map[string]string{
		"asset_list_item_id": item.ID,
		"status":             assetListItemStatusDone,
	})
	return ToolResult{
		AgentContent: string(content),
		Summary:      summary,
		NoSubmission: true,
	}, nil
}

// assetListNextDeliveryCheck is when the runner re-checks a deferred done
// report (#459): one post-success observation interval of the reported
// Offering (the kernel's read-only export; 60s when the Offering declares
// none), capped by the Catalog's asset_list.delivery_settle_max_seconds so a
// long interval cannot strand the item in working. A Catalog without the cap
// leaves the interval un-capped.
func assetListNextDeliveryCheck(policy *AssetListPolicy, task generation.Task, now time.Time) time.Time {
	wait := generation.PostSuccessInterval(task)
	if declared := policy.Schema.DeliverySettleMaxSeconds; declared > 0 {
		if maxWait := time.Duration(declared) * time.Second; wait > maxWait {
			wait = maxWait
		}
	}
	return now.Add(wait)
}

// latestAssetListTask picks the newest Task in one state. The source reads
// newest first, but the choice is made by time so a differently ordered source
// cannot report an older success or failure.
func latestAssetListTask(tasks []generation.Task, state generation.TaskState) (generation.Task, bool) {
	var latest generation.Task
	found := false
	for _, task := range tasks {
		if task.State != state {
			continue
		}
		if !found || task.CreatedAt.After(latest.CreatedAt) ||
			(task.CreatedAt.Equal(latest.CreatedAt) && string(task.ID) > string(latest.ID)) {
			latest = task
			found = true
		}
	}
	return latest, found
}

// assetListItemResultAssets snapshots the public identity of a Task's assets.
func assetListItemResultAssets(assets []generation.Asset) []assetListItemResultAsset {
	result := make([]assetListItemResultAsset, 0, len(assets))
	for _, asset := range assets {
		name := asset.Role
		if asset.Variant != "" {
			name += "/" + asset.Variant
		}
		result = append(result, assetListItemResultAsset{
			ID:   string(asset.ID),
			Name: name,
			Role: asset.Role,
		})
	}
	return result
}

// AssetListItemResultTaskID reads back the snapshot's Task id for an
// idempotent reply. A failed item stores no snapshot, so the id stays empty.
// The asset-list runner uses it to read that one Task by id when it settles a
// deferred report (#473).
func AssetListItemResultTaskID(raw json.RawMessage) string {
	var snapshot struct {
		TaskID string `json:"task_id"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &snapshot) != nil {
		return ""
	}
	return snapshot.TaskID
}

// assetListReviewedTaskID reads the Task of the item's review snapshot: the
// version the person last decided. A return keeps that snapshot while it clears
// the reported result (#508), so a rework report can tell the returned version
// from a new one. An item without a snapshot names no Task.
func assetListReviewedTaskID(raw json.RawMessage) string {
	return AssetListItemResultTaskID(raw)
}

// assetListItemReported renders the stored terminal state with the Schema's
// success copy.
func assetListItemReported(item assetlist.Item, status, taskID string,
	messages catalog.AssetListMessagesSchema) ToolResult {
	declared := messages.ItemReportedDone
	fallback := "the production-list item was reported done"
	if status == assetListItemStatusFailed {
		declared = messages.ItemReportedFailed
		fallback = "the production-list item was reported failed"
	}
	message := strings.NewReplacer("{name}", item.Name).Replace(assetListMessage(declared, fallback))
	content, err := json.Marshal(assetListItemReportContent{
		ItemID:  item.ID,
		State:   item.State,
		Status:  status,
		TaskID:  taskID,
		Message: message,
	})
	if err != nil {
		return ToolResult{Error: fallback, NoSubmission: true}
	}
	summary, _ := json.Marshal(map[string]string{
		"asset_list_item_id": item.ID,
		"status":             status,
	})
	return ToolResult{
		AgentContent: string(content),
		Summary:      summary,
		NoSubmission: true,
	}
}
