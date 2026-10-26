package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

const taskSummarySelect = `SELECT
 generation_tasks.id, owner_id, capability, summary.offering_json, summary.prompt_input_json, '',
 '', state, progress, failure_json, current_attempt_id,
 materialize_failures, next_action_at, version, created_at, updated_at, finished_at,
 generation_tasks.resource_state_json, generation_tasks.session_id,CASE WHEN generation_tasks.native_quote_required=1 THEN COALESCE(generation_tasks.native_quote_json,'null') ELSE generation_tasks.native_quote_json END,
 EXISTS (SELECT 1 FROM generation_attempts completion_attempt WHERE completion_attempt.task_id = generation_tasks.id AND completion_attempt.delivery_completion_json IS NOT NULL)
 FROM generation_tasks JOIN generation_task_summaries summary ON summary.task_id = generation_tasks.id`

func taskVisibility(scope generation.TaskListScope, id generation.TaskID) (string, []any) {
	where := `generation_tasks.id = ? AND NOT EXISTS (SELECT 1 FROM generation_task_tombstones WHERE task_id = generation_tasks.id)`
	args := []any{id}
	if !scope.AllOwners() {
		where += " AND owner_id = ?"
		args = append(args, scope.OwnerID())
	}
	return where, args
}
func (r *Repository) GetTaskSummary(ctx context.Context, scope generation.TaskListScope, id generation.TaskID) (generation.Task, error) {
	where, args := taskVisibility(scope, id)
	task, err := scanTask(r.db.QueryRowContext(ctx, taskSummarySelect+" WHERE "+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return task, generation.RepositoryNotFound
	}
	if err != nil {
		return task, err
	}
	task.Assets, err = r.loadAssets(ctx, r.db, id)
	return task, err
}
func (r *Repository) GetTaskVersion(ctx context.Context, scope generation.TaskListScope, id generation.TaskID) (int64, error) {
	where, args := taskVisibility(scope, id)
	var version int64
	err := r.db.QueryRowContext(ctx, "SELECT version FROM generation_tasks WHERE "+where, args...).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, generation.RepositoryNotFound
	}
	return version, err
}
