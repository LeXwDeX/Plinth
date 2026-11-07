package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// queryRower is the read surface shared by *sql.DB, *sql.Conn and *sql.Tx.
type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// TaskInSession reports whether a Task is readable as a source of a chat
// session: it belongs to the session itself, or a branch ref names it. Owner
// is always required. A sibling session or another owner never matches.
func (r *Repository) TaskInSession(
	ctx context.Context,
	owner generation.OwnerID,
	session string,
	task generation.TaskID,
) (bool, error) {
	if owner == "" || session == "" || task == "" {
		return false, nil
	}
	return taskInSession(ctx, r.db, string(owner), session, string(task))
}

func taskInSession(ctx context.Context, q queryRower, owner, session, task string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `
		SELECT 1 FROM generation_tasks AS t
		WHERE t.id = ? AND t.owner_id = ?
		  AND (t.session_id = ? OR EXISTS (
			SELECT 1 FROM chat_branch_task_refs AS ref
			WHERE ref.owner_id = t.owner_id
			  AND ref.branch_session_id = ?
			  AND ref.task_id = t.id
		  ))`,
		task, owner, session, session,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// BranchTaskRefs lists the Tasks a branch inherited from its ancestors, owner
// scoped. The list is evidence only; callers still require a succeeded Task.
func (r *Repository) BranchTaskRefs(ctx context.Context, owner string, session string) ([]generation.TaskID, error) {
	if owner == "" || session == "" {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT task_id FROM chat_branch_task_refs
		WHERE owner_id = ? AND branch_session_id = ?
		ORDER BY created_at, task_id`, owner, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []generation.TaskID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, generation.TaskID(id))
	}
	return result, rows.Err()
}

// sessionInAncestry reports whether candidate is session itself or one of its
// recorded ancestors, always within one owner. It backs the fail-closed
// evidence checks of a copied branch scene (a copied representation keeps the
// historical prepared_input_id/source_session of an ancestor). UNION terminates
// a malformed parent cycle.
func sessionInAncestry(ctx context.Context, q queryRower, owner, session, candidate string) (bool, error) {
	if owner == "" || session == "" || candidate == "" {
		return false, nil
	}
	var one int
	err := q.QueryRowContext(ctx, `
		WITH RECURSIVE ancestry(id) AS (
			SELECT id FROM go_chat_sessions WHERE id = ? AND owner_id = ?
			UNION
			SELECT parent.parent_session_id
			FROM go_chat_sessions AS parent
			JOIN ancestry ON parent.id = ancestry.id
			WHERE parent.owner_id = ?
			  AND parent.parent_session_id <> ''
		)
		SELECT 1 FROM ancestry WHERE id = ?`,
		session, owner, owner, candidate,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// assertBranchTaskRefsBackfill is used by migration tests: it reports whether
// the upgrade created the expected refs for one branch.
func (r *Repository) BranchTaskRefRows(ctx context.Context, branch, owner string) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT task_id, source_session_id FROM chat_branch_task_refs
		WHERE branch_session_id = ? AND owner_id = ?`, branch, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var task, source string
		if err := rows.Scan(&task, &source); err != nil {
			return nil, err
		}
		result[task] = source
	}
	return result, rows.Err()
}

var _ generation.SessionTaskScope = (*Repository)(nil)

// insertBranchTaskRefs copies the source session's own Tasks and inherits its
// ancestor refs. It runs inside the branch creation transaction, after the
// branch row exists.
func insertBranchTaskRefs(ctx context.Context, tx *sql.Tx, owner, sourceSession, branchSession, at string) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO chat_branch_task_refs
			(branch_session_id, owner_id, task_id, source_session_id, created_at)
		SELECT ?, owner_id, id, session_id, ?
		FROM generation_tasks
		WHERE session_id = ? AND owner_id = ?`,
		branchSession, at, sourceSession, owner,
	); err != nil {
		return fmt.Errorf("copy branch task refs: %w", err)
	}
	// A branch of a branch keeps the grandparent Tasks its source could read;
	// this is the only way a second-generation branch sees them.
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO chat_branch_task_refs
			(branch_session_id, owner_id, task_id, source_session_id, created_at)
		SELECT ?, owner_id, task_id, source_session_id, ?
		FROM chat_branch_task_refs
		WHERE branch_session_id = ? AND owner_id = ?`,
		branchSession, at, sourceSession, owner,
	); err != nil {
		return fmt.Errorf("inherit branch task refs: %w", err)
	}
	return nil
}
