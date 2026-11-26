package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

var (
	_ executiongrant.ProductionBudgetStore = (*Repository)(nil)
	_ executiongrant.BudgetGrantStore      = (*Repository)(nil)
	_ executiongrant.BudgetNoticeStore     = (*Repository)(nil)
)

const productionBudgetSelect = `SELECT id, owner_id, session_id, plan_json, limits_json,
 state, created_at, decided_at, decided_by, message_id, call_id FROM production_budgets`

func scanProductionBudget(row interface{ Scan(...any) error }) (budget.ProductionBudget, error) {
	var value budget.ProductionBudget
	var planJSON, limitsJSON, created string
	var decidedAt sql.NullString
	err := row.Scan(&value.ID, &value.OwnerID, &value.SessionID, &planJSON,
		&limitsJSON, &value.State, &created, &decidedAt, &value.DecidedBy,
		&value.MessageID, &value.CallID)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal([]byte(planJSON), &value.Plan); err != nil {
		return value, fmt.Errorf("解析 production_budgets.plan_json: %w", err)
	}
	var limits []budget.Amount
	if err := json.Unmarshal([]byte(limitsJSON), &limits); err != nil {
		return value, fmt.Errorf("解析 production_budgets.limits_json: %w", err)
	}
	if value.Limits, err = budget.NormalizeLimits(limits); err != nil {
		return value, fmt.Errorf("production_budgets.limits_json 无效: %w", err)
	}
	value.CreatedAt, err = parseTime(created)
	if err != nil {
		return value, err
	}
	if decidedAt.Valid {
		when, err := parseTime(decidedAt.String)
		if err != nil {
			return value, err
		}
		value.DecidedAt = &when
	}
	return value, nil
}

func productionBudgetByID(ctx context.Context, conn queryer, owner, budgetID string) (budget.ProductionBudget, error) {
	value, err := scanProductionBudget(conn.QueryRowContext(ctx,
		productionBudgetSelect+` WHERE id = ? AND owner_id = ?`, budgetID, owner))
	if errors.Is(err, sql.ErrNoRows) {
		return budget.ProductionBudget{}, budget.ErrNotFound
	}
	return value, err
}

// budgetTotalsSelect totals reservations per plan and unit; amounts of
// different units are never summed together (#120).
const budgetTotalsSelect = `SELECT budget_id, unit,
 COALESCE(SUM(CASE WHEN state = 'charged' THEN amount_micros END), 0),
 COALESCE(SUM(CASE WHEN state = 'reserved' THEN amount_micros END), 0)
 FROM budget_reservations`

// scanBudgetTotals reads budgetTotalsSelect rows into totals by plan.
func scanBudgetTotals(rows *sql.Rows, into map[string]budget.Totals) error {
	defer rows.Close()
	for rows.Next() {
		var budgetID, unit string
		var spent, reserved int64
		if err := rows.Scan(&budgetID, &unit, &spent, &reserved); err != nil {
			return err
		}
		if into[budgetID] == nil {
			into[budgetID] = budget.Totals{}
		}
		into[budgetID][unit] = budget.UnitTotals{Spent: spent, Reserved: reserved}
	}
	return rows.Err()
}

func budgetTotals(ctx context.Context, conn queryer, budgetID string) (budget.Totals, error) {
	rows, err := conn.QueryContext(ctx, budgetTotalsSelect+` WHERE budget_id = ? GROUP BY budget_id, unit`, budgetID)
	if err != nil {
		return nil, err
	}
	totals := map[string]budget.Totals{}
	if err := scanBudgetTotals(rows, totals); err != nil {
		return nil, err
	}
	return totals[budgetID], nil
}

func budgetSummary(ctx context.Context, conn queryer, value budget.ProductionBudget) (budget.Summary, error) {
	totals, err := budgetTotals(ctx, conn, value.ID)
	if err != nil {
		return budget.Summary{}, err
	}
	return budget.SummaryOf(value, totals), nil
}

