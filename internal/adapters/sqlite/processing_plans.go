package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

func (r *Repository) CreateProcessingPlan(ctx context.Context, plan processing.Plan) error {
	if !plan.Harness.Valid() {
		return processing.ErrInvalid
	}
	steps, err := json.Marshal(plan.Steps)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO processing_plans
		(id, owner_id, session_id, source_task_id, source_asset_id, state, approved_credits, request_hash, steps_json, failure, created_at, updated_at, version, harness_revision, harness_digest)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, plan.ID, plan.OwnerID, plan.SessionID, plan.SourceTaskID, plan.SourceAssetID, plan.State, 0, plan.RequestHash, string(steps), plan.Failure, plan.CreatedAt.Format(time.RFC3339Nano), plan.UpdatedAt.Format(time.RFC3339Nano), plan.Version, plan.Harness.Revision, plan.Harness.Digest)
	return err
}

func scanProcessingPlan(rows interface{ Scan(...any) error }) (processing.Plan, error) {
	var plan processing.Plan
	var steps, created, updated string
	var legacyApprovedCredits int
	err := rows.Scan(&plan.ID, &plan.OwnerID, &plan.SessionID, &plan.SourceTaskID, &plan.SourceAssetID, &plan.State, &legacyApprovedCredits, &plan.RequestHash, &steps, &plan.Failure, &created, &updated, &plan.Version, &plan.Harness.Revision, &plan.Harness.Digest)
	if err != nil {
		return processing.Plan{}, err
	}
	if !plan.Harness.Valid() {
		return processing.Plan{}, processing.ErrInvalid
	}
	if err := json.Unmarshal([]byte(steps), &plan.Steps); err != nil {
		return processing.Plan{}, fmt.Errorf("解析处理计划步骤: %w", err)
	}
	plan.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return processing.Plan{}, err
	}
	plan.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	return plan, err
}

const processingPlanColumns = `id, owner_id, session_id, source_task_id, source_asset_id, state, approved_credits, request_hash, steps_json, failure, created_at, updated_at, version, harness_revision, harness_digest`

func (r *Repository) GetProcessingPlan(ctx context.Context, owner generation.OwnerID, id string) (processing.Plan, error) {
	plan, err := scanProcessingPlan(r.db.QueryRowContext(ctx, `SELECT `+processingPlanColumns+` FROM processing_plans WHERE owner_id = ? AND id = ?`, owner, id))
	if errors.Is(err, sql.ErrNoRows) {
		return processing.Plan{}, processing.ErrMissing
	}
	return plan, err
}

func (r *Repository) ListProcessingPlans(ctx context.Context, owner generation.OwnerID, session string) ([]processing.Plan, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+processingPlanColumns+` FROM processing_plans WHERE owner_id = ? AND session_id = ? ORDER BY created_at DESC LIMIT 100`, owner, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return readProcessingPlans(rows)
}

// ListProcessingPlansBySource returns every plan of the owner's session made
// from one source task, newest first. It has no row cap: the format-completion
// check must see an old PAUSED plan even after a hundred newer plans.
func (r *Repository) ListProcessingPlansBySource(ctx context.Context, owner generation.OwnerID, session string, source generation.TaskID) ([]processing.Plan, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+processingPlanColumns+` FROM processing_plans WHERE owner_id = ? AND session_id = ? AND source_task_id = ? ORDER BY created_at DESC`, owner, session, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return readProcessingPlans(rows)
}

// ListActiveProcessingPlans 以稳定的 id 键集分页读取待推进计划：after
// 为上一页最后一个 id（首页为空串）。按 id 而不是 updated_at 翻页，推进
// 过程中被更新的计划不会在同一轮重复出现或挤掉其他计划。部分索引
// processing_plans_active 只负责筛出活动计划；按 id 排序由 SQLite 在这个
// 小集合上临时排序完成。
func (r *Repository) ListActiveProcessingPlans(ctx context.Context, after string, limit int) ([]processing.Plan, error) {
	if limit <= 0 {
		return nil, errors.New("处理计划分页大小必须为正数")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+processingPlanColumns+` FROM processing_plans WHERE state IN ('PENDING', 'RUNNING') AND id > ? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return readProcessingPlans(rows)
}

func readProcessingPlans(rows *sql.Rows) ([]processing.Plan, error) {
	plans := make([]processing.Plan, 0)
	for rows.Next() {
		plan, err := scanProcessingPlan(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}

func (r *Repository) SaveProcessingPlan(ctx context.Context, plan processing.Plan) error {
	steps, err := json.Marshal(plan.Steps)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE processing_plans SET state = ?, steps_json = ?, failure = ?, updated_at = ?, version = version + 1 WHERE id = ? AND owner_id = ? AND version = ? AND harness_revision = ? AND harness_digest = ?`, plan.State, string(steps), plan.Failure, plan.UpdatedAt.Format(time.RFC3339Nano), plan.ID, plan.OwnerID, plan.Version, plan.Harness.Revision, plan.Harness.Digest)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return processing.ErrConflict
	}
	return nil
}
