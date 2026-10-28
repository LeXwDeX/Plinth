package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	_ "modernc.org/sqlite"
)

//go:embed migrations/0001_generation.sql
var initialMigration string

//go:embed migrations/0002_attempt_observation_dedupe.sql
var observationDedupeMigration string

//go:embed migrations/0003_input_asset_metadata.sql
var inputAssetMetadataMigration string

//go:embed migrations/0004_task_tombstones.sql
var taskTombstonesMigration string

//go:embed migrations/0005_asset_variants.sql
var assetVariantsMigration string

//go:embed migrations/0006_authentication_users.sql
var authenticationUsersMigration string

//go:embed migrations/0007_chat_agent.sql
var chatAgentMigration string

//go:embed migrations/0008_chat_turns.sql
var chatTurnsMigration string

//go:embed migrations/0009_canvas.sql
var canvasMigration string

//go:embed migrations/0010_administration.sql
var administrationMigration string

//go:embed migrations/0011_balance_snapshots.sql
var balanceSnapshotsMigration string

//go:embed migrations/0012_authentication_sso.sql
var authenticationSSOMigration string

//go:embed migrations/0013_balance_snapshot_accounts.sql
var balanceSnapshotAccountsMigration string

//go:embed migrations/0014_etl_import_tracking.sql
var etlImportTrackingMigration string

//go:embed migrations/0015_generation_usage_ledger.sql
var generationUsageLedgerMigration string

//go:embed migrations/0016_legacy_migration_records.sql
var legacyMigrationRecordsMigration string

//go:embed migrations/0018_session_lifecycle.sql
var sessionLifecycleMigration string

//go:embed migrations/0020_task_read_projections.sql
var taskReadProjectionsMigration string

//go:embed migrations/0019_claim_assets.sql
var claimAssetsMigration string

//go:embed migrations/0017_observation_order.sql
var observationOrderMigration string

//go:embed migrations/0021_dispatch_checkpoint.sql
var dispatchCheckpointMigration string

//go:embed migrations/0022_resource_state.sql
var resourceStateMigration string

//go:embed migrations/0023_task_session.sql
var taskSessionMigration string

//go:embed migrations/0024_canvas_session_ownership.sql
var canvasSessionOwnershipMigration string

//go:embed migrations/0025_processing_plans.sql
var processingPlansMigration string

//go:embed migrations/0026_agent_state.sql
var agentStateMigration string

//go:embed migrations/0027_canvas_node_pk_revision.sql
var canvasNodePKRevisionMigration string

//go:embed migrations/0028_administration_audit_indexes.sql
var administrationAuditIndexesMigration string

//go:embed migrations/0029_login_failure_throttle.sql
var loginFailureThrottleMigration string

//go:embed migrations/0030_chat_turn_stop.sql
var chatTurnStopMigration string

//go:embed migrations/0031_chat_turn_checkpoint.sql
var chatTurnCheckpointMigration string

//go:embed migrations/0032_agent_consent_request_identity.sql
var agentConsentRequestIdentityMigration string

//go:embed migrations/0033_scene_objects.sql
var sceneObjectsMigration string

//go:embed migrations/0034_render_manifests.sql
var renderManifestsMigration string

//go:embed migrations/0035_prepared_inputs.sql
var preparedInputsMigration string

//go:embed migrations/0036_execution_grants.sql
var executionGrantsMigration string

//go:embed migrations/0037_agent_generation_operations.sql
var agentGenerationOperationsMigration string

//go:embed migrations/0038_skill_runs.sql
var skillRunsMigration string

//go:embed migrations/0039_model_captures.sql
var modelCapturesMigration string

//go:embed migrations/0040_execution_grant_turn.sql
var executionGrantTurnMigration string

//go:embed migrations/0041_agent_configuration.sql
var agentConfigurationMigration string

//go:embed migrations/0042_personal_skills.sql
var personalSkillsMigration string

//go:embed migrations/0043_chat_approval_mode.sql
var chatApprovalModeMigration string

//go:embed migrations/0044_chat_message_attachments.sql
var chatMessageAttachmentsMigration string

//go:embed migrations/0045_chat_session_notes.sql
var chatSessionNotesMigration string

//go:embed migrations/0046_chat_session_pick.sql
var chatSessionPickMigration string

//go:embed migrations/0047_pending_approvals.sql
var pendingApprovalsMigration string

//go:embed migrations/0048_skill_step_attempt.sql
var skillStepAttemptMigration string

//go:embed migrations/0049_chat_agent_scope.sql
var chatAgentScopeMigration string

//go:embed migrations/0050_chat_session_title_source.sql
var chatSessionTitleSourceMigration string

//go:embed migrations/0051_harness.sql
var harnessMigration string

//go:embed migrations/0052_asset_delivery_profile.sql
var assetDeliveryProfileMigration string

//go:embed migrations/0053_retention_receipts.sql
var retentionReceiptsMigration string

//go:embed migrations/0054_asset_recovery_receipts.sql
var assetRecoveryReceiptsMigration string

type Repository struct {
	db *sql.DB
	// clock 是写事务内的时间源（M3）：lease_until 必须由写事务时刻派生，
	// 不能用调用方在事务开始前采样的 now——SQLite 争用下该采样会任意陈旧，
	// 产生"出生即过期"的租约（lease_until < 提交 UpdatedAt → CAS 恒冲突）。
	// 默认墙钟；合成时间线的测试必须注入与 generation.Module 相同的时钟。
	clock func() time.Time
}

func Open(path string) (*Repository, error) {
	dsn, filePath, err := normalizeDSN(path)
	if err != nil {
		return nil, err
	}
	if filePath != "" && filePath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o750); err != nil {
			return nil, fmt.Errorf("创建数据库目录: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	repository := &Repository{db: db, clock: func() time.Time { return time.Now().UTC() }}
	if err := repository.configureAndMigrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return repository, nil
}

// SetClock 替换租约写入使用的写事务内时间源；只允许在装配阶段、并发使用
// 之前调用。注入 Clock 的 Module 必须在这里注入同一时钟，保证租约算术与
// 任务时间戳共享同一条时间线。
func (r *Repository) SetClock(now func() time.Time) {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	r.clock = now
}

func normalizeDSN(value string) (dsn string, filePath string, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "./data/art-ai.db"
	}
	if value == ":memory:" || strings.HasPrefix(value, "file::memory:") {
		return value, ":memory:", nil
	}
	if strings.HasPrefix(value, "file:") {
		filePath = strings.TrimPrefix(value, "file:")
		if query := strings.IndexByte(filePath, '?'); query >= 0 {
			filePath = filePath[:query]
		}
		if filePath == "" {
			return "", "", errors.New("SQLite file DSN 缺少路径")
		}
		if !filepath.IsAbs(filePath) {
			filePath, err = filepath.Abs(filePath)
			if err != nil {
				return "", "", fmt.Errorf("解析 SQLite 路径: %w", err)
			}
		}
		return "file:" + filepath.ToSlash(filePath), filePath, nil
	}
	filePath, err = filepath.Abs(value)
	if err != nil {
		return "", "", fmt.Errorf("解析 SQLite 路径: %w", err)
	}
	return "file:" + filepath.ToSlash(filePath), filePath, nil
}

func (r *Repository) configureAndMigrate(ctx context.Context) error {
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := r.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("配置 SQLite (%s): %w", statement, err)
		}
	}
	if _, err := r.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS go_schema_migrations (
			version     INTEGER PRIMARY KEY,
			name        TEXT NOT NULL,
			checksum    TEXT NOT NULL,
			applied_at  TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("创建 Go migration ledger: %w", err)
	}
	return r.runMigrations(ctx, migrations())
}

// MigrationHead 报告当前二进制携带的最高迁移版本（W7 版本守卫测试
// 与本地回滚 drill 的对齐口径：高于该版本的库必须拒绝打开）。
func MigrationHead() int {
	all := migrations()
	return all[len(all)-1].version
}

//go:embed migrations/0055_native_pricing.sql
var nativePricingMigration string

//go:embed migrations/0056_canvas_placed_results.sql
var canvasPlacedResultsMigration string

//go:embed migrations/0057_canvas_frame.sql
var canvasFrameMigration string

//go:embed migrations/0058_input_asset_derivation.sql
var inputAssetDerivationMigration string

//go:embed migrations/0059_agent_operation_recharge.sql
var agentOperationRechargeMigration string

//go:embed migrations/0060_upstream_refetches.sql
var upstreamRefetchesMigration string

//go:embed migrations/0061_generation_task_time_order.sql
var generationTaskTimeOrderMigration string

//go:embed migrations/0062_chat_turn_session_index.sql
var chatTurnSessionIndexMigration string

//go:embed migrations/0063_asset_media_type_corrections.sql
var assetMediaTypeCorrectionsMigration string

//go:embed migrations/0064_chat_choices.sql
var chatChoicesMigration string

//go:embed migrations/0065_chat_session_context.sql
var chatSessionContextMigration string

//go:embed migrations/0066_llm_settings.sql
var llmSettingsMigration string

//go:embed migrations/0067_agent_operation_reference_use.sql
var agentOperationReferenceUseMigration string

//go:embed migrations/0068_lineage_conversion_fold.sql
var lineageConversionFoldMigration string

//go:embed migrations/0069_delivery_completion.sql
var deliveryCompletionMigration string

//go:embed migrations/0070_agent_operation_reference_source.sql
var agentOperationReferenceSourceMigration string

