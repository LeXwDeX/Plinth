package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

// skillRunStepSeed describes one step of a SkillRun to seed: the phase it is in,
// the state of the Task it is bound to ("" for none), the status of its grant
// ("" for none) and, for a bound grant the step does not name yet (the lost
// response or failed binding recovery state, #430), the state of the Task only
// the grant names.
type skillRunStepSeed struct {
	phase          skillrun.StepPhase
	taskState      generation.TaskState
	grant          executiongrant.Status
	grantTaskState generation.TaskState
}

// seedSkillRun gives the session one SkillRun with the given steps. Every Task
// is linked to the session, as a Chat-submitted Task is, and keeps its row
// values so a test can prove a deletion never rewrote them.
func seedSkillRun(t *testing.T, r *Repository, ownerID, sessionID, runID string, steps ...skillRunStepSeed) {
	t.Helper()
	const stamp = "2026-10-22T00:00:00Z"
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := r.db.Exec(query, args...); err != nil {
			t.Fatalf("seed %s: %v", runID, err)
		}
	}
	exec(`INSERT INTO skill_runs (id, owner_id, session_id, recipe_id, recipe_revision, recipe_hash,
		selected_offering_id, source_asset_id, idempotency_key, request_hash, created_at, updated_at)
		VALUES (?, ?, ?, 'recipe', 'rev', 'hash', 'offering', 'asset', ?, 'request', ?, ?)`,
		runID, ownerID, sessionID, "key-"+runID, stamp, stamp)
	insertTask := func(id string, state generation.TaskState) {
		t.Helper()
		exec(`INSERT INTO generation_tasks (id, owner_id, capability, offering_json, input_json, request_hash,
			idempotency_key, state, version, created_at, updated_at, session_id)
			VALUES (?, ?, 'image-to-image', '{}', '{}', ?, ?, ?, 7, ?, ?, ?)`,
			id, ownerID, "hash-"+id, "task-key-"+id, string(state), stamp, stamp, sessionID)
	}
	for ordinal, step := range steps {
		key := fmt.Sprintf("%s-step-%d", runID, ordinal)
		// The table's checks tie each phase to the identities it must carry.
		var preparedID, preparedHash, grantID any
		if step.phase != skillrun.Planned {
			preparedID, preparedHash = "prepared-"+key, "prepared-hash"
		}
		if step.phase == skillrun.GrantPending || step.phase == skillrun.TaskBound {
			grantID = "grant-" + key
		}
		var taskID any
		if step.taskState != "" {
			taskID = "task-" + key
			insertTask(taskID.(string), step.taskState)
		}
		if step.grant != "" {
			var boundTask any
			if step.grant == executiongrant.Bound {
				// A step that has not bound its Task yet points only its grant
				// at it: the submission was accepted but its response or the
				// step's binding was lost.
				boundTask = taskID
				if boundTask == nil {
					boundTask = "grant-task-" + key
					insertTask(boundTask.(string), step.grantTaskState)
				}
			}
			exec(`INSERT INTO execution_grants (request_id, owner_id, session_id, offering_id, offering_revision,
				prepared_input_id, prepared_input_hash, idempotency_key, operation_id, status, task_id, source,
				request_version, created_at, updated_at)
				VALUES (?, ?, ?, 'offering', 'rev', ?, 'prepared-hash', ?, ?, ?, ?, 'human_browser', 1, ?, ?)`,
				"grant-"+key, ownerID, sessionID, "prepared-"+key, "grant-key-"+key, "operation-grant-"+key,
				string(step.grant), boundTask, stamp, stamp)
		}
		exec(`INSERT INTO skill_run_steps (run_id, ordinal, step_key, spec_json, phase, operation_id,
			idempotency_key, plan_step_id, prepared_input_id, prepared_input_hash, grant_request_id, task_id, review)
			VALUES (?, ?, ?, '{}', ?, ?, ?, ?, ?, ?, ?, ?, 'unreviewed')`,
			runID, ordinal, key, string(step.phase), "operation-"+key, "step-key-"+key, "plan-"+key,
			preparedID, preparedHash, grantID, taskID)
	}
}

func countSkillRows(t *testing.T, r *Repository, sessionID string) (runs, steps int) {
	t.Helper()
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM skill_runs WHERE session_id = ?`, sessionID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM skill_run_steps WHERE run_id IN
		(SELECT id FROM skill_runs WHERE session_id = ?)`, sessionID).Scan(&steps); err != nil {
		t.Fatal(err)
	}
	return runs, steps
}

