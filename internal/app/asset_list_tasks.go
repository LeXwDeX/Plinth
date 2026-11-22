package app

import (
	"context"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// assetListTaskReader is the pair of generation.Module reads the asset-list
// Task source needs: one Task by id and one page of a session's terminal Tasks.
type assetListTaskReader interface {
	Get(ctx context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error)
	List(ctx context.Context, scope generation.TaskListScope, query generation.ListQuery) (generation.TaskPage, error)
}

// assetListTaskSource reads the terminal Tasks of an item's own sub-session:
// the completion tool and the asset-list runner share it. Branch ancestors are
// another session's work, so they are deliberately not included.
//
// A stored done report names its Task, so that one is read by id: an item's
// sub-session can create more terminal Tasks than one page holds, and a report
// the runner cannot find opens another (paid) turn for the item (#473). A Task
// that is gone, or that belongs to another session, falls back to the page,
// where the report's absence is decided as before.
func assetListTaskSource(reader assetListTaskReader) chat.AssetListTaskSource {
	return func(
		ctx context.Context,
		ownerID, sessionID string,
		taskID generation.TaskID,
	) ([]generation.Task, error) {
		if taskID != "" {
			task, err := reader.Get(ctx, generation.OwnerID(ownerID), taskID)
			switch {
			case err == nil && task.SessionID == sessionID:
				return []generation.Task{task}, nil
			case err == nil:
				// Another session's Task is not this item's outcome.
			case generation.IsCode(err, generation.ErrNotFound):
				// Deleted with its session: read the page instead.
			default:
				return nil, err
			}
		}
		page, err := reader.List(ctx, generation.OwnTasks(generation.OwnerID(ownerID)),
			generation.ListQuery{
				SessionID: sessionID,
				States: []generation.TaskState{
					generation.TaskSucceeded, generation.TaskFailed,
				},
				Limit: 100,
			})
		if err != nil {
			return nil, err
		}
		return page.Tasks, nil
	}
}