func migrations() []migration {
	return []migration{{
		version: 1,
		name:    "generation",
		sql:     initialMigration,
	}, {
		version: 2,
		name:    "attempt_observation_dedupe",
		sql:     observationDedupeMigration,
	}, {
		version: 3,
		name:    "input_asset_metadata",
		sql:     inputAssetMetadataMigration,
	}, {
		version: 4,
		name:    "task_tombstones",
		sql:     taskTombstonesMigration,
	}, {
		version: 5,
		name:    "asset_variants",
		sql:     assetVariantsMigration,
	}, {
		version: 6,
		name:    "authentication_users",
		sql:     authenticationUsersMigration,
	}, {
		version: 7,
		name:    "chat_agent",
		sql:     chatAgentMigration,
	}, {
		version: 8,
		name:    "chat_turns",
		sql:     chatTurnsMigration,
	}, {
		version: 9,
		name:    "canvas",
		sql:     canvasMigration,
	}, {
		version: 10,
		name:    "administration",
		sql:     administrationMigration,
	}, {
		version: 11,
		name:    "balance_snapshots",
		sql:     balanceSnapshotsMigration,
	}, {
		version: 12,
		name:    "authentication_sso",
		sql:     authenticationSSOMigration,
	}, {
		version: 13,
		name:    "balance_snapshot_accounts",
		sql:     balanceSnapshotAccountsMigration,
	}, {
		version: 14,
		name:    "etl_import_tracking",
		sql:     etlImportTrackingMigration,
	}, {
		version: 15,
		name:    "generation_usage_ledger",
		sql:     generationUsageLedgerMigration,
	}, {
		version: 16,
		name:    "legacy_migration_records",
		sql:     legacyMigrationRecordsMigration,
	}, {version: 17, name: "observation_order", sql: observationOrderMigration}, {version: 18, name: "session_lifecycle", sql: sessionLifecycleMigration}, {version: 19, name: "claim_assets", sql: claimAssetsMigration}, {version: 20, name: "task_read_projections", sql: taskReadProjectionsMigration}, {version: 21, name: "dispatch_checkpoint", sql: dispatchCheckpointMigration}, {version: 22, name: "resource_state", sql: resourceStateMigration}, {version: 23, name: "task_session", sql: taskSessionMigration}, {version: 24, name: "canvas_session_ownership", sql: canvasSessionOwnershipMigration}, {version: 25, name: "processing_plans", sql: processingPlansMigration}, {version: 26, name: "agent_state", sql: agentStateMigration}, {version: 27, name: "canvas_node_pk_revision", sql: canvasNodePKRevisionMigration}, {version: 28, name: "administration_audit_indexes", sql: administrationAuditIndexesMigration}, {version: 29, name: "login_failure_throttle", sql: loginFailureThrottleMigration}, {version: 30, name: "chat_turn_stop", sql: chatTurnStopMigration}, {version: 31, name: "chat_turn_checkpoint", sql: chatTurnCheckpointMigration}, {version: 32, name: "agent_consent_request_identity", sql: agentConsentRequestIdentityMigration}, {version: 33, name: "scene_objects", sql: sceneObjectsMigration}, {version: 34, name: "render_manifests", sql: renderManifestsMigration}, {version: 35, name: "prepared_inputs", sql: preparedInputsMigration}, {version: 36, name: "execution_grants", sql: executionGrantsMigration}, {version: 37, name: "agent_generation_operations", sql: agentGenerationOperationsMigration}, {version: 38, name: "skill_runs", sql: skillRunsMigration}, {version: 39, name: "model_captures", sql: modelCapturesMigration}, {version: 40, name: "execution_grant_turn", sql: executionGrantTurnMigration}, {version: 41, name: "agent_configuration", sql: agentConfigurationMigration}, {version: 42, name: "personal_skills", sql: personalSkillsMigration}, {version: 43, name: "chat_approval_mode", sql: chatApprovalModeMigration}, {version: 44, name: "chat_message_attachments", sql: chatMessageAttachmentsMigration}, {version: 45, name: "chat_session_notes", sql: chatSessionNotesMigration}, {version: 46, name: "chat_session_pick", sql: chatSessionPickMigration}, {version: 47, name: "pending_approvals", sql: pendingApprovalsMigration}, {version: 48, name: "skill_step_attempt", sql: skillStepAttemptMigration}, {version: 49, name: "chat_agent_scope", sql: chatAgentScopeMigration}, {version: 50, name: "chat_session_title_source", sql: chatSessionTitleSourceMigration}, {version: 51, name: "harness", sql: harnessMigration}, {version: 52, name: "asset_delivery_profile", sql: assetDeliveryProfileMigration}, {version: 53, name: "retention_receipts", sql: retentionReceiptsMigration}, {version: 54, name: "asset_recovery_receipts", sql: assetRecoveryReceiptsMigration}, {version: 55, name: "native_pricing", sql: nativePricingMigration}, {version: 56, name: "canvas_placed_results", sql: canvasPlacedResultsMigration}, {version: 57, name: "canvas_frame", sql: canvasFrameMigration}, {version: 58, name: "input_asset_derivation", sql: inputAssetDerivationMigration}, {version: 59, name: "agent_operation_recharge", sql: agentOperationRechargeMigration}, {version: 60, name: "upstream_refetches", sql: upstreamRefetchesMigration}, {version: 61, name: "generation_task_time_order", sql: generationTaskTimeOrderMigration}, {version: 62, name: "chat_turn_session_index", sql: chatTurnSessionIndexMigration}, {version: 63, name: "asset_media_type_corrections", sql: assetMediaTypeCorrectionsMigration}, {version: 64, name: "chat_choices", sql: chatChoicesMigration}, {version: 65, name: "chat_session_context", sql: chatSessionContextMigration}, {version: 66, name: "llm_settings", sql: llmSettingsMigration}, {version: 67, name: "agent_operation_reference_use", sql: agentOperationReferenceUseMigration}, {version: 68, name: "lineage_conversion_fold", sql: lineageConversionFoldMigration}, {version: 69, name: "delivery_completion", sql: deliveryCompletionMigration}, {version: 70, name: "agent_operation_reference_source", sql: agentOperationReferenceSourceMigration}}
}

type migration struct {
	version int
	name    string
	sql     string
}

func (r *Repository) runMigrations(
	ctx context.Context,
	migrations []migration,
) error {
	if len(migrations) == 0 {
		return nil
	}
	var databaseVersion sql.NullInt64
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT MAX(version) FROM go_schema_migrations`,
	).Scan(&databaseVersion); err != nil {
		return fmt.Errorf("读取 Go migration 版本: %w", err)
	}
	latest := migrations[len(migrations)-1].version
	if databaseVersion.Valid && databaseVersion.Int64 > int64(latest) {
		return fmt.Errorf(
			"数据库 migration 版本 %d 高于当前二进制 %d",
			databaseVersion.Int64,
			latest,
		)
	}
	for _, migration := range migrations {
		checksum := migrationChecksum(migration.sql)
		var storedChecksum string
		err := r.db.QueryRowContext(
			ctx,
			`SELECT checksum FROM go_schema_migrations WHERE version = ?`,
			migration.version,
		).Scan(&storedChecksum)
		switch {
		case err == nil:
			if storedChecksum != checksum {
				return fmt.Errorf(
					"migration %04d_%s 校验和不匹配",
					migration.version,
					migration.name,
				)
			}
			continue
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("读取 migration %d: %w", migration.version, err)
		}

		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("开始 migration %d: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf(
				"执行 migration %04d_%s: %w",
				migration.version,
				migration.name,
				err,
			)
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO go_schema_migrations (version, name, checksum, applied_at)
			 VALUES (?, ?, ?, ?)`,
			migration.version,
			migration.name,
			checksum,
			formatTime(time.Now()),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("登记 migration %d: %w", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("提交 migration %d: %w", migration.version, err)
		}
	}
	return nil
}

