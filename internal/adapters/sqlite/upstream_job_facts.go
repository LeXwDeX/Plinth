package sqlite

import (
	"context"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

var _ generation.UpstreamJobFactsReader = (*Repository)(nil)

// UpstreamJobFacts reads, for owner's listed Tasks, the facts the upstream
// job age rule needs (#326), from records the kernel already keeps:
//   - a #295 refetch Task is delivered by its failed source's upstream job, so
//     its job is as old as that source Task;
//   - a later Task of the owner that failed as source_expired while its
//     frozen input named the Task means the Provider no longer keeps the job.
func (r *Repository) UpstreamJobFacts(ctx context.Context, owner generation.OwnerID, ids []generation.TaskID) (map[generation.TaskID]generation.UpstreamJobFacts, error) {
	facts := map[generation.TaskID]generation.UpstreamJobFacts{}
	if owner == "" || len(ids) == 0 {
		return facts, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []any{owner}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT refetch.task_id, source.created_at
		FROM generation_upstream_refetches refetch JOIN generation_tasks source ON source.id = refetch.source_task_id
		WHERE refetch.owner_id = ? AND refetch.task_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, created string
		if err := rows.Scan(&id, &created); err != nil {
			_ = rows.Close()
			return nil, err
		}
		at, err := parseTime(created)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		fact := facts[generation.TaskID(id)]
		fact.CreatedAt = at
		facts[generation.TaskID(id)] = fact
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	gone, err := r.db.QueryContext(ctx, `SELECT DISTINCT listed.id FROM generation_tasks listed
		JOIN generation_tasks failed ON failed.owner_id = listed.owner_id
		WHERE listed.owner_id = ? AND listed.id IN (`+placeholders+`)
		AND failed.state = 'FAILED' AND failed.failure_json LIKE '%"source_expired"%'
		AND json_extract(failed.failure_json, '$.class') = 'source_expired'
		AND instr(failed.input_json, '"' || listed.id || '"') > 0`, args...)
	if err != nil {
		return nil, err
	}
	defer gone.Close()
	for gone.Next() {
		var id string
		if err := gone.Scan(&id); err != nil {
			return nil, err
		}
		fact := facts[generation.TaskID(id)]
		fact.Gone = true
		facts[generation.TaskID(id)] = fact
	}
	return facts, gone.Err()
}
