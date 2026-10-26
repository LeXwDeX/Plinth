package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// GenerationTaskImport 是 ETL 从旧生产库合并出的完整任务。Task 保留
// 平台稳定 ID；Attempts 与 UsageLedgerEntries 则逐笔保留旧账本证据。
type GenerationTaskImport struct {
	Task               generation.Task
	Attempts           []GenerationAttemptImport
	UsageLedgerEntries []GenerationUsageLedgerImport
}

type GenerationAttemptImport struct {
	ID            generation.AttemptID
	Ordinal       int
	Offering      generation.Offering
	State         generation.AttemptState
	SubmissionKey string
	UpstreamJob   *generation.UpstreamJob
	Failure       *generation.Failure
	Opaque        json.RawMessage
	StartedAt     time.Time
	UpdatedAt     time.Time
	FinishedAt    *time.Time
}

type GenerationUsageLedgerImport struct {
	ID               string
	TaskID           generation.TaskID
	UserID           generation.OwnerID
	UserName         string
	UserEmail        string
	TaskType         string
	ProviderID       string
	EstimatedCredits int
	TaskStatus       string
	Params           json.RawMessage
	ResultURLs       json.RawMessage
	RetryCount       int
	MaxRetries       int
	NextRetryAt      *time.Time
	CreatedAt        time.Time
	FinishedAt       *time.Time
}

func (item GenerationTaskImport) CurrentAttemptID() generation.AttemptID {
	if len(item.Attempts) == 0 {
		return ""
	}
	return item.Attempts[len(item.Attempts)-1].ID
}

func (item *GenerationTaskImport) CurrentAttempt() *GenerationAttemptImport {
	if item == nil || len(item.Attempts) == 0 {
		return nil
	}
	return &item.Attempts[len(item.Attempts)-1]
}

type GenerationImportStats struct {
	Tasks    ImportStats
	Attempts ImportStats
	Ledger   ImportStats
	Assets   ImportStats
}

// ImportGenerationTasks 幂等导入任务域：稳定源 ID 的 provenance 与
// Task/Attempt 同事务提交；已导入记录在重跑时跳过，不覆盖在线侧新状态。
func (r *Repository) ImportGenerationTasks(
	ctx context.Context,
	tasks []GenerationTaskImport,
) error {
	_, err := r.ImportGenerationTasksAndAssets(ctx, tasks, nil)
	return err
}

// ImportGenerationTasksAndAssets 分批导入历史 Task、Attempt 与已固化
// Asset。单个任务及其 Asset 在同一事务中提交，避免留下成功任务缺产物的
// 中间状态；ETL 标记与业务行同事务，支持中断续跑。
func (r *Repository) ImportGenerationTasksAndAssets(
	ctx context.Context,
	tasks []GenerationTaskImport,
	assets []generation.Asset,
) (GenerationImportStats, error) {
	assetsByTask := make(map[generation.TaskID][]generation.Asset)
	taskIDs := make(map[generation.TaskID]bool, len(tasks))
	for _, item := range tasks {
		taskIDs[item.Task.ID] = true
	}
	for _, asset := range assets {
		if !taskIDs[asset.TaskID] {
			return GenerationImportStats{}, fmt.Errorf(
				"任务产物 %s 引用了未导入任务 %s",
				asset.ID,
				asset.TaskID,
			)
		}
		assetsByTask[asset.TaskID] = append(
			assetsByTask[asset.TaskID],
			asset,
		)
	}
	var result GenerationImportStats
	stats, err := r.importETLBatches(
		ctx,
		"generation_task",
		len(tasks),
		func(tx *sql.Tx, start int, end int) (ImportStats, error) {
			var taskStats ImportStats
			for _, item := range tasks[start:end] {
				task := item.Task
				taskAssets := assetsByTask[task.ID]
				claimed, err := claimETLImport(
					ctx,
					tx,
					"generation_task",
					string(task.ID),
				)
				if err != nil {
					return taskStats, err
				}
				if claimed {
					if err := importGenerationTask(
						ctx,
						tx,
						item,
						taskAssets,
					); err != nil {
						return taskStats, err
					}
					taskStats.Migrated++
					result.Attempts.Migrated += len(item.Attempts)
					result.Assets.Migrated += len(taskAssets)
				} else {
					taskStats.Skipped++
					result.Attempts.Skipped += len(item.Attempts)
					result.Assets.Skipped += len(taskAssets)
				}
				ledgerStats, err := importGenerationUsageLedger(
					ctx,
					tx,
					item,
				)
				if err != nil {
					return taskStats, err
				}
				result.Ledger.add(ledgerStats)
			}
			return taskStats, nil
		},
	)
	if err != nil {
		return result, err
	}
	result.Tasks = stats
	return result, nil
}