func migrationChecksum(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

func (r *Repository) Create(
	ctx context.Context,
	task generation.Task,
	event generation.Event,
	maxInflight int,
) (generation.Task, bool, error) {
	return r.createTask(ctx, task, event, maxInflight, nil, nil)
}

// CreateConversionGuarded admits a declared paid format conversion only when
// no active Task of the same owner and session already runs the same exact
// model source into an overlapping target format. The read runs under the same
// BEGIN IMMEDIATE transaction as the insert, so two simultaneous submissions
// cannot both pass. A read or rule error aborts the insert: a failed guard must
// never allow a second charged Task.
func (r *Repository) CreateConversionGuarded(
	ctx context.Context,
	task generation.Task,
	event generation.Event,
	maxInflight int,
	check generation.ConversionConflictChecker,
) (generation.Task, bool, error) {
	if task.Conversion == nil || check == nil {
		return generation.Task{}, false, generation.RepositoryConversionConflict
	}
	return r.createTask(ctx, task, event, maxInflight, nil, check)
}

// createTask is the one Task insert. An ordinary Task consumes its execution
// grant in the same transaction; an upstream refetch (#295) has none (it
// cannot create or charge upstream work) and inserts its source binding
// instead.
func (r *Repository) createTask(
	ctx context.Context,
	task generation.Task,
	event generation.Event,
	maxInflight int,
	refetch *generation.UpstreamRefetch,
	conversionGuard generation.ConversionConflictChecker,
) (generation.Task, bool, error) {
	if refetch != nil {
		if task.Admission != nil || task.NativeQuote != nil || task.Offering.NativePricing != nil ||
			refetch.TaskID != task.ID || refetch.OwnerID != task.OwnerID || refetch.SessionID != task.SessionID {
			return generation.Task{}, false, generation.RepositoryAuthorizationRequired
		}
	} else if task.Offering.NativePricing != nil {
		q, err := task.Offering.EvaluateNativeQuote(task.Input)
		a, _ := json.Marshal(q)
		b, _ := json.Marshal(task.NativeQuote)
		if err != nil || task.NativeQuote == nil || string(a) != string(b) || task.Admission == nil {
			return generation.Task{}, false, generation.RepositoryAuthorizationRequired
		}
	} else if task.NativeQuote != nil {
		return generation.Task{}, false, generation.RepositoryAuthorizationRequired
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return generation.Task{}, false, err
	}
	defer conn.Close()
	// Reserve SQLite's write lock before either read. A deferred transaction can
	// read a stale snapshot and then fail its write with SQLITE_BUSY_SNAPSHOT;
	// BEGIN IMMEDIATE serializes admission across independent repository instances.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return generation.Task{}, false, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	if task.IdempotencyKey != "" {
		existing, requestHash, found, err := findIdempotent(ctx, conn, task.OwnerID, task.IdempotencyKey)
		if err != nil {
			return generation.Task{}, false, err
		}
		if found {
			if requestHash != task.RequestHash {
				return generation.Task{}, false, generation.RepositoryIdempotencyConflict
			}
			var storedSession sql.NullString
			if err := conn.QueryRowContext(ctx, `SELECT session_id FROM generation_tasks WHERE id = ?`, existing).Scan(&storedSession); err != nil {
				return generation.Task{}, false, err
			}
			if storedSession.String != task.SessionID {
				return generation.Task{}, false, generation.RepositoryIdempotencyConflict
			}
			replayTask := task
			replayTask.ID = existing
			if refetch != nil {
				// The replayed Task must be the refetch of the same source Attempt.
				var source string
				if err := conn.QueryRowContext(ctx, `SELECT source_attempt_id FROM generation_upstream_refetches WHERE task_id = ? AND owner_id = ?`, existing, task.OwnerID).Scan(&source); err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						return generation.Task{}, false, generation.RepositoryIdempotencyConflict
					}
					return generation.Task{}, false, err
				}
				if source != string(refetch.SourceAttemptID) {
					return generation.Task{}, false, generation.RepositoryIdempotencyConflict
				}
			} else if err := verifyTaskGrantReplayOn(ctx, conn, replayTask, task.Admission); err != nil {
				return generation.Task{}, false, err
			}
			if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
				return generation.Task{}, false, err
			}
			committed = true
			if err := conn.Close(); err != nil {
				return generation.Task{}, false, err
			}
			stored, err := r.Get(ctx, task.OwnerID, existing)
			return stored, true, err
		}
	}
	if conversionGuard != nil && task.Conversion != nil {
		if err := r.admitConversion(ctx, conn, task, conversionGuard); err != nil {
			return generation.Task{}, false, err
		}
	}
	// Idempotent replay takes precedence over the limit, including after the
	// original task has left the inflight states. Only new paid work consumes a slot.
	if maxInflight > 0 {
		var inflight int
		if err := conn.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM generation_tasks
			WHERE owner_id = ? AND state IN (?, ?, ?)
			AND NOT EXISTS (
				SELECT 1 FROM generation_task_tombstones tombstone
				WHERE tombstone.task_id = generation_tasks.id
			)`, task.OwnerID, generation.TaskQueued, generation.TaskRunning,
			generation.TaskMaterializing).Scan(&inflight); err != nil {
			return generation.Task{}, false, err
		}
		if inflight >= maxInflight {
			return generation.Task{}, false, generation.RepositoryAdmissionFull
		}
	}
	if task.SessionID != "" {
		var ownerID string
		if err := conn.QueryRowContext(ctx, `SELECT owner_id FROM go_chat_sessions WHERE id = ?`, task.SessionID).Scan(&ownerID); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return generation.Task{}, false, err
			}
			return generation.Task{}, false, generation.RepositorySessionNotFound
		}
		if ownerID != string(task.OwnerID) {
			return generation.Task{}, false, generation.RepositorySessionNotFound
		}
	}
	// The human grant is consumed in this same write transaction as the Task.
	// Every new Provider Task takes this path, regardless of catalog pricing.
	// A refetch Task is not a Provider submission: it only reads an already
	// accepted job again and never reaches Start.
	if refetch == nil {
		if err := r.claimTaskExecutionGrant(ctx, conn, task); err != nil {
			return generation.Task{}, false, err
		}
	}

	offeringJSON, failureJSON, err := marshalTaskParts(task)
	if err != nil {
		return generation.Task{}, false, err
	}
	resourcesJSON, err := generation.EncodeResourceStatesForColumn(task.Resources)
	if err != nil {
		return generation.Task{}, false, err
	}
	_, err = conn.ExecContext(ctx, `
		INSERT INTO generation_tasks (
			id, owner_id, capability, offering_json, input_json, request_hash,
			idempotency_key, state, progress, failure_json, current_attempt_id,
			materialize_failures, next_action_at, version, created_at, updated_at, finished_at,
			resource_state_json, session_id,native_quote_json,native_quote_required
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,?,?)`,
		task.ID,
		task.OwnerID,
		task.Capability,
		offeringJSON,
		string(task.Input),
		task.RequestHash,
		task.IdempotencyKey,
		task.State,
		task.Progress,
		failureJSON,
		nullString(string(task.CurrentAttemptID)),
		task.MaterializeFailures,
		nullTaskTime(task.NextActionAt),
		task.Version,
		formatTime(task.CreatedAt),
		formatTime(task.UpdatedAt),
		nullTimePointer(task.FinishedAt),
		resourcesJSON,
		nullString(task.SessionID), nativeQuoteColumn(task.NativeQuote), task.NativeQuote != nil,
	)
	if err != nil {
		return generation.Task{}, false, err
	}
	if err := insertEvent(ctx, conn, event); err != nil {
		return generation.Task{}, false, err
	}
	if refetch != nil {
		if err := insertUpstreamRefetch(ctx, conn, *refetch); err != nil {
			return generation.Task{}, false, err
		}
	} else if err := r.bindTaskExecutionGrant(ctx, conn, task); err != nil {
		return generation.Task{}, false, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return generation.Task{}, false, err
	}
	committed = true
	return task, false, nil
}

// admitConversion refuses a new paid conversion when an active Task of the
// same owner and session runs the same exact model source into an overlapping
// target format. It runs inside createTask's immediate transaction, after the
// idempotent replay lookup, so a replay of the same key is never rejected.
func (r *Repository) admitConversion(
	ctx context.Context,
	conn *sql.Conn,
	task generation.Task,
	check generation.ConversionConflictChecker,
) error {
	if len(task.Conversion.Formats) == 0 {
		return errors.New("格式转换没有目标格式，无法证明没有并行的付费转换")
	}
	newSource, err := conversionSourceIdentity(ctx, conn, task.OwnerID, *task.Conversion)
	if err != nil {
		return err
	}
	if newSource.task == "" && newSource.inputAsset == "" {
		return errors.New("格式转换的来源无法确定")
	}
	rows, err := conn.QueryContext(ctx, taskSelect+` WHERE generation_tasks.owner_id = ?
		AND generation_tasks.session_id = ?
		AND generation_tasks.state IN (?, ?, ?)
		AND NOT EXISTS (
			SELECT 1 FROM generation_task_tombstones tombstone
			WHERE tombstone.task_id = generation_tasks.id
		)`, task.OwnerID, task.SessionID, generation.TaskQueued, generation.TaskRunning, generation.TaskMaterializing)
	if err != nil {
		return err
	}
	active := make([]generation.Task, 0, 4)
	for rows.Next() {
		value, scanErr := scanTask(rows)
		if scanErr != nil {
			rows.Close()
			return scanErr
		}
		active = append(active, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, value := range active {
		intent, declared, err := check(value)
		if err != nil {
			return err
		}
		if !declared {
			continue
		}
		source, err := conversionSourceIdentity(ctx, conn, task.OwnerID, intent)
		if err != nil {
			return err
		}
		if source.equal(newSource) && formatsOverlap(task.Conversion.Formats, intent.Formats) {
			return generation.RepositoryConversionConflict
		}
	}
	return nil
}

// conversionSource is the exact model source of a paid conversion, tagged by
// where that identity lives. A Task output normalizes to its owning Task; an
// uploaded or derived input asset keeps its stable input asset identity. The
// two forms are never equal, even if an ID happened to collide.
type conversionSource struct {
	task       generation.TaskID
	inputAsset generation.AssetID
}

func (source conversionSource) equal(other conversionSource) bool {
	if source.task != "" || other.task != "" {
		return source.task != "" && source.task == other.task
	}
	if source.inputAsset != "" || other.inputAsset != "" {
		return source.inputAsset != "" && source.inputAsset == other.inputAsset
	}
	return false
}

// conversionSourceIdentity resolves a conversion source to one stable identity.
// A generated Task output normalizes to its owning Task, so a source_task
// reference and the same saved model file are one source. An uploaded or
// derived input asset is verified against the owner and keeps its input asset
// ID: it has no owning Task to normalize to. A source that resolves to neither
// aborts admission rather than guessing, because guessing could admit a second
// paid run.
func conversionSourceIdentity(
	ctx context.Context,
	conn *sql.Conn,
	owner generation.OwnerID,
	intent generation.ConversionIntent,
) (conversionSource, error) {
	if intent.SourceTaskID != "" {
		return conversionSource{task: intent.SourceTaskID}, nil
	}
	if intent.SourceAssetID == "" {
		return conversionSource{}, errors.New("格式转换缺少来源模型")
	}
	var taskID generation.TaskID
	err := conn.QueryRowContext(ctx, `
		SELECT asset.task_id FROM generation_assets asset
		JOIN generation_tasks task ON task.id = asset.task_id
		WHERE asset.id = ? AND task.owner_id = ?`,
		intent.SourceAssetID, owner).Scan(&taskID)
	switch {
	case err == nil:
		if taskID == "" {
			return conversionSource{}, errors.New("格式转换的来源资产没有所属任务")
		}
		return conversionSource{task: taskID}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return conversionSource{}, err
	}
	var inputOwner generation.OwnerID
	err = conn.QueryRowContext(ctx, `SELECT owner_id FROM generation_input_assets WHERE id = ?`, intent.SourceAssetID).Scan(&inputOwner)
	if errors.Is(err, sql.ErrNoRows) {
		return conversionSource{}, fmt.Errorf("格式转换的来源资产 %s 无法解析", intent.SourceAssetID)
	}
	if err != nil {
		return conversionSource{}, err
	}
	if inputOwner != owner {
		return conversionSource{}, fmt.Errorf("格式转换的来源资产 %s 不属于该用户", intent.SourceAssetID)
	}
	return conversionSource{inputAsset: intent.SourceAssetID}, nil
}

func formatsOverlap(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for _, a := range left {
		for _, b := range right {
			if a == b {
				return true
			}
		}
	}
	return false
}

func findIdempotent(
	ctx context.Context,
	tx interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	owner generation.OwnerID,
	key string,
) (generation.TaskID, string, bool, error) {
	var id generation.TaskID
	var requestHash string
	err := tx.QueryRowContext(ctx, `
		SELECT id, request_hash
		FROM generation_tasks
		WHERE owner_id = ? AND idempotency_key = ?`,
		owner,
		key,
	).Scan(&id, &requestHash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return id, requestHash, true, nil
}

func (r *Repository) Get(
	ctx context.Context,
	owner generation.OwnerID,
	id generation.TaskID,
) (generation.Task, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return generation.Task{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(
		ctx,
		taskSelect+` WHERE id = ? AND owner_id = ?
			AND NOT EXISTS (
				SELECT 1 FROM generation_task_tombstones tombstone
				WHERE tombstone.task_id = generation_tasks.id
			)`,
		id,
		owner,
	)
	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return generation.Task{}, generation.RepositoryNotFound
	}
	if err != nil {
		return generation.Task{}, err
	}
	task.Assets, err = r.loadAssets(ctx, tx, task.ID)
	if err != nil {
		return generation.Task{}, err
	}
	if task.CurrentAttemptID != "" {
		attempt, err := loadAttempt(ctx, tx, task.CurrentAttemptID)
		if err != nil {
			return generation.Task{}, err
		}
		task.ProviderData = append(json.RawMessage(nil), attempt.Opaque...)
		task.DeliveryCompleted = attempt.DeliveryCompletion != nil
	}
	return task, nil
}

func (r *Repository) List(
	ctx context.Context,
	scope generation.TaskListScope,
	query generation.ListQuery,
) (generation.TaskPage, error) {
	if query.Limit <= 0 || query.Limit == int(^uint(0)>>1) || query.Offset < 0 {
		return generation.TaskPage{}, &generation.Error{Code: generation.ErrInvalidInput, Operation: "list", Detail: "任务分页参数无效"}
	}
	// Count, identifiers, rows and assets describe one committed snapshot.
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return generation.TaskPage{}, err
	}
	defer tx.Rollback()
	args := []any{}
	clauses := []string{`NOT EXISTS (
		SELECT 1 FROM generation_task_tombstones tombstone
		WHERE tombstone.task_id = generation_tasks.id
	)`}
	if !scope.AllOwners() {
		clauses = append(clauses, "owner_id = ?")
		args = append(args, scope.OwnerID())
	}
	if query.SessionID != "" {
		clauses = append(clauses, "session_id = ?")
		args = append(args, query.SessionID)
	}
	if query.Capability != "" {
		clauses = append(clauses, "capability = ?")
		args = append(args, query.Capability)
	}
	if len(query.OfferingIDs) > 0 {
		placeholders := make([]string, len(query.OfferingIDs))
		for index, id := range query.OfferingIDs {
			placeholders[index] = "?"
			args = append(args, id)
		}
		clauses = append(clauses, "json_extract(generation_tasks.offering_json, '$.id') IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(query.States) > 0 {
		placeholders := make([]string, len(query.States))
		for index, state := range query.States {
			placeholders[index] = "?"
			args = append(args, state)
		}
		clauses = append(clauses, "state IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(query.ExcludedStates) > 0 {
		placeholders := make([]string, len(query.ExcludedStates))
		for index, state := range query.ExcludedStates {
			placeholders[index] = "?"
			args = append(args, state)
		}
		clauses = append(clauses, "state NOT IN ("+strings.Join(placeholders, ",")+")")
	}
	if query.SearchID != "" {
		clauses = append(clauses, `id LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(query.SearchID)+"%")
	}
	countArgs := append([]any(nil), args...)
	countClauses := append([]string(nil), clauses...)
	if query.Cursor != "" {
		createdAt, id, err := decodeCursor(query.Cursor)
		if err != nil {
			return generation.TaskPage{}, &generation.Error{
				Code:      generation.ErrInvalidInput,
				Operation: "list",
				Detail:    "分页游标无效",
				Cause:     err,
			}
		}
		clauses = append(clauses, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, formatTime(createdAt), formatTime(createdAt), id)
	}
	var total int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM generation_tasks WHERE `+
			strings.Join(countClauses, " AND "),
		countArgs...,
	).Scan(&total); err != nil {
		return generation.TaskPage{}, err
	}

	orderBy := "created_at DESC, id DESC"
	if query.ActiveFirst {
		orderBy = `CASE
			WHEN state IN ('RUNNING', 'MATERIALIZING') THEN 0
			ELSE 1
		END, created_at DESC, id DESC`
	}
	taskIDs, err := r.listTaskIDs(
		ctx,
		tx,
		clauses,
		args,
		orderBy,
		query.Limit+1,
		query.Offset,
	)
	if err != nil {
		return generation.TaskPage{}, err
	}
	hasMore := len(taskIDs) > query.Limit
	if hasMore {
		taskIDs = taskIDs[:query.Limit]
	}
	tasks, err := r.loadTaskSummaries(ctx, tx, taskIDs)
	if err != nil {
		return generation.TaskPage{}, err
	}
	// Repository 使用单连接以保证内存数据库与 PRAGMA 一致；先关闭任务结果集，
	// 再加载资产，避免同一连接上的嵌套查询互相等待。
	for index := range tasks {
		tasks[index].Assets, err = r.loadAssets(ctx, tx, tasks[index].ID)
		if err != nil {
			return generation.TaskPage{}, err
		}
	}
	page := generation.TaskPage{Tasks: tasks, Total: total}
	if hasMore && len(tasks) > 0 {
		last := tasks[len(tasks)-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	if err := tx.Commit(); err != nil {
		return generation.TaskPage{}, err
	}
	return page, nil
}

func (r *Repository) listTaskIDs(
	ctx context.Context,
	reader queryer,
	clauses []string,
	args []any,
	orderBy string,
	limit int,
	offset int,
) ([]generation.TaskID, error) {
	queryArgs := append([]any(nil), args...)
	queryArgs = append(queryArgs, limit, offset)
	rows, err := reader.QueryContext(
		ctx,
		`SELECT id FROM generation_tasks WHERE `+
			strings.Join(clauses, " AND ")+`
			ORDER BY `+orderBy+` LIMIT ? OFFSET ?`,
		queryArgs...,
	)
	if err != nil {
		return nil, err
	}
	taskIDs := make([]generation.TaskID, 0)
	for rows.Next() {
		var taskID generation.TaskID
		if err := rows.Scan(&taskID); err != nil {
			rows.Close()
			return nil, err
		}
		taskIDs = append(taskIDs, taskID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return taskIDs, nil
}

func (r *Repository) loadTasksByID(ctx context.Context, taskIDs []generation.TaskID) ([]generation.Task, error) {
	return r.loadTaskSummaries(ctx, r.db, taskIDs)
}

func (r *Repository) loadTaskSummaries(ctx context.Context, reader queryer, taskIDs []generation.TaskID) ([]generation.Task, error) {
	if len(taskIDs) == 0 {
		return []generation.Task{}, nil
	}
	placeholders := make([]string, len(taskIDs))
	args := make([]any, len(taskIDs))
	for index, taskID := range taskIDs {
		placeholders[index] = "?"
		args[index] = taskID
	}
	rows, err := reader.QueryContext(
		ctx,
		taskSummarySelect+` WHERE generation_tasks.id IN (`+strings.Join(placeholders, ",")+`) AND NOT EXISTS (SELECT 1 FROM generation_task_tombstones WHERE task_id = generation_tasks.id)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	tasksByID := make(map[generation.TaskID]generation.Task, len(taskIDs))
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		tasksByID[task.ID] = task
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	tasks := make([]generation.Task, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		task, exists := tasksByID[taskID]
		if !exists {
			continue
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func (r *Repository) Load(
	ctx context.Context,
	id generation.TaskID,
) (generation.Aggregate, error) {
	return r.loadAggregateSnapshot(
		ctx,
		taskSelect+` WHERE id = ?
			AND NOT EXISTS (
				SELECT 1 FROM generation_task_tombstones tombstone
				WHERE tombstone.task_id = generation_tasks.id
			)`,
		id,
	)
}

func (r *Repository) LoadByUpstreamJob(
	ctx context.Context,
	providerID string,
	remoteID string,
) (generation.Aggregate, error) {
	return r.loadAggregateSnapshot(
		ctx,
		taskSelect+` WHERE id = (
				SELECT task_id
				FROM generation_attempts
				WHERE upstream_provider_id = ? AND upstream_job_id = ?
				LIMIT 1
			)
			AND NOT EXISTS (
				SELECT 1 FROM generation_task_tombstones tombstone
				WHERE tombstone.task_id = generation_tasks.id
			)`,
		providerID,
		remoteID,
	)
}

// loadAggregateSnapshot reads the Task and its parts from one transaction
// because CommitDeliveryCompletion commits them atomically.
func (r *Repository) loadAggregateSnapshot(
	ctx context.Context,
	query string,
	args ...any,
) (generation.Aggregate, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return generation.Aggregate{}, err
	}
	defer tx.Rollback()
	task, err := scanTask(tx.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return generation.Aggregate{}, generation.RepositoryNotFound
	}
	if err != nil {
		return generation.Aggregate{}, err
	}
	return r.loadAggregate(ctx, tx, task)
}

func (r *Repository) ClaimReady(
	ctx context.Context,
	workerID string,
	now time.Time,
	leaseFor time.Duration,
	limit int,
) ([]generation.Lease, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// M3：lease_until 由写事务时刻派生。调用方在 ClaimReady 之前采样的 now
	// 在 SQLite 争用/调度饥饿下会任意陈旧（F1a 实测 claim→commit 延迟
	// 82.8~292.3ms），旧采样写出的租约出生即过期（lease_until < 提交
	// UpdatedAt），令此后每次提交 CAS 恒冲突、任务非收敛重驱。租约挑选与
	// 领取守卫仍用调用方传入的 now（哪些过期租约可重领取的语义不变）。
	writeNow := r.clock()

	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM generation_tasks
		WHERE (state IN (?, ?, ?)
		       OR (state = ?
		           AND EXISTS (
		               SELECT 1 FROM json_each(
		                   CASE WHEN resource_state_json IS NULL
		                        THEN '{}'
		                        ELSE resource_state_json END
		               ) slot
		               WHERE json_extract(slot.value, '$.state') = 'pending'
		           )))
		  AND NOT EXISTS (
		      SELECT 1 FROM generation_task_tombstones tombstone
		      WHERE tombstone.task_id = generation_tasks.id
		  )
		  AND next_action_at IS NOT NULL
		  AND next_action_at <= ?
		  AND (lease_until IS NULL OR lease_until < ?)
		ORDER BY next_action_at ASC, rtrim(created_at, 'Z') ASC
		LIMIT ?`,
		generation.TaskQueued,
		generation.TaskRunning,
		generation.TaskMaterializing,
		generation.TaskSucceeded,
		formatTaskTime(now),
		formatTaskTime(now),
		limit,
	)
	if err != nil {
		return nil, err
	}
	ids := make([]generation.TaskID, 0, limit)
	for rows.Next() {
		var id generation.TaskID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	leases := make([]generation.Lease, 0, len(ids))
	// B17 毒行隔离：单行扫描/聚合失败（如历史脏数据 JSON 损坏）只跳过
	// 该行，不拖垮整批领取饿死队列；真实数据库故障仍会由最终 Commit
	// 暴露。
	for _, id := range ids {
		token := secureToken()
		result, err := tx.ExecContext(ctx, `
			UPDATE generation_tasks
			SET lease_owner = ?, lease_token = ?, lease_until = ?
			WHERE id = ?
			  AND NOT EXISTS (
			      SELECT 1 FROM generation_task_tombstones tombstone
			      WHERE tombstone.task_id = generation_tasks.id
			  )
			  AND (lease_until IS NULL OR lease_until < ?)`,
			workerID,
			token,
			formatTaskTime(writeNow.Add(leaseFor)),
			id,
			formatTaskTime(now),
		)
		if err != nil {
			return nil, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected != 1 {
			continue
		}
		task, err := scanTask(tx.QueryRowContext(ctx, taskSelect+` WHERE id = ?`, id))
		if err != nil {
			// 毒行：跳过（无日志设施，交付注释说明语义）
			continue
		}
		aggregate, err := r.loadAggregate(ctx, tx, task)
		if err != nil {
			continue
		}
		leases = append(leases, generation.Lease{Aggregate: aggregate, Token: token})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return leases, nil
}

func (r *Repository) Commit(ctx context.Context, commit generation.Commit) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, failureJSON, err := marshalTaskParts(commit.Task)
	if err != nil {
		return err
	}
	// B17 只写可变列：owner/capability/offering/input/request_hash/
	// idempotency_key 是创建期不变列，提交期不再回写，防止租约内聚合
	// 被并发目录修订意外改写身份字段。
	query := `
		UPDATE generation_tasks SET
			state = ?, progress = ?,
			failure_json = ?, current_attempt_id = ?, materialize_failures = ?,
			next_action_at = ?, version = ?, updated_at = ?, finished_at = ?`
	args := []any{
		commit.Task.State,
		commit.Task.Progress,
		failureJSON,
		nullString(string(commit.Task.CurrentAttemptID)),
		commit.Task.MaterializeFailures,
		nullTaskTime(commit.Task.NextActionAt),
		commit.Task.Version,
		formatTime(commit.Task.UpdatedAt),
		nullTimePointer(commit.Task.FinishedAt),
	}
	// 0022 资源态列非覆盖纪律（§6.5）：只有显式置 PersistResources 的
	// 资源态写者（成功提交初始化/合并更新/deadline finalizer）可写该列。
	if commit.PersistResources {
		resourcesJSON, err := generation.EncodeResourceStatesForColumn(commit.Task.Resources)
		if err != nil {
			return err
		}
		query += `, resource_state_json = ?`
		args = append(args, resourcesJSON)
	}
	if commit.ClearLease {
		query += `, lease_owner = NULL, lease_token = NULL, lease_until = NULL`
	}
	query += ` WHERE id = ? AND version = ?
		AND NOT EXISTS (
			SELECT 1 FROM generation_task_tombstones tombstone
			WHERE tombstone.task_id = generation_tasks.id
		)`
	args = append(args, commit.Task.ID, commit.ExpectedVersion)
	if commit.LeaseToken != "" {
		query += ` AND lease_token = ? AND lease_until > ?`
		args = append(args, commit.LeaseToken, formatTaskTime(commit.Task.UpdatedAt))
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return generation.RepositoryConflict
	}

	if commit.Attempt != nil {
		if err := upsertAttempt(ctx, tx, *commit.Attempt); err != nil {
			return err
		}
	}
	if commit.ReplaceClaims {
		if commit.Attempt == nil {
			return errors.New("替换产物声明时缺少 Attempt")
		}
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM generation_artifact_claims WHERE attempt_id = ?`,
			commit.Attempt.ID,
		); err != nil {
			return err
		}
		for _, claim := range commit.Claims {
			if err := insertClaim(ctx, tx, commit.Attempt.ID, claim); err != nil {
				return err
			}
		}
	}
	if commit.DispatchReceipt != nil {
		// 回执结算与 observation 推进同事务：当前持久状态必须是同
		// sequence/ack 的 dispatching，否则整个提交回滚（重复/伪造/
		// 陈旧回执零生命周期突变，§4.2）。
		if err := settleDispatchReceipt(ctx, tx, commit.Task.ID, *commit.DispatchReceipt); err != nil {
			return err
		}
	}
	for _, asset := range commit.NewAssets {
		if err := insertAsset(ctx, tx, asset); err != nil {
			return err
		}
	}
	for _, event := range commit.Events {
		if err := insertEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	for _, completion := range commit.ValidatedDeliveryCompletions {
		if err := applyDeliveryCompletion(ctx, tx, completion); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CommitDeliveryCompletion writes one already validated local delivery
// completion in its own transaction. The maintenance command uses it; the
// ordinary materialization path passes the same structure to Commit.
func (r *Repository) CommitDeliveryCompletion(ctx context.Context, completion generation.ValidatedDeliveryCompletion) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := applyDeliveryCompletion(ctx, tx, completion); err != nil {
		return err
	}
	return tx.Commit()
}

// applyDeliveryCompletion completes only the exact failed Task version the
// preparation validated. The ordinary task transition table is not widened:
// this explicit validated path is the sole FAILED -> SUCCEEDED writer.
func applyDeliveryCompletion(ctx context.Context, tx *sql.Tx, completion generation.ValidatedDeliveryCompletion) error {
	task := completion.Task
	attempt := completion.Attempt
	if task.State != generation.TaskSucceeded || task.CurrentAttemptID != attempt.ID ||
		attempt.TaskID != task.ID || attempt.State != generation.AttemptDeliveryCompleted ||
		attempt.DeliveryCompletion == nil || completion.ExpectedVersion <= 0 {
		return errors.New("交付完成提交的身份或状态无效")
	}
	_, failureJSON, err := marshalTaskParts(task)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE generation_tasks SET
			state = ?, progress = ?, failure_json = ?, current_attempt_id = ?,
			materialize_failures = 0, next_action_at = NULL, version = ?, updated_at = ?, finished_at = ?,
			lease_owner = NULL, lease_token = NULL, lease_until = NULL
		WHERE id = ? AND version = ? AND state = ?
		  AND NOT EXISTS (
		      SELECT 1 FROM generation_task_tombstones tombstone
		      WHERE tombstone.task_id = generation_tasks.id
		  )`,
		task.State, task.Progress, failureJSON, task.CurrentAttemptID,
		task.Version, formatTime(task.UpdatedAt), nullTimePointer(task.FinishedAt),
		task.ID, completion.ExpectedVersion, generation.TaskFailed,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return generation.RepositoryConflict
	}
	if err := upsertAttempt(ctx, tx, attempt); err != nil {
		return err
	}
	for _, asset := range completion.Assets {
		if asset.TaskID != task.ID || asset.AttemptID != attempt.ID {
			return errors.New("交付完成资产不属于新 Attempt")
		}
		if err := insertAsset(ctx, tx, asset); err != nil {
			return err
		}
	}
	for _, event := range completion.Events {
		if event.TaskID != task.ID {
			return errors.New("交付完成事件不属于该 Task")
		}
		if err := insertEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	return nil
}

// dispatchCheckpointState 是 generation_attempts.dispatch_checkpoint_json
// 的内核所有结构（§4.2）。状态机：idle/absent --Checkpoint--> dispatching
// --回执 dispatched--> received；dispatching --回执 not_dispatched（有证
// 明未派发）--> idle（sequence 保持单调递增）。
type dispatchCheckpointState struct {
	Sequence int64  `json:"sequence"`
	State    string `json:"state"`
	Ack      string `json:"ack,omitempty"`
}

const (
	dispatchCheckpointDispatching = "dispatching"
	dispatchCheckpointReceived    = "received"
	dispatchCheckpointIdle        = "idle"
)

func loadDispatchCheckpointState(
	ctx context.Context,
	queryer queryer,
	taskID generation.TaskID,
	attemptID generation.AttemptID,
) (dispatchCheckpointState, bool, error) {
	var raw sql.NullString
	err := queryer.QueryRowContext(ctx, `
		SELECT dispatch_checkpoint_json
		FROM generation_attempts
		WHERE id = ? AND task_id = ?`,
		attemptID,
		taskID,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return dispatchCheckpointState{}, false, generation.RepositoryNotFound
	}
	if err != nil {
		return dispatchCheckpointState{}, false, err
	}
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return dispatchCheckpointState{}, false, nil
	}
	var state dispatchCheckpointState
	if err := json.Unmarshal([]byte(raw.String), &state); err != nil {
		return dispatchCheckpointState{}, false, fmt.Errorf("解析 dispatch checkpoint: %w", err)
	}
	return state, true, nil
}

// CommitDispatchCheckpoint 在单事务内执行 §4.2 全谓词栅栏：
// version CAS + lease_token + lease_until>now + 非 tombstone，RowsAffected
// 必须为 1；同一事务写入 Provider 私有意图（opaque_json，格式不变）与
// dispatch_checkpoint_json（dispatching + 新 sequence/ack）。存在未回执
// 的 dispatching 状态时返回 ErrDispatchPending，不授予发送许可。
func (r *Repository) CommitDispatchCheckpoint(
	ctx context.Context,
	write generation.DispatchCheckpointWrite,
) (generation.DispatchCheckpointAcknowledge, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return generation.DispatchCheckpointAcknowledge{}, err
	}
	defer tx.Rollback()

	current, exists, err := loadDispatchCheckpointState(ctx, tx, write.TaskID, write.AttemptID)
	if err != nil {
		return generation.DispatchCheckpointAcknowledge{}, err
	}
	if exists && current.State == dispatchCheckpointDispatching {
		return generation.DispatchCheckpointAcknowledge{}, fmt.Errorf(
			"attempt %s 存在未回执的私有派发检查点（sequence=%d）: %w",
			write.AttemptID,
			current.Sequence,
			generation.ErrDispatchPending,
		)
	}
	next := dispatchCheckpointState{
		Sequence: current.Sequence + 1,
		State:    dispatchCheckpointDispatching,
		Ack:      write.AckToken,
	}
	if next.Ack == "" {
		return generation.DispatchCheckpointAcknowledge{}, errors.New("派发 ack token 不能为空")
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return generation.DispatchCheckpointAcknowledge{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE generation_tasks
		SET version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?
		  AND lease_token = ?
		  AND lease_until > ?
		  AND NOT EXISTS (
		      SELECT 1 FROM generation_task_tombstones tombstone
		      WHERE tombstone.task_id = generation_tasks.id
		  )`,
		formatTime(write.Now),
		write.TaskID,
		write.ExpectedVersion,
		write.LeaseToken,
		formatTaskTime(write.Now),
	)
	if err != nil {
		return generation.DispatchCheckpointAcknowledge{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return generation.DispatchCheckpointAcknowledge{}, err
	}
	if affected != 1 {
		return generation.DispatchCheckpointAcknowledge{}, generation.RepositoryConflict
	}
	attemptResult, err := tx.ExecContext(ctx, `
		UPDATE generation_attempts
		SET opaque_json = ?, dispatch_checkpoint_json = ?, updated_at = ?
		WHERE id = ? AND task_id = ?`,
		nullBytes(write.Intent),
		string(encoded),
		formatTime(write.Now),
		write.AttemptID,
		write.TaskID,
	)
	if err != nil {
		return generation.DispatchCheckpointAcknowledge{}, err
	}
	// 尝试行 UPDATE 必须命中恰好一行（S1）：0 行意味着许可未被持久化就
	// 授予（例如尝试行已被并发删除）——整个事务回滚，不发放发送许可。
	attemptAffected, err := attemptResult.RowsAffected()
	if err != nil {
		return generation.DispatchCheckpointAcknowledge{}, err
	}
	if attemptAffected != 1 {
		return generation.DispatchCheckpointAcknowledge{}, generation.RepositoryConflict
	}
	if err := tx.Commit(); err != nil {
		return generation.DispatchCheckpointAcknowledge{}, err
	}
	return generation.DispatchCheckpointAcknowledge{
		Sequence:   next.Sequence,
		AckToken:   next.Ack,
		NewVersion: write.ExpectedVersion + 1,
	}, nil
}

// settleDispatchReceipt 在回执路径事务内消费检查点：当前状态必须是同
// sequence/ack 的 dispatching；dispatched → received，not_dispatched →
// idle（保留 sequence 单调）。任何不匹配返回 RepositoryConflict 使整个
// 提交回滚。
func settleDispatchReceipt(
	ctx context.Context,
	tx *sql.Tx,
	taskID generation.TaskID,
	settlement generation.DispatchReceiptSettlement,
) error {
	current, exists, err := loadDispatchCheckpointState(ctx, tx, taskID, settlement.AttemptID)
	if err != nil {
		return err
	}
	if !exists ||
		current.State != dispatchCheckpointDispatching ||
		current.Sequence != settlement.Sequence ||
		current.Ack != settlement.AckToken {
		return generation.RepositoryConflict
	}
	next := dispatchCheckpointState{
		Sequence: current.Sequence,
		State:    dispatchCheckpointReceived,
	}
	if settlement.NotDispatched {
		next.State = dispatchCheckpointIdle
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE generation_attempts
		SET dispatch_checkpoint_json = ?
		WHERE id = ? AND task_id = ?`,
		string(encoded),
		settlement.AttemptID,
		taskID,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return generation.RepositoryConflict
	}
	return nil
}

// TaskTombstoned 报告任务是否已有 tombstone 记录（GEN-02 fail-closed）。
func (r *Repository) TaskTombstoned(ctx context.Context, id generation.TaskID) (bool, error) {
	var exists int
	err := r.db.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM generation_task_tombstones WHERE task_id = ?)`,
		id,
	).Scan(&exists)
	return exists == 1, err
}

func (r *Repository) Tombstone(
	ctx context.Context,
	tombstone generation.Tombstone,
) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE generation_tasks
		SET next_action_at = NULL,
		    lease_owner = NULL,
		    lease_token = NULL,
		    lease_until = NULL,
		    version = version + 1,
		    updated_at = ?
		WHERE id = ? AND version = ?
		  AND NOT EXISTS (
		      SELECT 1 FROM generation_task_tombstones existing
		      WHERE existing.task_id = generation_tasks.id
		  )`,
		formatTime(tombstone.DeletedAt),
		tombstone.TaskID,
		tombstone.ExpectedVersion,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		var exists int
		err := tx.QueryRowContext(
			ctx,
			`SELECT 1 FROM generation_tasks WHERE id = ?`,
			tombstone.TaskID,
		).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return generation.RepositoryNotFound
		}
		if err != nil {
			return err
		}
		return generation.RepositoryConflict
	}
	if err := insertEvent(ctx, tx, tombstone.Event); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO generation_task_tombstones (task_id, actor_id, deleted_at)
		VALUES (?, ?, ?)`,
		tombstone.TaskID,
		tombstone.ActorID,
		formatTime(tombstone.DeletedAt),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) Events(
	ctx context.Context,
	owner generation.OwnerID,
	taskID generation.TaskID,
	after int64,
	limit int,
) ([]generation.Event, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT e.sequence, e.task_id, e.event_type, e.data_json, e.created_at
		FROM generation_task_events e
		JOIN generation_tasks t ON t.id = e.task_id
		WHERE e.task_id = ? AND t.owner_id = ? AND e.sequence > ?
		  AND NOT EXISTS (
		      SELECT 1 FROM generation_task_tombstones tombstone
		      WHERE tombstone.task_id = t.id
		  )
		ORDER BY e.sequence ASC
		LIMIT ?`,
		taskID,
		owner,
		after,
		limit,
	)
	if err != nil {
		return nil, err
	}
	result := make([]generation.Event, 0)
	for rows.Next() {
		var event generation.Event
		var dataJSON string
		var createdAt string
		if err := rows.Scan(
			&event.Sequence,
			&event.TaskID,
			&event.Type,
			&dataJSON,
			&createdAt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		event.Data = json.RawMessage(dataJSON)
		event.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		var exists int
		err := r.db.QueryRowContext(
			ctx,
			`SELECT 1 FROM generation_tasks
			 WHERE id = ? AND owner_id = ?
			   AND NOT EXISTS (
			       SELECT 1 FROM generation_task_tombstones tombstone
			       WHERE tombstone.task_id = generation_tasks.id
			   )`,
			taskID,
			owner,
		).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, generation.RepositoryNotFound
		}
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// CreateDerivedInputAsset stores an input asset re-encoded from another one
// together with its source, method and source size (#276).
func (r *Repository) CreateDerivedInputAsset(
	ctx context.Context,
	asset generation.InputAsset,
	derivation generation.InputAssetDerivation,
) error {
	if derivation.SourceAssetID == "" || derivation.Method == "" || derivation.SourceSize <= 0 {
		return errors.New("derived input asset needs its source, method and source size")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO generation_input_assets (
			id, owner_id, storage_key, original_filename, asset_type,
			media_type, size_bytes, sha256, created_at,
			derived_from_asset_id, derivation_method, source_size_bytes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		asset.ID, asset.OwnerID, asset.StorageKey, asset.OriginalFilename,
		asset.AssetType, asset.MediaType, asset.Size, asset.SHA256,
		formatTime(asset.CreatedAt),
		derivation.SourceAssetID, derivation.Method, derivation.SourceSize,
	)
	return err
}

// InputAssetDerivation reads how the owner's derived input asset was made;
// found is false for an ordinary upload and for another owner's asset.
func (r *Repository) InputAssetDerivation(
	ctx context.Context,
	owner generation.OwnerID,
	id generation.AssetID,
) (generation.InputAssetDerivation, bool, error) {
	var source, method sql.NullString
	var size sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		SELECT derived_from_asset_id, derivation_method, source_size_bytes
		FROM generation_input_assets WHERE id = ? AND owner_id = ?`, id, owner,
	).Scan(&source, &method, &size)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !source.Valid) {
		return generation.InputAssetDerivation{}, false, nil
	}
	if err != nil {
		return generation.InputAssetDerivation{}, false, err
	}
	return generation.InputAssetDerivation{
		SourceAssetID: generation.AssetID(source.String),
		Method:        method.String,
		SourceSize:    size.Int64,
	}, true, nil
}