// CreateProductionBudget stores a pending plan and closes the session's other
// pending plan in the same transaction. A replay of the same ID returns the
// unchanged stored plan; the same ID with different content is a conflict.
func (r *Repository) CreateProductionBudget(ctx context.Context, value budget.ProductionBudget) (budget.ProductionBudget, error) {
	if value.ID == "" || value.OwnerID == "" || value.SessionID == "" ||
		value.State != budget.StatePending || value.CreatedAt.IsZero() {
		return budget.ProductionBudget{}, executiongrant.ErrInvalid
	}
	limits, err := budget.NormalizeLimits(value.Limits)
	if err != nil {
		return budget.ProductionBudget{}, executiongrant.ErrInvalid
	}
	planJSON, err := json.Marshal(value.Plan)
	if err != nil {
		return budget.ProductionBudget{}, err
	}
	limitsJSON, err := json.Marshal(limits)
	if err != nil {
		return budget.ProductionBudget{}, err
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return budget.ProductionBudget{}, err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackGrantConn(conn)
		}
	}()
	existing, err := scanProductionBudget(conn.QueryRowContext(ctx,
		productionBudgetSelect+` WHERE id = ?`, value.ID))
	if err == nil {
		storedPlan, marshalErr := json.Marshal(existing.Plan)
		if marshalErr != nil {
			return budget.ProductionBudget{}, marshalErr
		}
		storedLimits, marshalErr := json.Marshal(existing.Limits)
		if marshalErr != nil {
			return budget.ProductionBudget{}, marshalErr
		}
		if existing.OwnerID != value.OwnerID || existing.SessionID != value.SessionID ||
			string(storedLimits) != string(limitsJSON) || string(storedPlan) != string(planJSON) {
			return budget.ProductionBudget{}, executiongrant.ErrConflict
		}
		if err := commitGrantConn(ctx, conn); err != nil {
			return budget.ProductionBudget{}, err
		}
		committed = true
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return budget.ProductionBudget{}, err
	}
	now := formatTime(value.CreatedAt)
	if _, err := conn.ExecContext(ctx, `UPDATE production_budgets
 SET state = 'closed', decided_at = ?, decided_by = ?
 WHERE owner_id = ? AND session_id = ? AND state = 'pending'`,
		now, budget.SystemActor, value.OwnerID, value.SessionID); err != nil {
		return budget.ProductionBudget{}, err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO production_budgets
 (id, owner_id, session_id, plan_json, limits_json, state, created_at, decided_at, decided_by, message_id, call_id)
 VALUES (?, ?, ?, ?, ?, 'pending', ?, NULL, '', ?, ?)`,
		value.ID, value.OwnerID, value.SessionID, string(planJSON), string(limitsJSON), now, value.MessageID, value.CallID); err != nil {
		return budget.ProductionBudget{}, err
	}
	stored, err := productionBudgetByID(ctx, conn, value.OwnerID, value.ID)
	if err != nil {
		return budget.ProductionBudget{}, err
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		return budget.ProductionBudget{}, err
	}
	committed = true
	return stored, nil
}

// DecideProductionBudget records the person's decision. Approval closes the
// session's former current plan (active, exhausted or completed, #410) and
// releases the reservations of that plan that can no longer be claimed; a
// reservation of an approved step that may still bind stays reserved until its
// Task ends.
func (r *Repository) DecideProductionBudget(ctx context.Context, ownerID, budgetID, actor string, approve bool, now time.Time) (budget.ProductionBudget, error) {
	if ownerID == "" || budgetID == "" || actor == "" {
		return budget.ProductionBudget{}, executiongrant.ErrInvalid
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return budget.ProductionBudget{}, err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackGrantConn(conn)
		}
	}()
	value, err := productionBudgetByID(ctx, conn, ownerID, budgetID)
	if err != nil {
		return budget.ProductionBudget{}, err
	}
	if value.State != budget.StatePending {
		return budget.ProductionBudget{}, budget.ErrState
	}
	stamp := formatTime(now)
	if approve {
		former, err := productionBudgetsInStates(ctx, conn, ownerID, value.SessionID, budget.CurrentStates()...)
		if err != nil {
			return budget.ProductionBudget{}, err
		}
		for _, old := range former {
			if _, err := conn.ExecContext(ctx, `UPDATE production_budgets
 SET state = 'closed', decided_at = ?, decided_by = ? WHERE id = ? AND state IN ('active','exhausted','completed')`,
				stamp, budget.SystemActor, old.ID); err != nil {
				return budget.ProductionBudget{}, err
			}
			if err := releaseUnclaimableReservationsOnConn(ctx, conn, old.ID, stamp); err != nil {
				return budget.ProductionBudget{}, err
			}
		}
	}
	next := budget.StateDenied
	if approve {
		next = budget.StateActive
	}
	result, err := conn.ExecContext(ctx, `UPDATE production_budgets
 SET state = ?, decided_at = ?, decided_by = ? WHERE id = ? AND owner_id = ? AND state = 'pending'`,
		next, stamp, actor, budgetID, ownerID)
	if err != nil {
		return budget.ProductionBudget{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return budget.ProductionBudget{}, err
	} else if n != 1 {
		return budget.ProductionBudget{}, budget.ErrState
	}
	stored, err := productionBudgetByID(ctx, conn, ownerID, budgetID)
	if err != nil {
		return budget.ProductionBudget{}, err
	}
	if approve {
		// #410: a plan whose steps all succeeded before it was approved is done
		// at once. It is one plan, so this stays a single read; the sweep's
		// bounded scope is what keeps the rest of the plans out of the hot path.
		if _, err := completeFinishedPlansOnConn(ctx, conn, []string{budgetID}, now); err != nil {
			return budget.ProductionBudget{}, err
		}
		stored, err = productionBudgetByID(ctx, conn, ownerID, budgetID)
		if err != nil {
			return budget.ProductionBudget{}, err
		}
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		return budget.ProductionBudget{}, err
	}
	committed = true
	return stored, nil
}

// abandonUnboundGrantOnConn denies an approved grant whose reservation is still
// unbound and releases that reservation in the same transaction. It is called
// when the operation the grant authorized ends without a Task (a permanently
// rejected submit, or a processing authorization that will never be used), so
// the plan's reserved credits are not held forever (#103 review B2). A grant
// the 3D step mode approved (#330), or a personal access key's policy did
// (#332), is denied the same way: it holds one step of its asset's cap until it
// binds a Task, and an operation that ended without one must give that step
// back. Other grants without a reservation, claimed
// grants (a submit is in flight) and decided grants are left untouched.
func abandonUnboundGrantOnConn(ctx context.Context, conn *sql.Conn, owner, requestID string, now time.Time) error {
	return abandonApprovedGrantOnConn(ctx, conn, owner, requestID, now, false)
}

// abandonApprovedGrantOnConn is abandonUnboundGrantOnConn; with anyApproval
// it denies the approved grant whoever approved it, with or without a
// reservation (#665): the kernel refused it for good, so no later submit can
// use it, and a step holding it must become restartable.
func abandonApprovedGrantOnConn(ctx context.Context, conn *sql.Conn, owner, requestID string, now time.Time, anyApproval bool) error {
	if owner == "" || requestID == "" {
		return nil
	}
	anyFlag := 0
	if anyApproval {
		anyFlag = 1
	}
	stamp := formatTime(now)
	result, err := conn.ExecContext(ctx, `UPDATE execution_grants
 SET status = 'denied', decided_by = ?, decided_at = ?, updated_at = ?
 WHERE request_id = ? AND owner_id = ? AND status = 'approved'
 AND (? = 1 OR decided_by IN (?, ?) OR EXISTS (SELECT 1 FROM budget_reservations
  WHERE budget_reservations.grant_request_id = execution_grants.request_id
  AND budget_reservations.state = 'reserved' AND budget_reservations.task_id = ''))`,
		executiongrant.AbandonedActor, stamp, stamp, requestID, owner, anyFlag, executiongrant.StepsActor, executiongrant.AccessKeyActor)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 0 {
		return err
	}
	grant, err := grantByRequest(ctx, conn, owner, requestID)
	if err != nil {
		return err
	}
	identityJSON, err := json.Marshal(grant.Identity)
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO execution_grant_audit
 (request_id, owner_id, request_version, event, actor_id, identity_json, occurred_at)
 VALUES (?, ?, ?, 'denied', ?, ?, ?)`,
		requestID, owner, grant.RequestVersion, executiongrant.AbandonedActor, string(identityJSON), stamp); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `UPDATE budget_reservations
 SET state = 'released', settled_at = ?
 WHERE grant_request_id = ? AND state = 'reserved' AND task_id = ''`,
		stamp, requestID)
	return err
}