func importGenerationTask(
	ctx context.Context,
	tx *sql.Tx,
	item GenerationTaskImport,
	assets []generation.Asset,
) error {
	task := item.Task
	offeringJSON, failureJSON, err := marshalTaskParts(task)
	if err != nil {
		return fmt.Errorf("编码任务 %s: %w", task.ID, err)
	}
	attemptID := item.CurrentAttemptID()
	if attemptID == "" {
		return fmt.Errorf("导入任务 %s 缺少历史 Attempt", task.ID)
	}
	if _, err := tx.ExecContext(ctx, `
			INSERT INTO generation_tasks (
				id, owner_id, capability, offering_json, input_json,
				request_hash, idempotency_key, state, progress,
				failure_json, current_attempt_id, materialize_failures,
				next_action_at, version, created_at, updated_at,
				finished_at
			) VALUES (?, ?, ?, ?, ?, '', '', ?, ?, ?, ?, 0, NULL, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				owner_id = excluded.owner_id,
				capability = excluded.capability,
				offering_json = excluded.offering_json,
				input_json = excluded.input_json,
				state = excluded.state,
				progress = excluded.progress,
				failure_json = excluded.failure_json,
				current_attempt_id = excluded.current_attempt_id,
				version = excluded.version,
				created_at = excluded.created_at,
				updated_at = excluded.updated_at,
				finished_at = excluded.finished_at`,
		task.ID,
		task.OwnerID,
		task.Capability,
		offeringJSON,
		string(task.Input),
		task.State,
		task.Progress,
		failureJSON,
		attemptID,
		task.Version,
		formatTime(task.CreatedAt),
		formatTime(task.UpdatedAt),
		nullTimePointer(task.FinishedAt),
	); err != nil {
		return fmt.Errorf("导入任务 %s: %w", task.ID, err)
	}
	for _, attempt := range item.Attempts {
		attemptOfferingJSON, err := json.Marshal(attempt.Offering)
		if err != nil {
			return fmt.Errorf(
				"编码任务 %s 的 Attempt %s Offering: %w",
				task.ID,
				attempt.ID,
				err,
			)
		}
		attemptFailureJSON, err := marshalOptional(attempt.Failure)
		if err != nil {
			return fmt.Errorf(
				"编码任务 %s 的 Attempt %s 失败详情: %w",
				task.ID,
				attempt.ID,
				err,
			)
		}
		var upstreamProvider any
		var upstreamJob any
		if attempt.UpstreamJob != nil {
			upstreamProvider = attempt.UpstreamJob.ProviderID
			upstreamJob = attempt.UpstreamJob.RemoteID
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO generation_attempts (
				id, task_id, ordinal, offering_json, state,
				submission_key, upstream_provider_id, upstream_job_id,
				failure_json, opaque_json, last_observation_key,
				started_at, updated_at, finished_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				ordinal = excluded.ordinal,
				offering_json = excluded.offering_json,
				state = excluded.state,
				submission_key = excluded.submission_key,
				upstream_provider_id = excluded.upstream_provider_id,
				upstream_job_id = excluded.upstream_job_id,
				failure_json = excluded.failure_json,
				opaque_json = excluded.opaque_json,
				updated_at = excluded.updated_at,
				finished_at = excluded.finished_at`,
			attempt.ID,
			task.ID,
			attempt.Ordinal,
			string(attemptOfferingJSON),
			attempt.State,
			attempt.SubmissionKey,
			upstreamProvider,
			upstreamJob,
			attemptFailureJSON,
			nullBytes(attempt.Opaque),
			formatTime(attempt.StartedAt),
			formatTime(attempt.UpdatedAt),
			nullTimePointer(attempt.FinishedAt),
		); err != nil {
			return fmt.Errorf(
				"导入任务 %s 的 Attempt %s: %w",
				task.ID,
				attempt.ID,
				err,
			)
		}
	}
	for _, asset := range assets {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO generation_assets (
				id, task_id, attempt_id, role, variant, required, ordinal,
				storage_key, media_type, size_bytes, sha256, created_at, delivery_profile
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				task_id = excluded.task_id,
				attempt_id = excluded.attempt_id,
				role = excluded.role,
				variant = excluded.variant,
				required = excluded.required,
				ordinal = excluded.ordinal,
				storage_key = excluded.storage_key,
				media_type = excluded.media_type,
				size_bytes = excluded.size_bytes,
				sha256 = excluded.sha256,
				created_at = excluded.created_at,
				delivery_profile = excluded.delivery_profile`,
			asset.ID,
			asset.TaskID,
			asset.AttemptID,
			asset.Role,
			asset.Variant,
			boolInt(asset.Required),
			asset.Ordinal,
			asset.StorageKey,
			asset.MediaType,
			asset.Size,
			asset.SHA256,
			formatTime(asset.CreatedAt),
			asset.DeliveryProfile,
		); err != nil {
			return fmt.Errorf("导入任务产物 %s: %w", asset.ID, err)
		}
	}
	return nil
}

func importGenerationUsageLedger(
	ctx context.Context,
	tx *sql.Tx,
	item GenerationTaskImport,
) (ImportStats, error) {
	var stats ImportStats
	for _, entry := range item.UsageLedgerEntries {
		if entry.TaskID != item.Task.ID {
			return stats, fmt.Errorf(
				"账本 %s 引用了错误任务 %s（期望 %s）",
				entry.ID,
				entry.TaskID,
				item.Task.ID,
			)
		}
		claimed, err := claimETLImport(
			ctx,
			tx,
			"usage_ledger",
			entry.ID,
		)
		if err != nil {
			return stats, err
		}
		if !claimed {
			stats.Skipped++
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO generation_usage_ledger (
				id, task_id, user_id, user_name, user_email, task_type,
				provider_id, estimated_credits, task_status, params_json,
				result_urls_json, retry_count, max_retries, next_retry_at,
				created_at, finished_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			entry.ID,
			entry.TaskID,
			entry.UserID,
			entry.UserName,
			entry.UserEmail,
			entry.TaskType,
			entry.ProviderID,
			entry.EstimatedCredits,
			entry.TaskStatus,
			string(entry.Params),
			string(entry.ResultURLs),
			entry.RetryCount,
			entry.MaxRetries,
			nullTimePointer(entry.NextRetryAt),
			formatTime(entry.CreatedAt),
			nullTimePointer(entry.FinishedAt),
		); err != nil {
			return stats, fmt.Errorf(
				"导入任务 %s 的账本 %s: %w",
				item.Task.ID,
				entry.ID,
				err,
			)
		}
		stats.Migrated++
	}
	return stats, nil
}
