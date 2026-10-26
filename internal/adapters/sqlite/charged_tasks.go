package sqlite

import "github.com/LeXwDeX/Plinth/internal/generation"

// taskMayBeChargedSQL is the one rule (#324, #327) for which Tasks an
// estimate of spent credits counts — the session total and the administration
// and account usage statistics alike: queued, running and succeeded Tasks,
// and failed or canceled ones whose upstream finished (the provider produced
// and billed a result that was then not stored) or whose submission is
// ambiguous (the provider may have accepted and charged it). A Task that
// failed or was canceled before its upstream finished was not charged.
//
// A locally completed conversion Task (#330 third follow-ups) is SUCCEEDED but
// keeps its original charge fact: its completion Attempt is recognized by
// delivery_completion_json, and the ordinary "succeeded counts" branch is
// skipped. It then counts only if an original Attempt reached COMPLETED or the
// submission was ambiguous, exactly as the failed Task did. A provider that
// never ran therefore stays free, while a billed failed run stays counted.
// The ambiguity can live only on the original Attempt: a historical or imported
// completion may already have cleared task.failure_json.
//
// task is the SQL alias of a generation_tasks row; the states and phase are
// the generation package's constants, never caller input.
func taskMayBeChargedSQL(task string) string {
	return `((` + task + `.state NOT IN ('` + string(generation.TaskFailed) + `', '` + string(generation.TaskCanceled) + `')
		AND NOT EXISTS (SELECT 1 FROM generation_attempts AS completion_attempt
			WHERE completion_attempt.task_id = ` + task + `.id
			  AND completion_attempt.delivery_completion_json IS NOT NULL))
		OR EXISTS (SELECT 1 FROM generation_attempts AS charged_attempt
			WHERE charged_attempt.task_id = ` + task + `.id AND charged_attempt.last_observation_phase = '` + string(generation.UpstreamCompleted) + `')
		OR EXISTS (SELECT 1 FROM generation_attempts AS ambiguous_attempt
			WHERE ambiguous_attempt.task_id = ` + task + `.id
			  AND COALESCE(CASE WHEN json_valid(ambiguous_attempt.failure_json) THEN json_extract(ambiguous_attempt.failure_json, '$.ambiguous') END, 0) = 1)
		OR COALESCE(CASE WHEN json_valid(` + task + `.failure_json) THEN json_extract(` + task + `.failure_json, '$.ambiguous') END, 0) = 1)`
}