func (r *Repository) CreateInputAsset(
	ctx context.Context,
	asset generation.InputAsset,
) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO generation_input_assets (
			id, owner_id, storage_key, original_filename, asset_type,
			media_type, size_bytes, sha256, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		asset.ID,
		asset.OwnerID,
		asset.StorageKey,
		asset.OriginalFilename,
		asset.AssetType,
		asset.MediaType,
		asset.Size,
		asset.SHA256,
		formatTime(asset.CreatedAt),
	)
	return err
}

func (r *Repository) GetInputAsset(
	ctx context.Context,
	id generation.AssetID,
) (generation.InputAsset, error) {
	var asset generation.InputAsset
	var createdAt string
	err := r.db.QueryRowContext(ctx, `
		SELECT id, owner_id, storage_key, original_filename, asset_type,
		       media_type, size_bytes, sha256, created_at
		FROM generation_input_assets
		WHERE id = ?`,
		id,
	).Scan(
		&asset.ID,
		&asset.OwnerID,
		&asset.StorageKey,
		&asset.OriginalFilename,
		&asset.AssetType,
		&asset.MediaType,
		&asset.Size,
		&asset.SHA256,
		&createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return generation.InputAsset{}, &generation.Error{
			Code:      generation.ErrNotFound,
			Operation: "get_input_asset",
		}
	}
	if err != nil {
		return generation.InputAsset{}, err
	}
	asset.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return generation.InputAsset{}, err
	}
	return asset, nil
}

