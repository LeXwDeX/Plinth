package sqlite

import (
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"strings"
)

// Sorting a compound SELECT directly lets SQLite merge its indexed branches.
// Only identifiers and timestamps enter the merge; params are read afterwards.
func administrationGenerationIDsSQL(query administration.GenerationQuery, legacy bool) (string, []any) {
	type branch struct{ selectFrom, baseWhere, user, kind, state, created string }
	branches := []branch{
		{`SELECT ledger.id AS record_id,ledger.task_id AS task_id,'native' AS record_source,unixepoch(ledger.created_at,'subsec') AS created_unix FROM generation_usage_ledger ledger JOIN generation_tasks task ON task.id=ledger.task_id`,
			`NOT EXISTS(SELECT 1 FROM generation_task_tombstones WHERE task_id=ledger.task_id)`, "ledger.user_id", "ledger.task_type", "task.state", "unixepoch(ledger.created_at,'subsec')"},
		{`SELECT task.id AS record_id,task.id AS task_id,'native' AS record_source,unixepoch(task.created_at,'subsec') AS created_unix FROM generation_tasks task`,
			`NOT EXISTS(SELECT 1 FROM generation_usage_ledger WHERE task_id=task.id) AND NOT EXISTS(SELECT 1 FROM generation_task_tombstones WHERE task_id=task.id)`, "task.owner_id", "task.capability", "task.state", "unixepoch(task.created_at,'subsec')"},
	}
	if legacy {
		branches = append(branches, branch{`SELECT CAST(ledger.id AS TEXT) AS record_id,ledger.taskId AS task_id,'legacy' AS record_source,` + databaseUnixExpression("ledger.createdAt") + ` AS created_unix FROM usage_ledger ledger`,
			`NOT EXISTS(SELECT 1 FROM generation_tasks WHERE id=ledger.taskId)`, "ledger.userId", "ledger.taskType", "ledger.taskStatus", databaseUnixExpression("ledger.createdAt")})
	}
	parts := make([]string, 0, len(branches))
	args := []any{}
	for _, b := range branches {
		filters := []string{b.baseWhere}
		for _, f := range []struct{ column, value string }{{b.user, query.UserID}, {b.kind, query.TaskType}, {b.state, query.Status}} {
			if f.value != "" {
				filters = append(filters, f.column+" = ?")
				args = append(args, f.value)
			}
		}
		if query.From != nil {
			filters = append(filters, b.created+" >= ?")
			args = append(args, unixSeconds(*query.From))
		}
		if query.To != nil {
			filters = append(filters, b.created+" <= ?")
			args = append(args, unixSeconds(*query.To))
		}
		parts = append(parts, b.selectFrom+" WHERE "+strings.Join(filters, " AND "))
	}
	args = append(args, query.PageSize, generation.PageOffset(query.Page, query.PageSize))
	return strings.Join(parts, " UNION ALL ") + " ORDER BY created_unix DESC, task_id DESC LIMIT ? OFFSET ?", args
}
