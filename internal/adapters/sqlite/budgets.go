package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

const productionBudgetSelect = `SELECT id, owner_id, session_id, plan_json, limit_credits,
 state, created_at, decided_at, decided_by, message_id, call_id FROM production_budgets`

func scanProductionBudget(row interface{ Scan(...any) error }) (budget.ProductionBudget, error) {
	var value budget.ProductionBudget
	var planJSON, created string
	var decidedAt sql.NullString
	err := row.Scan(&value.ID, &value.OwnerID, &value.SessionID, &planJSON,
		&value.LimitCredits, &value.State, &created, &decidedAt, &value.DecidedBy,
		&value.MessageID, &value.CallID)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal([]byte(planJSON), &value.Plan); err != nil {
		return value, fmt.Errorf("解析 production_budgets.plan_json: %w", err)
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

func budgetTotals(ctx context.Context, conn queryer, budgetID string) (spent, reserved int, err error) {
	err = conn.QueryRowContext(ctx, `SELECT
 COALESCE(SUM(CASE WHEN state = 'charged' THEN credits END), 0),
 COALESCE(SUM(CASE WHEN state = 'reserved' THEN credits END), 0)
 FROM budget_reservations WHERE budget_id = ?`, budgetID).Scan(&spent, &reserved)
	return spent, reserved, err
}

func budgetSummary(ctx context.Context, conn queryer, value budget.ProductionBudget) (budget.Summary, error) {
	spent, reserved, err := budgetTotals(ctx, conn, value.ID)
	if err != nil {
		return budget.Summary{}, err
	}
	return budget.SummaryOf(value, spent, reserved), nil
}

// CreateProductionBudget stores a pending plan and closes the session's other
// pending plan in the same transaction. A replay of the same ID returns the
// unchanged stored plan; the same ID with different content is a conflict.
func (r *Repository) CreateProductionBudget(ctx context.Context, value budget.ProductionBudget) (budget.ProductionBudget, error) {
	if value.ID == "" || value.OwnerID == "" || value.SessionID == "" ||
		value.LimitCredits <= 0 || value.State != budget.StatePending || value.CreatedAt.IsZero() {
		return budget.ProductionBudget{}, executiongrant.ErrInvalid
	}
	planJSON, err := json.Marshal(value.Plan)
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
		if existing.OwnerID != value.OwnerID || existing.SessionID != value.SessionID ||
			existing.LimitCredits != value.LimitCredits || string(storedPlan) != string(planJSON) {
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
 (id, owner_id, session_id, plan_json, limit_credits, state, created_at, decided_at, decided_by, message_id, call_id)
 VALUES (?, ?, ?, ?, ?, 'pending', ?, NULL, '', ?, ?)`,
		value.ID, value.OwnerID, value.SessionID, string(planJSON), value.LimitCredits, now, value.MessageID, value.CallID); err != nil {
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
// session's former active plan and releases the reservations of that plan that
// can no longer be claimed; a reservation of an approved step that may still
// bind stays reserved until its Task ends.
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
		former, err := productionBudgetsInStates(ctx, conn, ownerID, value.SessionID, budget.StateActive, budget.StateExhausted)
		if err != nil {
			return budget.ProductionBudget{}, err
		}
		for _, old := range former {
			if _, err := conn.ExecContext(ctx, `UPDATE production_budgets
 SET state = 'closed', decided_at = ?, decided_by = ? WHERE id = ? AND state IN ('active','exhausted')`,
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
// the plan's reserved credits are not held forever (#103 review B2). Grants
// without a reservation, claimed grants (a submit is in flight) and decided
// grants are left untouched.
func abandonUnboundGrantOnConn(ctx context.Context, conn *sql.Conn, owner, requestID string, now time.Time) error {
	if owner == "" || requestID == "" {
		return nil
	}
	stamp := formatTime(now)
	result, err := conn.ExecContext(ctx, `UPDATE execution_grants
 SET status = 'denied', decided_by = ?, decided_at = ?, updated_at = ?
 WHERE request_id = ? AND owner_id = ? AND status = 'approved'
 AND EXISTS (SELECT 1 FROM budget_reservations
  WHERE budget_reservations.grant_request_id = execution_grants.request_id
  AND budget_reservations.state = 'reserved' AND budget_reservations.task_id = '')`,
		executiongrant.AbandonedActor, stamp, stamp, requestID, owner)
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
	return nil
}

// SettleBudgetReservations settles ended Tasks' reservations, releases the
// reservations of grants that can no longer spend, and marks an active plan
// exhausted once its charged total reached the limit. It returns the plans it
// marked exhausted in this call, so their notice is posted once.
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
	rows, err := conn.QueryContext(ctx, productionBudgetSelect+`
 WHERE state = 'active' AND (
  SELECT COALESCE(SUM(credits), 0) FROM budget_reservations
  WHERE budget_id = production_budgets.id AND state = 'charged'
 ) >= limit_credits`)
	if err != nil {
		return nil, err
	}
	var exhausted []budget.ProductionBudget
	for rows.Next() {
		value, err := scanProductionBudget(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		exhausted = append(exhausted, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	stamp := formatTime(now)
	for _, value := range exhausted {
		if _, err := conn.ExecContext(ctx, `UPDATE production_budgets
 SET state = 'exhausted', decided_at = ? WHERE id = ? AND state = 'active'`,
			stamp, value.ID); err != nil {
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
	all, err := productionBudgetsInStates(ctx, r.db, ownerID, sessionID, budget.StateActive, budget.StateExhausted)
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
		args = append(args, budget.StateActive, budget.StateExhausted)
		rows, err := r.db.QueryContext(ctx, productionBudgetSelect+`
 WHERE owner_id = ? AND session_id IN (`+queryPlaceholders(len(chunk))+`)
 AND state IN (?, ?) ORDER BY created_at DESC`, args...)
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
	totals := make(map[string][2]int, len(budgetIDs))
	for start := 0; start < len(budgetIDs); start += productionBudgetSummaryBatch {
		end := min(start+productionBudgetSummaryBatch, len(budgetIDs))
		chunk := budgetIDs[start:end]
		args := make([]any, 0, len(chunk))
		for _, budgetID := range chunk {
			args = append(args, budgetID)
		}
		rows, err := r.db.QueryContext(ctx, `SELECT budget_id,
 COALESCE(SUM(CASE WHEN state = 'charged' THEN credits END), 0),
 COALESCE(SUM(CASE WHEN state = 'reserved' THEN credits END), 0)
 FROM budget_reservations WHERE budget_id IN (`+queryPlaceholders(len(chunk))+`)
 GROUP BY budget_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var budgetID string
			var spent, reserved int
			if err := rows.Scan(&budgetID, &spent, &reserved); err != nil {
				rows.Close()
				return nil, err
			}
			totals[budgetID] = [2]int{spent, reserved}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	for sessionID, value := range current {
		total := totals[value.ID]
		summaries[sessionID] = budget.SummaryOf(value, total[0], total[1])
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
// what already ended, re-check the remaining limit, insert the reservation and
// approve the grant in one BEGIN IMMEDIATE transaction. Two concurrent steps
// therefore cannot both take the last credits.
func (r *Repository) ApproveExecutionGrantWithBudget(ctx context.Context, owner, requestID, budgetID, actor string, credits int) (executiongrant.Grant, error) {
	if owner == "" || requestID == "" || budgetID == "" || credits < 0 ||
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
	spent, reserved, err := budgetTotals(ctx, conn, budgetID)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	if credits > value.LimitCredits-spent-reserved {
		return executiongrant.Grant{}, budget.ErrOverLimit
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO budget_reservations
 (grant_request_id, budget_id, task_id, credits, state, created_at)
 VALUES (?, ?, '', ?, 'reserved', ?)`,
		requestID, budgetID, credits, formatTime(now)); err != nil {
		return executiongrant.Grant{}, err
	}
	stamp := formatTime(now)
	result, err := conn.ExecContext(ctx, `UPDATE execution_grants
 SET status = 'approved', decided_by = ?, decided_at = ?, updated_at = ?
 WHERE request_id = ? AND owner_id = ? AND status = 'pending'`,
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