// AbandonRefusedExecutionGrant is the data-layer side of
// executiongrant.Service.AbandonRefusedGrant (#665).
func (r *Repository) AbandonRefusedExecutionGrant(ctx context.Context, owner, requestID string) error {
	if owner == "" || requestID == "" {
		return executiongrant.ErrInvalid
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackGrantConn(conn)
		}
	}()
	if err := abandonApprovedGrantOnConn(ctx, conn, owner, requestID, time.Now().UTC(), true); err != nil {
		return err
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		return err
	}
	committed = true
	return nil
}

// ReleaseUnboundBudgetReservation is the data-layer side of
// executiongrant.Service.AbandonBudgetReservation.
func (r *Repository) ReleaseUnboundBudgetReservation(ctx context.Context, owner, requestID string) error {
	if owner == "" || requestID == "" {
		return executiongrant.ErrInvalid
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackGrantConn(conn)
		}
	}()
	if err := abandonUnboundGrantOnConn(ctx, conn, owner, requestID, time.Now().UTC()); err != nil {
		return err
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		return err
	}
	committed = true
	return nil
}

func productionBudgetsInStates(ctx context.Context, conn queryer, ownerID, sessionID string, states ...string) ([]budget.ProductionBudget, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(states)), ",")
	args := []any{ownerID, sessionID}
	for _, state := range states {
		args = append(args, state)
	}
	rows, err := conn.QueryContext(ctx, productionBudgetSelect+`
 WHERE owner_id = ? AND session_id = ? AND state IN (`+placeholders+`) ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []budget.ProductionBudget
	for rows.Next() {
		value, err := scanProductionBudget(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

// releaseUnclaimableReservationsOnConn releases the unbound reservations of a
// plan whose grant can never become a Task: it was denied, or its trigger turn
// was stopped before the operation detached. A reservation of an approved step
// that may still bind is left for SettleBudgetReservations.
func releaseUnclaimableReservationsOnConn(ctx context.Context, conn *sql.Conn, budgetID, stamp string) error {
	_, err := conn.ExecContext(ctx, `UPDATE budget_reservations
 SET state = 'released', settled_at = ?
 WHERE budget_id = ? AND state = 'reserved' AND task_id = ''
 AND EXISTS (SELECT 1 FROM execution_grants
  WHERE execution_grants.request_id = budget_reservations.grant_request_id
  AND (execution_grants.status = 'denied'
   OR (execution_grants.status = 'approved' AND NOT (`+turnOwnsStoppedGrantNot+`))))`,
		stamp, budgetID)
	return err
}

// settleReservationsOnConn settles the reservations that reached a final
// charge fact: a bound Task that ended (charged or released by
// taskMayBeChargedSQL), and an unbound grant that can no longer spend. A
// non-empty budgetID limits the work to one plan.
func settleReservationsOnConn(ctx context.Context, conn *sql.Conn, budgetID string, now time.Time) error {
	stamp := formatTime(now)
	scope := ""
	args := []any{stamp}
	if budgetID != "" {
		scope = " AND budget_id = ?"
		args = append(args, budgetID)
	}
	// #410: only a plan whose reservation this pass settles can have changed a
	// step's charge fact, so only those plans are reconsidered. The sweep runs
	// every two seconds; re-reading every active plan's Tasks on each tick would
	// also read the sessions of plans that were abandoned long ago, and it would
	// do so while holding the single write connection.
	touched, err := reservedBudgetIDs(ctx, conn, budgetID)
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `UPDATE budget_reservations
 SET state = 'released', settled_at = ?
 WHERE state = 'reserved' AND task_id = ''`+scope+`
 AND EXISTS (SELECT 1 FROM execution_grants
  WHERE execution_grants.request_id = budget_reservations.grant_request_id
  AND (execution_grants.status = 'denied'
   OR (execution_grants.status = 'approved' AND NOT (`+turnOwnsStoppedGrantNot+`))))`, args...); err != nil {
		return err
	}
	args = []any{stamp}
	if budgetID != "" {
		args = append(args, budgetID)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE budget_reservations
 SET state = CASE WHEN EXISTS (
   SELECT 1 FROM generation_tasks task
   WHERE task.id = budget_reservations.task_id AND `+taskMayBeChargedSQL("task")+`
 ) THEN 'charged' ELSE 'released' END,
 settled_at = ?
 WHERE state = 'reserved' AND task_id <> ''`+scope+`
 AND EXISTS (SELECT 1 FROM generation_tasks task
  WHERE task.id = budget_reservations.task_id
  AND task.state IN ('`+string(generation.TaskSucceeded)+`', '`+string(generation.TaskFailed)+`', '`+string(generation.TaskCanceled)+`'))`, args...); err != nil {
		return err
	}
	if len(touched) == 0 {
		return nil
	}
	_, err = completeFinishedPlansOnConn(ctx, conn, touched, now)
	return err
}