func (r *Repository) LoadInputAssets(
	ctx context.Context,
	owner generation.OwnerID,
	ids []generation.AssetID,
) (map[generation.AssetID]generation.InputAsset, error) {
	result := make(map[generation.AssetID]generation.InputAsset, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	placeholders := make([]string, len(ids))
	inputArgs := make([]any, 0, len(ids)+1)
	inputArgs = append(inputArgs, owner)
	for index, id := range ids {
		placeholders[index] = "?"
		inputArgs = append(inputArgs, id)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, owner_id, storage_key, original_filename, asset_type,
		       media_type, size_bytes, sha256, created_at
		FROM generation_input_assets
		WHERE owner_id = ? AND id IN (`+strings.Join(placeholders, ",")+`)`,
		inputArgs...,
	)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var asset generation.InputAsset
		var createdAt string
		if err := rows.Scan(
			&asset.ID,
			&asset.OwnerID,
			&asset.StorageKey,
			&asset.OriginalFilename,
			&asset.AssetType,
			&asset.MediaType,
			&asset.Size,
			&asset.SHA256,
			&createdAt,
		); err != nil {
			return nil, err
		}
		asset.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, err
		}
		result[asset.ID] = asset
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	outputArgs := make([]any, 0, len(ids)+1)
	outputArgs = append(outputArgs, owner)
	for _, id := range ids {
		outputArgs = append(outputArgs, id)
	}
	rows, err = r.db.QueryContext(ctx, `
		SELECT asset.id, task.owner_id, asset.storage_key, asset.media_type,
		       asset.size_bytes, asset.sha256, asset.created_at,
		       asset.role, asset.variant
		FROM generation_assets AS asset
		JOIN generation_tasks AS task ON task.id = asset.task_id
		WHERE task.owner_id = ? AND task.state = 'SUCCEEDED'
		  AND NOT EXISTS (
		    SELECT 1 FROM generation_task_tombstones AS tombstone
		    WHERE tombstone.task_id = task.id
		  )
		  AND asset.id IN (`+strings.Join(placeholders, ",")+`)`,
		outputArgs...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var asset generation.InputAsset
		var createdAt string
		if err := rows.Scan(
			&asset.ID,
			&asset.OwnerID,
			&asset.StorageKey,
			&asset.MediaType,
			&asset.Size,
			&asset.SHA256,
			&createdAt,
			&asset.Role,
			&asset.Variant,
		); err != nil {
			return nil, err
		}
		asset.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, err
		}
		result[asset.ID] = asset
	}
	return result, rows.Err()
}

func (r *Repository) Close() error {
	return r.db.Close()
}

var _ generation.InputAssetRepository = (*Repository)(nil)

const taskSelect = `
	SELECT
		id, owner_id, capability, offering_json, input_json, request_hash,
		idempotency_key, state, progress, failure_json, current_attempt_id,
		materialize_failures, next_action_at, version, created_at, updated_at, finished_at,
		resource_state_json, session_id,native_quote_json,
		EXISTS (SELECT 1 FROM generation_attempts completion_attempt
			WHERE completion_attempt.task_id = generation_tasks.id
			  AND completion_attempt.delivery_completion_json IS NOT NULL)
	FROM generation_tasks`

type scanner interface {
	Scan(...any) error
}

func scanTask(row scanner) (generation.Task, error) {
	var task generation.Task
	var offeringJSON string
	var inputJSON string
	var failureJSON sql.NullString
	var currentAttempt sql.NullString
	var nextAction sql.NullString
	var createdAt string
	var updatedAt string
	var finishedAt sql.NullString
	var resourcesJSON sql.NullString
	var sessionID sql.NullString
	var nativeQuoteJSON sql.NullString
	var deliveryCompleted int
	err := row.Scan(
		&task.ID,
		&task.OwnerID,
		&task.Capability,
		&offeringJSON,
		&inputJSON,
		&task.RequestHash,
		&task.IdempotencyKey,
		&task.State,
		&task.Progress,
		&failureJSON,
		&currentAttempt,
		&task.MaterializeFailures,
		&nextAction,
		&task.Version,
		&createdAt,
		&updatedAt,
		&finishedAt,
		&resourcesJSON,
		&sessionID, &nativeQuoteJSON, &deliveryCompleted,
	)
	if err != nil {
		return generation.Task{}, err
	}
	task.DeliveryCompleted = deliveryCompleted != 0
	if err := json.Unmarshal([]byte(offeringJSON), &task.Offering); err != nil {
		return generation.Task{}, fmt.Errorf("解析 Offering snapshot: %w", err)
	}
	if nativeQuoteJSON.Valid {
		if err := json.Unmarshal([]byte(nativeQuoteJSON.String), &task.NativeQuote); err != nil {
			return generation.Task{}, err
		}
		if err := task.NativeQuote.Validate(); err != nil {
			return generation.Task{}, err
		}
	} else if task.Offering.NativePricing != nil {
		return generation.Task{}, errors.New("native Task quote missing")
	}
	task.Input = json.RawMessage(inputJSON)
	task.SessionID = sessionID.String
	if failureJSON.Valid && strings.TrimSpace(failureJSON.String) != "null" {
		task.Failure = &generation.Failure{}
		if err := json.Unmarshal([]byte(failureJSON.String), task.Failure); err != nil {
			return generation.Task{}, fmt.Errorf("解析 Task failure: %w", err)
		}
	}
	if currentAttempt.Valid {
		task.CurrentAttemptID = generation.AttemptID(currentAttempt.String)
	}
	if nextAction.Valid {
		task.NextActionAt, err = parseTime(nextAction.String)
		if err != nil {
			return generation.Task{}, err
		}
	}
	if resourcesJSON.Valid {
		task.Resources, err = generation.DecodeResourceStatesFromColumn([]byte(resourcesJSON.String))
		if err != nil {
			return generation.Task{}, err
		}
	}
	task.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return generation.Task{}, err
	}
	task.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return generation.Task{}, err
	}
	if finishedAt.Valid {
		value, err := parseTime(finishedAt.String)
		if err != nil {
			return generation.Task{}, err
		}
		task.FinishedAt = &value
	}
	task.Assets = []generation.Asset{}
	return task, nil
}

func (r *Repository) loadAggregate(
	ctx context.Context,
	queryer queryer,
	task generation.Task,
) (generation.Aggregate, error) {
	aggregate := generation.Aggregate{Task: task}
	var err error
	task.Assets, err = r.loadAssets(ctx, queryer, task.ID)
	if err != nil {
		return generation.Aggregate{}, err
	}
	aggregate.Task = task
	if task.CurrentAttemptID == "" {
		return aggregate, nil
	}
	attempt, err := loadAttempt(ctx, queryer, task.CurrentAttemptID)
	if err != nil {
		return generation.Aggregate{}, err
	}
	aggregate.CurrentAttempt = &attempt
	aggregate.Task.DeliveryCompleted = attempt.DeliveryCompletion != nil
	aggregate.Claims, err = loadClaims(ctx, queryer, attempt.ID)
	if err != nil {
		return generation.Aggregate{}, err
	}
	return aggregate, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (r *Repository) loadAssets(
	ctx context.Context,
	queryer queryer,
	taskID generation.TaskID,
) ([]generation.Asset, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT id, task_id, attempt_id, role, variant, required, ordinal, storage_key,
		       media_type, size_bytes, sha256, created_at, delivery_profile
		FROM generation_assets
		WHERE task_id = ?
		ORDER BY role ASC, ordinal ASC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets := make([]generation.Asset, 0)
	for rows.Next() {
		var asset generation.Asset
		var required int
		var createdAt string
		if err := rows.Scan(
			&asset.ID,
			&asset.TaskID,
			&asset.AttemptID,
			&asset.Role,
			&asset.Variant,
			&required,
			&asset.Ordinal,
			&asset.StorageKey,
			&asset.MediaType,
			&asset.Size,
			&asset.SHA256,
			&createdAt,
			&asset.DeliveryProfile,
		); err != nil {
			return nil, err
		}
		asset.Required = required != 0
		asset.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}

func loadAttempt(
	ctx context.Context,
	queryer queryer,
	id generation.AttemptID,
) (generation.Attempt, error) {
	var attempt generation.Attempt
	var offeringJSON string
	var upstreamProvider sql.NullString
	var upstreamJob sql.NullString
	var failureJSON sql.NullString
	var opaqueJSON sql.NullString
	var deliveryCompletionJSON sql.NullString
	var startedAt string
	var updatedAt string
	var finishedAt sql.NullString
	var observedAt sql.NullString
	err := queryer.QueryRowContext(ctx, `
		SELECT id, task_id, ordinal, offering_json, state, submission_key,
		       upstream_provider_id, upstream_job_id, failure_json, opaque_json,
		       last_observation_key, last_observation_sequence, observation_failures, last_observation_at, last_observation_phase,
		       started_at, updated_at, finished_at, delivery_completion_json
		FROM generation_attempts WHERE id = ?`,
		id,
	).Scan(
		&attempt.ID,
		&attempt.TaskID,
		&attempt.Ordinal,
		&offeringJSON,
		&attempt.State,
		&attempt.SubmissionKey,
		&upstreamProvider,
		&upstreamJob,
		&failureJSON,
		&opaqueJSON,
		&attempt.LastObservationKey,
		&attempt.LastObservationSequence,
		&attempt.ObservationFailures,
		&observedAt,
		&attempt.LastObservationPhase,
		&startedAt,
		&updatedAt,
		&finishedAt,
		&deliveryCompletionJSON,
	)
	if err != nil {
		return generation.Attempt{}, err
	}
	if err := json.Unmarshal([]byte(offeringJSON), &attempt.Offering); err != nil {
		return generation.Attempt{}, err
	}
	if upstreamProvider.Valid && upstreamJob.Valid {
		attempt.UpstreamJob = &generation.UpstreamJob{
			ProviderID: upstreamProvider.String,
			RemoteID:   upstreamJob.String,
		}
	}
	if failureJSON.Valid && strings.TrimSpace(failureJSON.String) != "null" {
		attempt.Failure = &generation.Failure{}
		if err := json.Unmarshal([]byte(failureJSON.String), attempt.Failure); err != nil {
			return generation.Attempt{}, err
		}
	}
	if opaqueJSON.Valid {
		attempt.Opaque = json.RawMessage(opaqueJSON.String)
	}
	if deliveryCompletionJSON.Valid && strings.TrimSpace(deliveryCompletionJSON.String) != "" {
		attempt.DeliveryCompletion = &generation.DeliveryCompletion{}
		if err := json.Unmarshal([]byte(deliveryCompletionJSON.String), attempt.DeliveryCompletion); err != nil {
			return generation.Attempt{}, err
		}
	}
	attempt.StartedAt, err = parseTime(startedAt)
	if err != nil {
		return generation.Attempt{}, err
	}
	attempt.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return generation.Attempt{}, err
	}
	if finishedAt.Valid {
		value, err := parseTime(finishedAt.String)
		if err != nil {
			return generation.Attempt{}, err
		}
		attempt.FinishedAt = &value
	}
	if observedAt.Valid {
		attempt.LastObservationAt, err = parseTime(observedAt.String)
		if err != nil {
			return generation.Attempt{}, err
		}
	}
	return attempt, nil
}

func loadClaims(
	ctx context.Context,
	queryer queryer,
	attemptID generation.AttemptID,
) ([]generation.ArtifactClaim, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT claim_id, role, variant, locator, inline_data, media_type, expires_at, metadata_json, stored_asset_json
		FROM generation_artifact_claims
		WHERE attempt_id = ?
		ORDER BY claim_id ASC`,
		attemptID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	claims := make([]generation.ArtifactClaim, 0)
	for rows.Next() {
		var claim generation.ArtifactClaim
		var inline []byte
		var expiresAt sql.NullString
		var metadata sql.NullString
		var storedAsset sql.NullString
		if err := rows.Scan(
			&claim.ID,
			&claim.Role,
			&claim.Variant,
			&claim.Locator,
			&inline,
			&claim.MediaType,
			&expiresAt,
			&metadata,
			&storedAsset,
		); err != nil {
			return nil, err
		}
		claim.Inline = inline
		if expiresAt.Valid {
			value, err := parseTime(expiresAt.String)
			if err != nil {
				return nil, err
			}
			claim.ExpiresAt = &value
		}
		if metadata.Valid {
			claim.Metadata = json.RawMessage(metadata.String)
		}
		if storedAsset.Valid {
			claim.StoredAsset = &generation.Asset{}
			if err := json.Unmarshal([]byte(storedAsset.String), claim.StoredAsset); err != nil {
				return nil, err
			}
		}
		claims = append(claims, claim)
	}
	return claims, rows.Err()
}

func upsertAttempt(ctx context.Context, tx *sql.Tx, attempt generation.Attempt) error {
	offeringJSON, err := json.Marshal(attempt.Offering)
	if err != nil {
		return err
	}
	failureJSON, err := marshalOptional(attempt.Failure)
	if err != nil {
		return err
	}
	var upstreamProvider any
	var upstreamJob any
	if attempt.UpstreamJob != nil {
		upstreamProvider = attempt.UpstreamJob.ProviderID
		upstreamJob = attempt.UpstreamJob.RemoteID
	}
	var deliveryCompletion any
	if attempt.DeliveryCompletion != nil {
		encoded, err := json.Marshal(attempt.DeliveryCompletion)
		if err != nil {
			return err
		}
		deliveryCompletion = string(encoded)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO generation_attempts (
			id, task_id, ordinal, offering_json, state, submission_key,
			upstream_provider_id, upstream_job_id, failure_json, opaque_json,
			last_observation_key, last_observation_sequence, observation_failures, last_observation_at, last_observation_phase, started_at, updated_at, finished_at, delivery_completion_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			state = excluded.state,
			upstream_provider_id = excluded.upstream_provider_id,
			upstream_job_id = excluded.upstream_job_id,
			failure_json = excluded.failure_json,
			opaque_json = excluded.opaque_json,
			last_observation_key = excluded.last_observation_key,
 last_observation_sequence = excluded.last_observation_sequence,
 observation_failures = excluded.observation_failures,
 last_observation_at = excluded.last_observation_at,
 last_observation_phase = excluded.last_observation_phase,
			updated_at = excluded.updated_at,
			finished_at = excluded.finished_at,
			delivery_completion_json = COALESCE(excluded.delivery_completion_json, generation_attempts.delivery_completion_json)`,
		attempt.ID,
		attempt.TaskID,
		attempt.Ordinal,
		string(offeringJSON),
		attempt.State,
		attempt.SubmissionKey,
		upstreamProvider,
		upstreamJob,
		failureJSON,
		nullBytes(attempt.Opaque),
		attempt.LastObservationKey,
		attempt.LastObservationSequence,
		attempt.ObservationFailures,
		nullTime(attempt.LastObservationAt),
		attempt.LastObservationPhase,
		formatTime(attempt.StartedAt),
		formatTime(attempt.UpdatedAt),
		nullTimePointer(attempt.FinishedAt),
		deliveryCompletion,
	)
	if err != nil && isUpstreamJobConflict(err) {
		return fmt.Errorf("%w: %v", generation.RepositoryUpstreamJobConflict, err)
	}
	return err
}

// isUpstreamJobConflict reports the unique-index violation of
// generation_attempts_upstream_job: another Attempt already records this
// (upstream_provider_id, upstream_job_id).
func isUpstreamJobConflict(err error) bool {
	var coded interface{ Code() int }
	if !errors.As(err, &coded) || coded.Code()&0xff != sqliteConstraint {
		return false
	}
	return strings.Contains(err.Error(), "generation_attempts.upstream_job_id")
}

// sqliteConstraint is the primary result code SQLITE_CONSTRAINT.
const sqliteConstraint = 19

func insertClaim(
	ctx context.Context,
	tx *sql.Tx,
	attemptID generation.AttemptID,
	claim generation.ArtifactClaim,
) error {
	if claim.ID == "" {
		claim.ID = secureToken()
	}
	var storedAsset any
	if claim.StoredAsset != nil {
		encoded, err := json.Marshal(claim.StoredAsset)
		if err != nil {
			return err
		}
		storedAsset = string(encoded)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO generation_artifact_claims (
			attempt_id, claim_id, role, variant, locator, inline_data, media_type, expires_at, metadata_json, stored_asset_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		attemptID,
		claim.ID,
		claim.Role,
		claim.Variant,
		claim.Locator,
		nullBytes(claim.Inline),
		claim.MediaType,
		nullTimePointer(claim.ExpiresAt),
		nullBytes(claim.Metadata),
		storedAsset,
	)
	return err
}

func insertAsset(ctx context.Context, tx *sql.Tx, asset generation.Asset) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO generation_assets (
			id, task_id, attempt_id, role, variant, required, ordinal, storage_key,
			media_type, size_bytes, sha256, created_at, delivery_profile
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
	)
	return err
}

func insertEvent(ctx context.Context, tx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, event generation.Event) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO generation_task_events (task_id, event_type, data_json, created_at)
		VALUES (?, ?, ?, ?)`,
		event.TaskID,
		event.Type,
		string(event.Data),
		formatTime(event.CreatedAt),
	)
	return err
}

func marshalTaskParts(task generation.Task) (string, any, error) {
	offeringJSON, err := json.Marshal(task.Offering)
	if err != nil {
		return "", nil, err
	}
	failureJSON, err := marshalOptional(task.Failure)
	if err != nil {
		return "", nil, err
	}
	return string(offeringJSON), failureJSON, nil
}

func marshalOptional(value *generation.Failure) (any, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

// formatTaskTime renders generation task schedule/lease columns as a fixed
// nine-digit UTC instant. RFC3339Nano trims trailing fractional zeros, so its
// byte order is not time order ("...52.588Z" > "...52.5881Z"); the fixed width
// keeps SQLite TEXT predicates and the next_action_at ordering exact to 1ns.
// 0061 normalizes historical schedule/lease rows; chat_choices (0064) uses it
// from the start; other columns and tables keep the previous encoding.
func formatTaskTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func nullTaskTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return formatTaskTime(value)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("解析数据库时间 %q: %w", value, err)
	}
	return parsed.UTC(), nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return formatTime(value)
}

func nullTimePointer(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return formatTime(*value)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func secureToken() string {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		panic(fmt.Sprintf("生成租约 token 失败: %v", err))
	}
	return hex.EncodeToString(buffer)
}

func encodeCursor(createdAt time.Time, id generation.TaskID) string {
	raw := formatTime(createdAt) + "\x00" + string(id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(value string) (time.Time, generation.TaskID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.SplitN(string(raw), "\x00", 2)
	if len(parts) != 2 || parts[1] == "" {
		return time.Time{}, "", errors.New("cursor 结构无效")
	}
	createdAt, err := parseTime(parts[0])
	if err != nil {
		return time.Time{}, "", err
	}
	return createdAt, generation.TaskID(parts[1]), nil
}

func nativeQuoteColumn(q *generation.NativeQuote) any {
	if q == nil {
		return nil
	}
	raw, _ := json.Marshal(q)
	return string(raw)
}