// A session with a SkillRun step that has work in flight (a grant waiting for
// its decision, a bound grant whose Task has not ended, or a Task that has not
// ended) is refused, and nothing is lost by it. A paid submission whose
// response or step binding was lost keeps its run while that Task runs: the
// grant names it and recovery must be able to record the result (#384, #430).
// The bulk rows are deleted by transactions of their own, so the refusal has to
// come before the first of them: a refused deletion changes nothing.
func TestDeleteSessionWithASkillRunInFlightIsRefusedAndLosesNothing(t *testing.T) {
	for name, step := range map[string]skillRunStepSeed{
		"a Task still running": {phase: skillrun.TaskBound, taskState: generation.TaskRunning},
		"a Task still queued":  {phase: skillrun.TaskBound, taskState: generation.TaskQueued},
		"a grant waiting for you": {
			phase: skillrun.GrantPending, grant: executiongrant.Pending},
		"a paid Task whose step binding was lost": {
			phase: skillrun.GrantPending, grant: executiongrant.Bound, grantTaskState: generation.TaskQueued},
		"a paid Task whose step binding was lost, now running": {
			phase: skillrun.GrantPending, grant: executiongrant.Bound, grantTaskState: generation.TaskRunning},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := openTestRepository(filepath.Join(t.TempDir(), "chat.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			seedBulkChatSession(t, r, "owner-a", "kept", 30, 100)
			seedSkillRun(t, r, "owner-a", "kept", "run-1", skillRunStepSeed{phase: skillrun.Planned}, step)
			r.deleteBatchRows = 10
			batches := 0
			r.afterDeleteBatch = func() { batches++ }

			if err := r.PreflightSessionDeletion(context.Background(), "owner-a", "kept"); !errors.Is(err, chat.ErrSessionDeletionBlocked) {
				t.Fatalf("PreflightSessionDeletion = %v, want ErrSessionDeletionBlocked", err)
			}
			deleted, err := r.DeleteSession(context.Background(), "owner-a", "kept")
			if deleted || !errors.Is(err, chat.ErrSessionDeletionBlocked) {
				t.Fatalf("DeleteSession = %v, %v, want a refusal with ErrSessionDeletionBlocked", deleted, err)
			}
			if batches != 0 {
				t.Fatalf("a refused deletion drained %d batches of rows first", batches)
			}
			rows := countSessionRows(t, r, "kept")
			if rows["sessions"] != 1 || rows["messages"] != 30 || rows["turns"] != 30 || rows["events"] != 100 {
				t.Fatalf("a refused deletion changed the session: %v", rows)
			}
			if runs, steps := countSkillRows(t, r, "kept"); runs != 1 || steps != 2 {
				t.Fatalf("a refused deletion changed the SkillRun: %d runs, %d steps", runs, steps)
			}
		})
	}
}

