package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The production-list completion tool (#328): inside an item's own sub-session
// the Agent reports whether the item is done or failed. The server decides
// what "done" means: a successful Task of this session must exist and its
// public facts are snapshotted into result_json. The Agent cannot name a Task
// or an asset, and a refusal never leaks an internal error text.

// AssetListTaskSource reads the terminal Tasks created in one sub-session,
// newest first, scoped to the owner. Branch ancestors are deliberately not
// included: an item's outcome is the work of its own session. The application
// supplies it from generation.Module.List with the same owner scope.
type AssetListTaskSource func(ctx context.Context, ownerID, sessionID string) ([]generation.Task, error)

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
			return assetListItemReported(item, status, assetListItemResultTaskID(item.Result),
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
	tasks, err := policy.Tasks(ctx, command.OwnerID, command.SessionID)
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
		result, err := json.Marshal(assetListItemResultSnapshot{
			TaskID: string(task.ID),
			Assets: assetListItemResultAssets(task.Assets),
		})
		if err != nil {
			return assetListRefusal(reportFailed), nil
		}
		finished, err := policy.Service.FinishItem(ctx, assetlist.FinishItemInput{
			OwnerID: command.OwnerID,
			ListID:  item.ListID,
			ItemID:  item.ID,
			State:   assetlist.StateReview,
			Result:  result,
			At:      service.clock(),
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

// assetListItemResultTaskID reads back the snapshot's Task id for an
// idempotent reply. A failed item stores no snapshot, so the id stays empty.
func assetListItemResultTaskID(raw json.RawMessage) string {
	var snapshot struct {
		TaskID string `json:"task_id"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &snapshot) != nil {
		return ""
	}
	return snapshot.TaskID
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