// reservedBudgetIDs lists the plans whose reservations a settlement pass may
// move: those still holding a reserved row. A non-empty budgetID narrows the
// read to one plan, which is what the atomic budget approval needs.
func reservedBudgetIDs(ctx context.Context, conn queryer, budgetID string) ([]string, error) {
	query := `SELECT DISTINCT budget_id FROM budget_reservations WHERE state = 'reserved'`
	args := []any{}
	if budgetID != "" {
		query += ` AND budget_id = ?`
		args = append(args, budgetID)
	}
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// planTaskSelect reads the task facts a plan step pairs with: the Offering ID
// comes from the frozen Offering the Task was submitted with, and the state
// from the Task itself. Tasks of other sessions are never read.
const planTaskSelect = `SELECT session_id, id, capability, json_extract(offering_json, '$.id'), state, created_at
 FROM generation_tasks`

// sessionStepTasks reads the Tasks of one session in the shape the plan
// projection pairs. A row that does not scan is skipped rather than failing
// the whole projection: a plan card is a read model, and the caller falls back
// to its own pairing when the result is unusable.
func sessionStepTasks(ctx context.Context, conn queryer, ownerID, sessionID string) []budget.StepTask {
	rows, err := conn.QueryContext(ctx, planTaskSelect+
		` WHERE owner_id = ? AND session_id = ? ORDER BY created_at, id`, ownerID, sessionID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	tasks := make([]budget.StepTask, 0, 8)
	for rows.Next() {
		var (
			sessionID  string
			task       budget.StepTask
			offeringID sql.NullString
			created    string
		)
		if err := rows.Scan(&sessionID, &task.TaskID, &task.Capability, &offeringID, &task.State, &created); err != nil {
			return nil
		}
		task.OfferingID = offeringID.String
		task.Succeeded = task.State == string(generation.TaskSucceeded)
		task.Finished = task.State == string(generation.TaskSucceeded) ||
			task.State == string(generation.TaskFailed) || task.State == string(generation.TaskCanceled)
		if when, err := parseTime(created); err == nil {
			task.CreatedAt = when
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil
	}
	return tasks
}

// completeFinishedPlansOnConn marks a plan whose required steps all have a
// succeeded Task as completed, and gives back the reservations of its steps
// that can no longer bind, the same way a replaced plan does (#410). It is
// idempotent: a plan that is not active or exhausted is left alone, so a
// completed, closed or denied plan never moves again.
//
// budgetIDs names the plans to reconsider, empty for all of them. Callers pass
// the plans a settlement actually touched (or the one plan a decision or a
// continuation concerns); the all-plans form is the one-shot a deployment runs
// at start-up, when a plan that finished while nothing settled must be collected
// exactly once. Nothing here runs on a timer.
func completeFinishedPlansOnConn(ctx context.Context, conn *sql.Conn, budgetIDs []string, now time.Time) ([]budget.ProductionBudget, error) {
	if len(budgetIDs) == 0 {
		return nil, nil
	}
	args := []any{budget.StateActive, budget.StateExhausted}
	query := productionBudgetSelect + ` WHERE state IN (?, ?)`
	query += ` AND id IN (` + queryPlaceholders(len(budgetIDs)) + `)`
	for _, id := range budgetIDs {
		args = append(args, id)
	}
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var plans []budget.ProductionBudget
	for rows.Next() {
		value, err := scanProductionBudget(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		plans = append(plans, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	stamp := formatTime(now)
	var completed []budget.ProductionBudget
	for _, value := range plans {
		// A Task older than the plan's decision did not run one of its steps.
		var decidedAt time.Time
		if value.DecidedAt != nil {
			decidedAt = *value.DecidedAt
		}
		tasks := budget.EligibleStepTasks(
			sessionStepTasks(ctx, conn, value.OwnerID, value.SessionID), decidedAt)
		statuses := budget.PlanStepStatuses(budget.StepStatusInput{
			Plan:      value.Plan,
			PlanState: value.State,
			Tasks:     tasks,
		})
		if !budget.RequiredStepsDone(statuses) {
			continue
		}
		blocked, err := unfinishedSkillRunStep(ctx, conn, value.OwnerID, value.SessionID, plannedCapabilities(value.Plan))
		if err != nil {
			return nil, err
		}
		if blocked {
			continue
		}
		if _, err := conn.ExecContext(ctx, `UPDATE production_budgets
 SET state = '`+budget.StateCompleted+`' WHERE id = ? AND state IN ('`+
			budget.StateActive+`', '`+budget.StateExhausted+`')`, value.ID); err != nil {
			return nil, err
		}
		if err := releaseUnclaimableReservationsOnConn(ctx, conn, value.ID, stamp); err != nil {
			return nil, err
		}
		value.State = budget.StateCompleted
		completed = append(completed, value)
	}
	return completed, nil
}

// unfinishedSkillRunStep reports whether the session has a SkillRun step the
// plan still owes: a step of a capability the plan covers that has not finished
// (planned, prepared, waiting for its grant or bound to a Task) or whose last
// review rejected it (#410 with #243). The Task of a rejected view succeeded, so
// pairing alone would call the plan done while the view must be redrawn — and
// that redraw is spending the person already approved for the view. An optional
// step (a recipe's optional view) and a step the person replaced with their own
// picture hold nothing back.
func unfinishedSkillRunStep(ctx context.Context, conn queryer, ownerID, sessionID string, capabilities []string) (bool, error) {
	if ownerID == "" || sessionID == "" || len(capabilities) == 0 {
		return false, nil
	}
	args := []any{ownerID, sessionID}
	for _, capability := range capabilities {
		args = append(args, capability)
	}
	var pending int
	err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_run_steps step
 JOIN skill_runs run ON run.id = step.run_id
 WHERE run.owner_id = ? AND run.session_id = ?
   AND COALESCE(json_extract(step.spec_json, '$.optional'), 0) = 0
   AND json_extract(step.spec_json, '$.capability') IN (`+queryPlaceholders(len(capabilities))+`)
   AND COALESCE(step.replacement_asset_id, '') = ''
   AND (step.phase <> 'output_ready' OR step.review = 'rejected')`, args...).Scan(&pending)
	if err != nil {
		return false, err
	}
	return pending > 0, nil
}

// plannedCapabilities are the capabilities a plan may spend on: its steps and
// its declared contingencies, without duplicates and in plan order.
func plannedCapabilities(plan budget.Plan) []string {
	capabilities := plan.SortedCapabilities()
	for _, contingency := range plan.Contingencies {
		if contingency.Step.Capability == "" ||
			slices.Contains(capabilities, contingency.Step.Capability) {
			continue
		}
		capabilities = append(capabilities, contingency.Step.Capability)
	}
	return capabilities
}

// completeExhaustedSessionPlanOnConn reconsiders the session's exhausted plan
// when a SkillRun step of that session records its output (#561). The
// settlement judges a plan only when it settles one of its reservations, and a
// step's Task can be charged before its run records the output: the unfinished
// step holds the plan back, the plan turns exhausted, and with nothing left to
// settle it would never be judged again, reading "budget used up" for ever
// although every step succeeded.
//
// Only an exhausted plan is judged here. An active plan still pays for a
// redraw its review may ask for after the output is recorded (#243), so it
// keeps waiting for the settlement; an exhausted one has nothing left to pay
// with, and completing it changes what the card says, not what may spend.
func completeExhaustedSessionPlanOnConn(ctx context.Context, conn *sql.Conn, ownerID, sessionID string, now time.Time) error {
	if ownerID == "" || sessionID == "" {
		return nil
	}
	rows, err := conn.QueryContext(ctx, `SELECT id FROM production_budgets
 WHERE owner_id = ? AND session_id = ? AND state = ?`,
		ownerID, sessionID, budget.StateExhausted)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	_, err = completeFinishedPlansOnConn(ctx, conn, ids, now)
	return err
}

// CompleteFinishedBudgets marks every active or exhausted plan whose required
// steps all have a succeeded Task as completed, and returns the plans it moved
// (#410). It is the one-shot a deployment runs at start-up: a plan whose steps
// all succeeded while nothing settled — its steps were paid without a
// reservation, or the settlement that would have collected it never ran — still
// reads completed afterwards. It is deliberately not called on the sweep's
// timer; see completeFinishedPlansOnConn.
func (r *Repository) CompleteFinishedBudgets(ctx context.Context, now time.Time) ([]budget.ProductionBudget, error) {
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackGrantConn(conn)
		}
	}()
	ids, err := planIDsInCurrentStates(ctx, conn, "")
	if err != nil {
		return nil, err
	}
	completed, err := completeFinishedPlansOnConn(ctx, conn, ids, now)
	if err != nil {
		return nil, err
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		return nil, err
	}
	committed = true
	return completed, nil
}

// CompleteFinishedBudget is CompleteFinishedBudgets for one plan of one owner,
// the scope a plan decision or a continuation turn needs. A plan of another
// owner is not found.
func (r *Repository) CompleteFinishedBudget(ctx context.Context, ownerID, budgetID string, now time.Time) ([]budget.ProductionBudget, error) {
	if ownerID == "" || budgetID == "" {
		return nil, executiongrant.ErrInvalid
	}
	if _, err := productionBudgetByID(ctx, r.db, ownerID, budgetID); err != nil {
		return nil, err
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackGrantConn(conn)
		}
	}()
	completed, err := completeFinishedPlansOnConn(ctx, conn, []string{budgetID}, now)
	if err != nil {
		return nil, err
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		return nil, err
	}
	committed = true
	return completed, nil
}

// planIDsInCurrentStates lists the ids of the plans a session may still show and
// spend under: the active, exhausted and completed ones. An empty budgetID reads
// every owner; the all-plans one-shot is the only caller that does.
func planIDsInCurrentStates(ctx context.Context, conn queryer, budgetID string) ([]string, error) {
	query := `SELECT id FROM production_budgets WHERE state IN (` +
		queryPlaceholders(len(budget.CurrentStates())) + `)`
	args := make([]any, 0, len(budget.CurrentStates())+1)
	for _, state := range budget.CurrentStates() {
		args = append(args, state)
	}
	if budgetID != "" {
		query += ` AND id = ?`
		args = append(args, budgetID)
	}
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SettleBudgetReservations settles ended Tasks' reservations, releases the
// reservations of grants that can no longer spend, marks an active plan
// exhausted once its charged totals reached the limit of every unit, and marks
// a plan completed once every required step has a succeeded Task (#410). It
// returns the plans it marked exhausted in this call, so their notice is
// posted once. A completed plan needs no notice: the card reads its state and
// the session's next turn reads the plan from the same projection.
func (r *Repository) SettleBudgetReservations(ctx context.Context, now time.Time) ([]budget.ProductionBudget, error) {
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackGrantConn(conn)
		}
	}()
	if err := settleReservationsOnConn(ctx, conn, "", now); err != nil {
		return nil, err
	}
	// Only an active plan that charged something can have used up every unit.
	rows, err := conn.QueryContext(ctx, productionBudgetSelect+`
 WHERE state = 'active' AND EXISTS (
  SELECT 1 FROM budget_reservations
  WHERE budget_id = production_budgets.id AND state = 'charged'
 )`)
	if err != nil {
		return nil, err
	}
	var charged []budget.ProductionBudget
	for rows.Next() {
		value, err := scanProductionBudget(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		charged = append(charged, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var exhausted []budget.ProductionBudget
	for _, value := range charged {
		totals, err := budgetTotals(ctx, conn, value.ID)
		if err != nil {
			return nil, err
		}
		if value.ExhaustedBy(totals) {
			exhausted = append(exhausted, value)
		}
	}
	// decided_at stays the person's decision (#561): the step pairing counts only
	// Tasks submitted after it, so moving it to the settlement would drop the
	// plan's own Tasks and read every step as not run.
	for _, value := range exhausted {
		if _, err := conn.ExecContext(ctx, `UPDATE production_budgets
 SET state = 'exhausted' WHERE id = ? AND state = 'active'`, value.ID); err != nil {
			return nil, err
		}
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		return nil, err
	}
	committed = true
	return exhausted, nil
}

func (r *Repository) GetProductionBudget(ctx context.Context, ownerID, budgetID string) (budget.ProductionBudget, error) {
	if ownerID == "" || budgetID == "" {
		return budget.ProductionBudget{}, executiongrant.ErrInvalid
	}
	return productionBudgetByID(ctx, r.db, ownerID, budgetID)
}

func (r *Repository) PendingProductionBudgets(ctx context.Context, ownerID, sessionID string) ([]budget.ProductionBudget, error) {
	if ownerID == "" || sessionID == "" {
		return nil, executiongrant.ErrInvalid
	}
	return productionBudgetsInStates(ctx, r.db, ownerID, sessionID, budget.StatePending)
}

func (r *Repository) CurrentProductionBudget(ctx context.Context, ownerID, sessionID string) (budget.ProductionBudget, bool, error) {
	if ownerID == "" || sessionID == "" {
		return budget.ProductionBudget{}, false, executiongrant.ErrInvalid
	}
	all, err := productionBudgetsInStates(ctx, r.db, ownerID, sessionID, budget.CurrentStates()...)
	if err != nil || len(all) == 0 {
		return budget.ProductionBudget{}, false, err
	}
	return all[0], true, nil
}

func (r *Repository) ProductionBudgetSummary(ctx context.Context, ownerID, sessionID string) (budget.Summary, bool, error) {
	value, found, err := r.CurrentProductionBudget(ctx, ownerID, sessionID)
	if err != nil || !found {
		return budget.Summary{}, found, err
	}
	summary, err := budgetSummary(ctx, r.db, value)
	return summary, err == nil, err
}

// productionBudgetSummaryBatch bounds the IN lists of the list projection:
// SQLite binds one parameter per value and a session list can be long.
const productionBudgetSummaryBatch = 500

// ProductionBudgetSummaries reads the current plan summaries of many sessions
// for the session list: one query selects each session's active/exhausted plan
// (the newest created_at, as ProductionBudgetSummary reads it) and one grouped
// query totals its reservations. A session without a current plan is absent
// from the map, so the projection omits its member instead of claiming a plan.
func (r *Repository) ProductionBudgetSummaries(ctx context.Context, ownerID string, sessionIDs []string) (map[string]budget.Summary, error) {
	summaries := make(map[string]budget.Summary)
	if ownerID == "" || len(sessionIDs) == 0 {
		return summaries, nil
	}
	current := make(map[string]budget.ProductionBudget)
	for start := 0; start < len(sessionIDs); start += productionBudgetSummaryBatch {
		end := min(start+productionBudgetSummaryBatch, len(sessionIDs))
		chunk := sessionIDs[start:end]
		args := make([]any, 0, len(chunk)+3)
		args = append(args, ownerID)
		for _, sessionID := range chunk {
			args = append(args, sessionID)
		}
		for _, state := range budget.CurrentStates() {
			args = append(args, state)
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(budget.CurrentStates())), ",")
		rows, err := r.db.QueryContext(ctx, productionBudgetSelect+`
 WHERE owner_id = ? AND session_id IN (`+queryPlaceholders(len(chunk))+`)
 AND state IN (`+placeholders+`) ORDER BY created_at DESC`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			value, err := scanProductionBudget(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			// The unique current-plan index keeps this to one row per session;
			// ORDER BY created_at DESC keeps the read identical to the
			// single-session method if that ever changes.
			if _, found := current[value.SessionID]; !found {
				current[value.SessionID] = value
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	if len(current) == 0 {
		return summaries, nil
	}
	budgetIDs := make([]string, 0, len(current))
	for _, value := range current {
		budgetIDs = append(budgetIDs, value.ID)
	}
	totals := make(map[string]budget.Totals, len(budgetIDs))
	for start := 0; start < len(budgetIDs); start += productionBudgetSummaryBatch {
		end := min(start+productionBudgetSummaryBatch, len(budgetIDs))
		chunk := budgetIDs[start:end]
		args := make([]any, 0, len(chunk))
		for _, budgetID := range chunk {
			args = append(args, budgetID)
		}
		rows, err := r.db.QueryContext(ctx, budgetTotalsSelect+` WHERE budget_id IN (`+queryPlaceholders(len(chunk))+`)
 GROUP BY budget_id, unit`, args...)
		if err != nil {
			return nil, err
		}
		if err := scanBudgetTotals(rows, totals); err != nil {
			return nil, err
		}
	}
	for sessionID, value := range current {
		summaries[sessionID] = budget.SummaryOf(value, totals[value.ID])
	}
	return summaries, nil
}

func queryPlaceholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func (r *Repository) BudgetSummaryForGrant(ctx context.Context, ownerID, requestID string) (budget.Summary, bool, error) {
	if ownerID == "" || requestID == "" {
		return budget.Summary{}, false, executiongrant.ErrInvalid
	}
	var budgetID string
	err := r.db.QueryRowContext(ctx, `SELECT budget_id FROM budget_reservations
 WHERE grant_request_id = ?`, requestID).Scan(&budgetID)
	if errors.Is(err, sql.ErrNoRows) {
		return budget.Summary{}, false, nil
	}
	if err != nil {
		return budget.Summary{}, false, err
	}
	value, err := productionBudgetByID(ctx, r.db, ownerID, budgetID)
	if err != nil {
		return budget.Summary{}, false, err
	}
	summary, err := budgetSummary(ctx, r.db, value)
	return summary, err == nil, err
}

// ExpireProductionBudgets denies every pending plan that waited past cutoff
// and returns them so the caller posts the expiry message. The cutoff is
// compared in Go: RFC3339Nano text is not byte-ordered by time.
func (r *Repository) ExpireProductionBudgets(ctx context.Context, cutoff time.Time) ([]budget.ProductionBudget, error) {
	if cutoff.IsZero() {
		return nil, nil
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackGrantConn(conn)
		}
	}()
	rows, err := conn.QueryContext(ctx, productionBudgetSelect+`
 WHERE state = 'pending' ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	var expired []budget.ProductionBudget
	for rows.Next() {
		value, err := scanProductionBudget(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if !value.CreatedAt.After(cutoff) {
			expired = append(expired, value)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	stamp := formatTime(time.Now().UTC())
	for _, value := range expired {
		if _, err := conn.ExecContext(ctx, `UPDATE production_budgets
 SET state = 'denied', decided_at = ?, decided_by = ? WHERE id = ? AND state = 'pending'`,
			stamp, budget.SystemActor, value.ID); err != nil {
			return nil, err
		}
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		return nil, err
	}
	committed = true
	return expired, nil
}

// ProductionBudgetsAwaitingNotice lists exhausted plans and plans the system
// expired whose chat notice was never recorded. The sweep posts and marks each
// one, so a post that failed after the state transition committed is retried
// instead of being lost (#103 review B3).
func (r *Repository) ProductionBudgetsAwaitingNotice(ctx context.Context, limit int) ([]budget.ProductionBudget, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, productionBudgetSelect+`
 WHERE notice_sent_at = '' AND (state = 'exhausted' OR (state = 'denied' AND decided_by = ?))
 ORDER BY created_at ASC LIMIT ?`, budget.SystemActor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := make([]budget.ProductionBudget, 0)
	for rows.Next() {
		value, err := scanProductionBudget(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, value)
	}
	return plans, rows.Err()
}

// MarkProductionBudgetNoticeSent records the one notice of an exhausted or
// system-expired plan. A repeated mark is a no-op.
func (r *Repository) MarkProductionBudgetNoticeSent(ctx context.Context, ownerID, budgetID string, now time.Time) error {
	if ownerID == "" || budgetID == "" || now.IsZero() {
		return executiongrant.ErrInvalid
	}
	_, err := r.db.ExecContext(ctx, `UPDATE production_budgets SET notice_sent_at = ?
 WHERE id = ? AND owner_id = ? AND notice_sent_at = ''`, formatTime(now), budgetID, ownerID)
	return err
}

// ApproveExecutionGrantWithBudget is the atomic budget authorization: settle
// what already ended, re-check what remains in the amount's unit, insert the
// reservation and approve the grant in one BEGIN IMMEDIATE transaction. Two
// concurrent steps therefore cannot both take the last credits of a unit. A
// positive amount needs the plan's limit in its own unit; different units are
// never compared or added (#120). A zero amount reserves nothing and fits any
// active plan.
func (r *Repository) ApproveExecutionGrantWithBudget(ctx context.Context, owner, requestID, budgetID, actor string, amount budget.Amount) (executiongrant.Grant, error) {
	if owner == "" || requestID == "" || budgetID == "" || amount.Unit == "" || amount.Micros < 0 ||
		!strings.HasPrefix(actor, executiongrant.BudgetActorPrefix) ||
		len(actor) == len(executiongrant.BudgetActorPrefix) {
		return executiongrant.Grant{}, executiongrant.ErrInvalid
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackGrantConn(conn)
		}
	}()
	now := time.Now().UTC()
	if err := settleReservationsOnConn(ctx, conn, budgetID, now); err != nil {
		return executiongrant.Grant{}, err
	}
	grant, err := grantByRequest(ctx, conn, owner, requestID)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	if grant.Status != executiongrant.Pending {
		if (grant.DecidedBy == actor || grant.ApprovedByBudget()) &&
			(grant.Status == executiongrant.Approved || grant.Status == executiongrant.Claimed || grant.Status == executiongrant.Bound) {
			if err := commitGrantConn(ctx, conn); err != nil {
				return executiongrant.Grant{}, err
			}
			committed = true
			return grant, nil
		}
		return executiongrant.Grant{}, executiongrant.ErrState
	}
	if grant.OwnerID != owner ||
		(grant.Source != executiongrant.AgentPending && grant.Source != executiongrant.HumanBrowser) ||
		!grant.BudgetEligible {
		// An Agent-created processing plan has no chat turn, so its step grants
		// carry the plan's HumanBrowser principal even though the Agent started
		// the plan; the server-set eligibility is what makes it the plan's to
		// approve. A browser-started step is never eligible.
		return executiongrant.Grant{}, executiongrant.ErrConflict
	}
	value, err := productionBudgetByID(ctx, conn, owner, budgetID)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	if value.State != budget.StateActive || value.SessionID != grant.SessionID {
		return executiongrant.Grant{}, budget.ErrInactive
	}
	if amount.Micros > 0 {
		limit, limited := value.Limit(amount.Unit)
		if !limited {
			return executiongrant.Grant{}, budget.ErrOverLimit
		}
		totals, err := budgetTotals(ctx, conn, budgetID)
		if err != nil {
			return executiongrant.Grant{}, err
		}
		settled := totals[amount.Unit]
		if amount.Micros > limit.Micros-settled.Spent-settled.Reserved {
			return executiongrant.Grant{}, budget.ErrOverLimit
		}
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO budget_reservations
 (grant_request_id, budget_id, task_id, unit, amount_micros, state, created_at)
 VALUES (?, ?, '', ?, ?, 'reserved', ?)`,
		requestID, budgetID, amount.Unit, amount.Micros, formatTime(now)); err != nil {
		return executiongrant.Grant{}, err
	}
	stamp := formatTime(now)
	// A stopped turn's own grant is never approved by its plan either (#665).
	result, err := conn.ExecContext(ctx, `UPDATE execution_grants
 SET status = 'approved', decided_by = ?, decided_at = ?, updated_at = ?
 WHERE request_id = ? AND owner_id = ? AND status = 'pending' AND `+turnOwnsStoppedGrantNot,
		actor, stamp, stamp, requestID, owner)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return executiongrant.Grant{}, err
	} else if n != 1 {
		return executiongrant.Grant{}, executiongrant.ErrState
	}
	identityJSON, err := json.Marshal(grant.Identity)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO execution_grant_audit
 (request_id, owner_id, request_version, event, actor_id, identity_json, occurred_at)
 VALUES (?, ?, ?, 'approved', ?, ?, ?)`,
		requestID, owner, grant.RequestVersion, actor, string(identityJSON), stamp); err != nil {
		return executiongrant.Grant{}, err
	}
	approved, err := grantByRequest(ctx, conn, owner, requestID)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		return executiongrant.Grant{}, err
	}
	committed = true
	return approved, nil
}