// A session whose SkillRuns are all at rest is deleted with them: their steps
// and runs go in the final transaction, before the session row, and the Tasks
// and assets the steps pointed at are left as they were, except for the session
// link the foreign key clears. A Task's state is the Generation kernel's alone.
// An approved grant and a bound grant whose Task already ended are at rest: an
// approval has no Task and the ended Task has no generation work in flight, so
// deleting the session is the person's choice (#430).
func TestDeleteSessionTakesItsRestingSkillRunsWithIt(t *testing.T) {
	r, err := openTestRepository(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	seedBulkChatSession(t, r, "owner-a", "done", 30, 100)
	seedSkillRun(t, r, "owner-a", "done", "run-done",
		skillRunStepSeed{phase: skillrun.TaskBound, taskState: generation.TaskSucceeded},
		skillRunStepSeed{phase: skillrun.TaskBound, taskState: generation.TaskFailed},
		skillRunStepSeed{phase: skillrun.TaskBound, taskState: generation.TaskCanceled},
		skillRunStepSeed{phase: skillrun.GrantPending, grant: executiongrant.Denied},
		// A grant approved by a turn that was stopped before submitting it.
		skillRunStepSeed{phase: skillrun.GrantPending, grant: executiongrant.Approved},
		// A submission whose step binding was lost but whose Task already
		// ended: the run can record the result, but nothing is still running.
		skillRunStepSeed{phase: skillrun.GrantPending, grant: executiongrant.Bound, grantTaskState: generation.TaskSucceeded},
		skillRunStepSeed{phase: skillrun.GrantPending, grant: executiongrant.Bound, grantTaskState: generation.TaskFailed},
		skillRunStepSeed{phase: skillrun.GrantPending, grant: executiongrant.Bound, grantTaskState: generation.TaskCanceled},
		skillRunStepSeed{phase: skillrun.Prepared},
		skillRunStepSeed{phase: skillrun.Planned},
	)
	// Another session's SkillRun, even one in flight, is not touched.
	seedBulkChatSession(t, r, "owner-a", "other", 3, 3)
	seedSkillRun(t, r, "owner-a", "other", "run-other", skillRunStepSeed{phase: skillrun.TaskBound, taskState: generation.TaskRunning})
	type taskRow struct {
		State   string
		Version int
		Updated string
	}
	tasks := func() map[string]taskRow {
		rows, err := r.db.Query(`SELECT id, state, version, updated_at FROM generation_tasks ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := map[string]taskRow{}
		for rows.Next() {
			var id string
			var row taskRow
			if err := rows.Scan(&id, &row.State, &row.Version, &row.Updated); err != nil {
				t.Fatal(err)
			}
			result[id] = row
		}
		return result
	}
	before := tasks()
	r.deleteBatchRows = 10

	if err := r.PreflightSessionDeletion(context.Background(), "owner-a", "done"); err != nil {
		t.Fatalf("PreflightSessionDeletion of a session with resting SkillRuns = %v", err)
	}
	deleted, err := r.DeleteSession(context.Background(), "owner-a", "done")
	if err != nil || !deleted {
		t.Fatalf("DeleteSession = %v, %v", deleted, err)
	}
	for table, count := range countSessionRows(t, r, "done") {
		if count != 0 {
			t.Errorf("%s rows of the deleted session = %d, want 0", table, count)
		}
	}
	if runs, steps := countSkillRows(t, r, "done"); runs != 0 || steps != 0 {
		t.Fatalf("SkillRun rows of the deleted session: %d runs, %d steps, want none", runs, steps)
	}
	if runs, steps := countSkillRows(t, r, "other"); runs != 1 || steps != 1 {
		t.Fatalf("another session's SkillRun was touched: %d runs, %d steps", runs, steps)
	}
	after := tasks()
	if len(after) != len(before) {
		t.Fatalf("Tasks after the deletion = %d, before = %d: a deletion never removes a Task", len(after), len(before))
	}
	for id, was := range before {
		if after[id] != was {
			t.Errorf("Task %s changed: %+v -> %+v; a session deletion never rewrites a Task", id, was, after[id])
		}
	}
	var linked int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM generation_tasks WHERE session_id = 'done'`).Scan(&linked); err != nil || linked != 0 {
		t.Fatalf("Tasks still linked to the deleted session = %d, %v", linked, err)
	}
}

// The repository's query and skillrun.Step.InFlight are one rule. Every step
// that can be written is seeded and the preflight must agree with InFlight on
// whether the session is refused.
func TestPreflightSessionDeletionAgreesWithSkillStepInFlight(t *testing.T) {
	r, err := openTestRepository(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cases := []skillRunStepSeed{
		{skillrun.Planned, "", "", ""},
		{skillrun.Prepared, "", "", ""},
		{skillrun.GrantPending, "", executiongrant.Pending, ""},
		{skillrun.GrantPending, "", executiongrant.Denied, ""},
		{skillrun.GrantPending, "", executiongrant.Approved, ""},
		// A paid submission whose response or step binding was lost: the grant
		// is bound and names the Task while the step still waits for it.
		{skillrun.GrantPending, "", executiongrant.Bound, generation.TaskQueued},
		{skillrun.GrantPending, "", executiongrant.Bound, generation.TaskRunning},
		{skillrun.GrantPending, "", executiongrant.Bound, generation.TaskMaterializing},
		{skillrun.GrantPending, "", executiongrant.Bound, generation.TaskSucceeded},
		{skillrun.GrantPending, "", executiongrant.Bound, generation.TaskFailed},
		{skillrun.GrantPending, "", executiongrant.Bound, generation.TaskCanceled},
		{skillrun.TaskBound, generation.TaskQueued, "", ""},
		{skillrun.TaskBound, generation.TaskRunning, "", ""},
		{skillrun.TaskBound, generation.TaskMaterializing, "", ""},
		{skillrun.TaskBound, generation.TaskSucceeded, "", ""},
		{skillrun.TaskBound, generation.TaskFailed, "", ""},
		{skillrun.TaskBound, generation.TaskCanceled, "", ""},
		{skillrun.TaskBound, generation.TaskRunning, executiongrant.Bound, ""},
		{skillrun.TaskBound, generation.TaskFailed, executiongrant.Bound, ""},
	}
	for index, step := range cases {
		session := fmt.Sprintf("session-%d", index)
		seedBulkChatSession(t, r, "owner-a", session, 1, 1)
		seedSkillRun(t, r, "owner-a", session, fmt.Sprintf("run-%d", index), step)
		want := skillrun.Step{Phase: step.phase}.InFlight(step.taskState, step.grant, step.grantTaskState)
		err := r.PreflightSessionDeletion(context.Background(), "owner-a", session)
		switch {
		case want && !errors.Is(err, chat.ErrSessionDeletionBlocked):
			t.Errorf("%+v is in flight but the preflight answered %v", step, err)
		case !want && err != nil:
			t.Errorf("%+v is at rest but the preflight answered %v", step, err)
		}
	}
}

// insertRunningTask adds a running Task linked to the session, as a paid
// submission accepted by another request would.
func insertRunningTask(t *testing.T, r *Repository, ownerID, sessionID, taskID string) {
	t.Helper()
	const stamp = "2026-10-22T00:00:00Z"
	if _, err := r.db.Exec(`INSERT INTO generation_tasks (id, owner_id, capability, offering_json, input_json,
		request_hash, idempotency_key, state, version, created_at, updated_at, session_id)
		VALUES (?, ?, 'image-to-image', '{}', '{}', ?, ?, 'RUNNING', 1, ?, ?, ?)`,
		taskID, ownerID, "hash-"+taskID, "task-key-"+taskID, stamp, stamp, sessionID); err != nil {
		t.Fatalf("insert Task %s: %v", taskID, err)
	}
}

// advanceGrantToRunningTask simulates a paid submission accepted after the
// deletion preflight: the step's approved grant becomes bound to a Task that is
// still running.
func advanceGrantToRunningTask(t *testing.T, r *Repository, ownerID, sessionID, grantID, taskID string) {
	t.Helper()
	insertRunningTask(t, r, ownerID, sessionID, taskID)
	if _, err := r.db.Exec(`UPDATE execution_grants SET status='bound', task_id=?
		WHERE request_id=? AND owner_id=?`, taskID, grantID, ownerID); err != nil {
		t.Fatalf("advance %s: bind Task: %v", grantID, err)
	}
}

// advanceStepToRunningTask binds a prepared step to a Task that another request
// has just submitted; the step itself did not know it before this write.
func advanceStepToRunningTask(t *testing.T, r *Repository, ownerID, sessionID, runID, stepKey, taskID string) {
	t.Helper()
	insertRunningTask(t, r, ownerID, sessionID, taskID)
	if _, err := r.db.Exec(`UPDATE skill_run_steps SET phase='task_bound', task_id=?, grant_request_id=?
		WHERE run_id=? AND step_key=?`, taskID, "grant-"+stepKey, runID, stepKey); err != nil {
		t.Fatalf("advance %s/%s: bind Task: %v", runID, stepKey, err)
	}
}

// An approved grant has no Task and no terminal or expiry transition: counting
// it as in flight would make a session whose approving turn was stopped before
// submitting undeletable forever. The turn is done here, and the deletion takes
// the run with it (#430).
func TestDeleteSessionTakesAnApprovedGrantWhoseTurnWasStopped(t *testing.T) {
	r, err := openTestRepository(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx := context.Background()
	seedBulkChatSession(t, r, "owner-a", "approved", 2, 2)
	seedSkillRun(t, r, "owner-a", "approved", "run-approved",
		skillRunStepSeed{phase: skillrun.GrantPending, grant: executiongrant.Approved})
	if err := r.PreflightSessionDeletion(ctx, "owner-a", "approved"); err != nil {
		t.Fatalf("PreflightSessionDeletion of an approved grant = %v", err)
	}
	deleted, err := r.DeleteSession(ctx, "owner-a", "approved")
	if err != nil || !deleted {
		t.Fatalf("DeleteSession = %v, %v, want the stopped approved run deleted", deleted, err)
	}
	for table, count := range countSessionRows(t, r, "approved") {
		if count != 0 {
			t.Errorf("%s rows of the deleted session = %d, want 0", table, count)
		}
	}
	if runs, steps := countSkillRows(t, r, "approved"); runs != 0 || steps != 0 {
		t.Fatalf("SkillRun rows of the deleted session: %d runs, %d steps, want none", runs, steps)
	}
}

// A step advanced between the preflight and the rows (a paid submission
// accepted while the turn was stopped) refuses the deletion before any bulk
// row is drained, and the session keeps its rows and SkillRun (#430).
func TestDeleteSessionRefusesAStepAdvancedAfterThePreflight(t *testing.T) {
	r, err := openTestRepository(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx := context.Background()
	seedBulkChatSession(t, r, "owner-a", "kept", 30, 100)
	seedSkillRun(t, r, "owner-a", "kept", "run-kept",
		skillRunStepSeed{phase: skillrun.GrantPending, grant: executiongrant.Approved})
	r.deleteBatchRows = 10
	batches := 0
	r.afterDeleteBatch = func() { batches++ }

	if err := r.PreflightSessionDeletion(ctx, "owner-a", "kept"); err != nil {
		t.Fatalf("PreflightSessionDeletion before the advance = %v", err)
	}
	advanceGrantToRunningTask(t, r, "owner-a", "kept", "grant-run-kept-step-0", "task-late")
	deleted, err := r.DeleteSession(ctx, "owner-a", "kept")
	if deleted || !errors.Is(err, chat.ErrSessionDeletionBlocked) {
		t.Fatalf("DeleteSession = %v, %v, want a refusal with ErrSessionDeletionBlocked", deleted, err)
	}
	if batches != 0 {
		t.Fatalf("a refused deletion drained %d batches of rows first", batches)
	}
	rows := countSessionRows(t, r, "kept")
	if rows["sessions"] != 1 || rows["messages"] != 30 || rows["turns"] != 30 || rows["events"] != 100 {
		t.Fatalf("a refused deletion changed the session: %v", rows)
	}
	if runs, steps := countSkillRows(t, r, "kept"); runs != 1 || steps != 1 {
		t.Fatalf("a refused deletion changed the SkillRun: %d runs, %d steps", runs, steps)
	}
}

// A submission can be accepted after the preflight and after the drain began;
// the final transaction checks the same condition again, rolls back and refuses
// the deletion, so the session and its SkillRun stay even then (#430).
func TestDeleteSessionRechecksSkillRunsInsideTheFinalTransaction(t *testing.T) {
	r, err := openTestRepository(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx := context.Background()
	// One event gives the drain a batch to commit; no messages means the turns
	// and the interrupted conversation are not touched before the recheck.
	seedBulkChatSession(t, r, "owner-a", "late", 0, 1)
	seedSkillRun(t, r, "owner-a", "late", "run-late", skillRunStepSeed{phase: skillrun.Prepared})
	advanced := false
	r.afterDeleteBatch = func() {
		if advanced {
			return
		}
		advanced = true
		advanceStepToRunningTask(t, r, "owner-a", "late", "run-late", "run-late-step-0", "task-late")
	}
	deleted, err := r.DeleteSession(ctx, "owner-a", "late")
	if !advanced {
		t.Fatal("the drain never ran; the test did not exercise the in-transaction recheck")
	}
	if deleted || !errors.Is(err, chat.ErrSessionDeletionBlocked) {
		t.Fatalf("DeleteSession = %v, %v, want a refusal with ErrSessionDeletionBlocked", deleted, err)
	}
	if rows := countSessionRows(t, r, "late"); rows["sessions"] != 1 {
		t.Fatalf("the session did not survive the rolled-back transaction: %v", rows)
	}
	if runs, steps := countSkillRows(t, r, "late"); runs != 1 || steps != 1 {
		t.Fatalf("the SkillRun did not survive the rolled-back transaction: %d runs, %d steps", runs, steps)
	}
}

// Deleting the canvas project that owns a session deletes the session with it,
// and that is a deletion of the session like any other (#384): its bulk rows go
// in short transactions that free the connection, and a SkillRun with work in
// flight refuses it before anything is deleted, the project included.
func TestDeleteProjectAndSessionFollowsTheSessionDeletionRules(t *testing.T) {
	r, err := openTestRepository(filepath.Join(t.TempDir(), "canvas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx := context.Background()
	seedBulkChatSession(t, r, "owner-a", "busy", 30, 100)
	seedOwningCanvasProject(t, r, "owner-a", "project-busy", "busy")
	seedSkillRun(t, r, "owner-a", "busy", "run-busy", skillRunStepSeed{phase: skillrun.TaskBound, taskState: generation.TaskRunning})
	seedBulkChatSession(t, r, "owner-a", "owned", 30, 100)
	seedOwningCanvasProject(t, r, "owner-a", "project-owned", "owned")
	seedSkillRun(t, r, "owner-a", "owned", "run-owned", skillRunStepSeed{phase: skillrun.TaskBound, taskState: generation.TaskSucceeded})
	r.deleteBatchRows = 10
	batches := 0
	r.afterDeleteBatch = func() {
		batches++
		readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if owned, err := r.OwnsSession(readCtx, "owner-a", "busy"); err != nil || !owned {
			t.Errorf("read between deletion batches = %v, %v; the connection was not free", owned, err)
		}
	}
	projects := func() int {
		var count int
		if err := r.db.QueryRow(`SELECT COUNT(*) FROM go_canvas_projects`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	deleted, err := r.DeleteProjectAndSession(ctx, "owner-a", "project-busy")
	if deleted || !errors.Is(err, chat.ErrSessionDeletionBlocked) {
		t.Fatalf("DeleteProjectAndSession with work in flight = %v, %v, want a refusal", deleted, err)
	}
	if batches != 0 || projects() != 2 {
		t.Fatalf("a refused deletion drained %d batches and left %d projects", batches, projects())
	}
	if rows := countSessionRows(t, r, "busy"); rows["sessions"] != 1 || rows["messages"] != 30 || rows["events"] != 100 {
		t.Fatalf("a refused deletion changed the session: %v", rows)
	}

	deleted, err = r.DeleteProjectAndSession(ctx, "owner-a", "project-owned")
	if err != nil || !deleted {
		t.Fatalf("DeleteProjectAndSession = %v, %v", deleted, err)
	}
	if batches < 4 {
		t.Fatalf("the owned session was deleted in %d batches, want it split into short transactions", batches)
	}
	for table, count := range countSessionRows(t, r, "owned") {
		if count != 0 {
			t.Errorf("%s rows of the deleted session = %d, want 0", table, count)
		}
	}
	if runs, steps := countSkillRows(t, r, "owned"); runs != 0 || steps != 0 {
		t.Fatalf("SkillRun rows of the deleted session: %d runs, %d steps", runs, steps)
	}
	if projects() != 1 {
		t.Fatalf("projects after the deletion = %d, want only the refused one", projects())
	}
}

// The canvas path rechecks inside its transaction too: a submission accepted
// after the project deletion's preflight and during the drain refuses the
// deletion and rolls back, keeping the project, the session and the SkillRun
// (#430).
func TestDeleteProjectAndSessionRechecksSkillRunsInsideTheFinalTransaction(t *testing.T) {
	r, err := openTestRepository(filepath.Join(t.TempDir(), "canvas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx := context.Background()
	seedBulkChatSession(t, r, "owner-a", "late", 0, 1)
	seedOwningCanvasProject(t, r, "owner-a", "project-late", "late")
	seedSkillRun(t, r, "owner-a", "late", "run-late", skillRunStepSeed{phase: skillrun.Prepared})
	advanced := false
	r.afterDeleteBatch = func() {
		if advanced {
			return
		}
		advanced = true
		advanceStepToRunningTask(t, r, "owner-a", "late", "run-late", "run-late-step-0", "task-late")
	}
	deleted, err := r.DeleteProjectAndSession(ctx, "owner-a", "project-late")
	if !advanced {
		t.Fatal("the drain never ran; the test did not exercise the in-transaction recheck")
	}
	if deleted || !errors.Is(err, chat.ErrSessionDeletionBlocked) {
		t.Fatalf("DeleteProjectAndSession = %v, %v, want a refusal with ErrSessionDeletionBlocked", deleted, err)
	}
	var projects int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM go_canvas_projects WHERE id = 'project-late'`).Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if projects != 1 {
		t.Fatalf("the refused project was removed by the rolled-back transaction")
	}
	if rows := countSessionRows(t, r, "late"); rows["sessions"] != 1 {
		t.Fatalf("the session did not survive the rolled-back transaction: %v", rows)
	}
	if runs, steps := countSkillRows(t, r, "late"); runs != 1 || steps != 1 {
		t.Fatalf("the SkillRun did not survive the rolled-back transaction: %d runs, %d steps", runs, steps)
	}
}
