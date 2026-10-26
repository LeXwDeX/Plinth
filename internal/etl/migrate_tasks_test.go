package etl_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/etl/etltest"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// seedLegacyTask 播种一个完整的旧任务：账本行 + 缓存行 + 输入快照。
func seedLegacyTask(
	t *testing.T,
	fixture *etltest.Fixture,
	taskID, taskType, ledgerStatus, cacheStatus string,
	credits int,
) {
	t.Helper()
	fixture.Exec(t,
		`INSERT INTO usage_ledger (
			id, userId, userName, userEmail, taskType, taskId,
			providerId, estimatedCredits, taskStatus, params,
			resultUrls, createdAt, finishedAt
		 ) VALUES (?, 'legacy-user', 'Legacy User', 'legacy@example.test',
			?, ?, 'meshy', ?, ?, '{"prompt":"seed"}', '[]',
			'2025-06-01T00:00:00+00:00', '2025-06-01T00:05:00+00:00')`,
		"ledger-"+taskID, taskType, taskID, credits, ledgerStatus,
	)
	if cacheStatus != "" {
		fixture.Exec(t,
			`INSERT INTO task_cache (
				taskId, taskType, status, providerId, data,
				cachedAt, updatedAt
			 ) VALUES (?, ?, ?, 'meshy', '{"upstream":"payload"}',
				'2025-06-01T00:00:00+00:00', '2025-06-01T00:06:00+00:00')`,
			taskID, taskType, cacheStatus,
		)
	}
}

func seedLegacyImageOutput(
	t *testing.T,
	fixture *etltest.Fixture,
	taskID string,
) {
	t.Helper()
	sourceURL := "/api/gpt-images/" + taskID + ".png"
	if err := os.WriteFile(
		filepath.Join(
			fixture.Root,
			"storage",
			"generated",
			taskID+".png",
		),
		validPNGFixture(t),
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	fixture.Exec(t,
		`UPDATE usage_ledger SET resultUrls = ? WHERE taskId = ?`,
		`["`+sourceURL+`"]`,
		taskID,
	)
	fixture.Exec(t,
		`UPDATE task_cache SET data = ? WHERE taskId = ?`,
		`{"image_urls":["`+sourceURL+`"],"progress":100}`,
		taskID,
	)
}

func openTargetDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestMigrateTasksStatusMapping 覆盖旧 TaskStatus 全部 9 个枚举：
// 终态直映；EXPIRED 落 FAILED；5 个进行中状态一律落终态 FAILED（中断）。
func TestMigrateTasksStatusMapping(t *testing.T) {
	fixture := etltest.NewFixture(t)
	expected := map[string]generation.TaskState{
		"SUCCEEDED":          generation.TaskSucceeded,
		"FAILED":             generation.TaskFailed,
		"CANCELED":           generation.TaskCanceled,
		"EXPIRED":            generation.TaskFailed,
		"PENDING":            generation.TaskFailed,
		"IN_PROGRESS":        generation.TaskFailed,
		"PROCESSING":         generation.TaskFailed,
		"UPSTREAM_COMPLETED": generation.TaskFailed,
		"RETRY_PENDING":      generation.TaskFailed,
	}
	for legacyStatus := range expected {
		seedLegacyTask(t, fixture,
			"task-"+legacyStatus, "text-to-image",
			legacyStatus, legacyStatus, 1,
		)
	}
	// 原生 SUCCEEDED 表示上游完成且本地产物已固化。为状态直映
	// 测试提供一份真实历史产物；缺字节的降级由资产迁移测试覆盖。
	succeededSource := "/api/gpt-images/task-SUCCEEDED.png"
	if err := os.WriteFile(
		filepath.Join(
			fixture.Root,
			"storage",
			"generated",
			"task-SUCCEEDED.png",
		),
		validPNGFixture(t),
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	fixture.Exec(t,
		`UPDATE usage_ledger SET resultUrls = ?
		 WHERE taskId = 'task-SUCCEEDED'`,
		`["`+succeededSource+`"]`,
	)
	fixture.Exec(t,
		`UPDATE task_cache SET data = ?
		 WHERE taskId = 'task-SUCCEEDED'`,
		`{"image_urls":["`+succeededSource+`"],"progress":100}`,
	)
	target := migrateTarget(t)
	code, output := runETL(t,
		"migrate", "--legacy-root", fixture.Root, "--target-db", target,
	)
	if code != 0 {
		t.Fatalf("迁移应成功，输出:\n%s", output)
	}
	repository, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	for legacyStatus, state := range expected {
		task, err := repository.Get(
			t.Context(), "legacy-user",
			generation.TaskID("task-"+legacyStatus),
		)
		if err != nil {
			t.Fatalf("读取任务 task-%s: %v", legacyStatus, err)
		}
		if task.State != state {
			t.Errorf(
				"旧状态 %s 应映射为 %s，实际 %s",
				legacyStatus, state, task.State,
			)
		}
		if !task.State.Terminal() {
			t.Errorf("旧状态 %s 迁移后必须是终态", legacyStatus)
		}
	}
	// 进行中状态必须带"中断"失败说明；正常 FAILED 不伪造失败详情。
	interrupted, err := repository.Get(
		t.Context(), "legacy-user", "task-IN_PROGRESS",
	)
	if err != nil {
		t.Fatal(err)
	}
	if interrupted.Failure == nil ||
		!strings.Contains(interrupted.Failure.SafeDetail, "中断") {
		t.Errorf("进行中任务应携带中断说明: %#v", interrupted.Failure)
	}
	failed, err := repository.Get(
		t.Context(), "legacy-user", "task-FAILED",
	)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Failure != nil {
		t.Errorf("旧 FAILED 任务不应伪造失败详情: %#v", failed.Failure)
	}
}

// TestMigrateTasksFieldFidelity 断言 Task 保留原 ID，
// Capability / Provider / 创建参数 / 时间戳 / 金额逐字段保留。
func TestMigrateTasksFieldFidelity(t *testing.T) {
	fixture := etltest.NewFixture(t)
	fixture.Exec(t,
		`INSERT INTO usage_ledger (
			id, userId, userName, userEmail, taskType, taskId,
			providerId, estimatedCredits, taskStatus, params,
			resultUrls, createdAt, finishedAt
		 ) VALUES ('ledger-rich', 'owner-a', 'Owner A', 'a@example.test',
			'image-to-3d', 'rich-task', 'tripo', 7, 'FAILED',
			'{"prompt":"ledger params"}', '["https://x/1.glb"]',
			'2025-02-03T04:05:06.789+00:00', '2025-02-03T04:15:06+00:00')`,
	)
	fixture.Exec(t,
		`INSERT INTO task_cache (
			taskId, taskType, status, providerId, data, cachedAt, updatedAt
		 ) VALUES ('rich-task', 'image-to-3d', 'FAILED', 'tripo',
			'{"model_urls":{"glb":"https://x/1.glb"},"progress":100}',
			'2025-02-03T04:05:06+00:00', '2025-02-03T04:15:07+00:00')`,
	)
	fixture.Exec(t,
		`INSERT INTO task_input_snapshot (
			taskId, capability, providerId, inputParams, inputFiles, createdAt
		 ) VALUES ('rich-task', 'image-to-3d', 'tripo',
			'{"prompt":"snapshot params","quality":"high"}',
			'[{"storagePath":"a.png"}]', '2025-02-03T04:05:06+00:00')`,
	)
	// 无快照的 legacy 别名任务：taskType 'animations' 必须经
	// Product Schema task_stream.aliases 归一为 'animation'。
	seedLegacyTask(t, fixture,
		"alias-task", "animations", "FAILED", "FAILED", 2,
	)
	target := migrateTarget(t)
	code, output := runETL(t,
		"migrate", "--legacy-root", fixture.Root, "--target-db", target,
	)
	if code != 0 {
		t.Fatalf("迁移应成功，输出:\n%s", output)
	}
	repository, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	task, err := repository.Get(t.Context(), "owner-a", "rich-task")
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "rich-task" ||
		task.OwnerID != "owner-a" ||
		task.Capability != generation.ImageTo3D ||
		task.Offering.ProviderID != "tripo" ||
		task.Offering.EstimatedCredits != 7 ||
		string(task.Input) != `{"prompt":"snapshot params","quality":"high"}` {
		t.Fatalf("字段保真失败: %#v", task)
	}
	if got := task.CreatedAt.Format("2006-01-02T15:04:05.999Z"); got !=
		"2025-02-03T04:05:06.789Z" {
		t.Errorf("createdAt = %s", got)
	}
	if task.FinishedAt == nil ||
		task.FinishedAt.Format("2006-01-02T15:04:05Z") !=
			"2025-02-03T04:15:06Z" {
		t.Errorf("finishedAt = %v", task.FinishedAt)
	}
	// 旧 task_cache.data 保留为 Attempt Opaque（Provider 私有数据）。
	if !strings.Contains(string(task.ProviderData), "model_urls") {
		t.Errorf("旧缓存 data 未保留: %s", task.ProviderData)
	}
	alias, err := repository.Get(
		t.Context(), "legacy-user", "alias-task",
	)
	if err != nil {
		t.Fatal(err)
	}
	if alias.Capability != generation.Animation {
		t.Errorf(
			"taskType animations 应归一为 animation，实际 %s",
			alias.Capability,
		)
	}
	// 旧栈 taskId 即上游 Job ID：Attempt 保留上游身份（ADR-0001）。
	db := openTargetDatabase(t, target)
	var ordinal int
	var upstreamProvider, upstreamJob string
	if err := db.QueryRow(
		`SELECT ordinal, upstream_provider_id, upstream_job_id
		 FROM generation_attempts WHERE task_id = 'rich-task'`,
	).Scan(&ordinal, &upstreamProvider, &upstreamJob); err != nil {
		t.Fatal(err)
	}
	if ordinal != 1 || upstreamProvider != "tripo" ||
		upstreamJob != "rich-task" {
		t.Errorf(
			"attempt = ordinal %d upstream %s/%s",
			ordinal, upstreamProvider, upstreamJob,
		)
	}
}

// TestMigrateTasksLedgerReconciliation 断言账本金额合计守恒：
// 同一 taskId 的多条账本行金额求和进任务，总额与源账本一致。
func TestMigrateTasksLedgerReconciliation(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedLegacyTask(t, fixture,
		"task-a", "text-to-image", "PENDING", "", 3,
	)
	seedLegacyTask(t, fixture,
		"task-b", "text-to-3d", "FAILED", "FAILED", 5,
	)
	// task-a 的第二条账本行（重复扣费记录），金额必须合并。
	fixture.Exec(t,
		`INSERT INTO usage_ledger (
			id, userId, userName, userEmail, taskType, taskId,
			providerId, estimatedCredits, taskStatus, params,
			resultUrls, createdAt
		 ) VALUES ('ledger-extra', 'legacy-user', 'Legacy User',
			'legacy@example.test', 'text-to-image', 'task-a', 'meshy',
			4, 'FAILED', '{}', '[]', '2025-06-01T00:01:00+00:00')`,
	)
	target := migrateTarget(t)
	code, output := runETL(t,
		"migrate", "--legacy-root", fixture.Root, "--target-db", target,
	)
	if code != 0 {
		t.Fatalf("迁移应成功，输出:\n%s", output)
	}
	db := openTargetDatabase(t, target)
	var taskCount, creditTotal int
	if err := db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(
			CAST(json_extract(offering_json, '$.estimated_credits')
				AS INTEGER)), 0)
		 FROM generation_tasks`,
	).Scan(&taskCount, &creditTotal); err != nil {
		t.Fatal(err)
	}
	if taskCount != 2 {
		t.Errorf("任务数应为 2，实际 %d", taskCount)
	}
	if creditTotal != 3+4+5 {
		t.Errorf("金额合计应为 12，实际 %d", creditTotal)
	}
	var ledgerCount, ledgerCredits, attemptCount int
	if err := db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(estimated_credits), 0)
		 FROM generation_usage_ledger`,
	).Scan(&ledgerCount, &ledgerCredits); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM generation_attempts`,
	).Scan(&attemptCount); err != nil {
		t.Fatal(err)
	}
	if ledgerCount != 3 || ledgerCredits != 12 || attemptCount != 3 {
		t.Fatalf(
			"逐笔账本/Attempt 未完整保留: 账本=%d 金额=%d Attempt=%d",
			ledgerCount,
			ledgerCredits,
			attemptCount,
		)
	}
	rows, err := db.Query(
		`SELECT id FROM generation_usage_ledger ORDER BY created_at, id`,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ledgerIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ledgerIDs = append(ledgerIDs, id)
	}
	if got := strings.Join(ledgerIDs, ","); got !=
		"ledger-task-a,ledger-task-b,ledger-extra" {
		t.Fatalf("账本稳定 ID 未逐笔保留: %s", got)
	}
	attemptRows, err := db.Query(
		`SELECT ordinal,
			CAST(json_extract(offering_json, '$.estimated_credits') AS INTEGER),
			submission_key
		 FROM generation_attempts
		 WHERE task_id = 'task-a'
		 ORDER BY ordinal`,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = attemptRows.Close() }()
	var attempts []string
	for attemptRows.Next() {
		var ordinal, credits int
		var submissionKey string
		if err := attemptRows.Scan(
			&ordinal,
			&credits,
			&submissionKey,
		); err != nil {
			t.Fatal(err)
		}
		attempts = append(
			attempts,
			fmt.Sprintf("%d:%d:%s", ordinal, credits, submissionKey),
		)
	}
	if got := strings.Join(attempts, ","); got !=
		"1:3:legacy-ledger:ledger-task-a,2:4:legacy-ledger:ledger-extra" {
		t.Fatalf("逐笔 Attempt 证据不完整: %s", got)
	}
	repository, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	taskA, err := repository.Get(t.Context(), "legacy-user", "task-a")
	if err != nil {
		t.Fatal(err)
	}
	if taskA.Failure != nil {
		t.Fatalf(
			"Task 当前投影应采用最新 FAILED 账本行，不应保留首行 PENDING 的中断详情: %#v",
			taskA.Failure,
		)
	}
}

// TestMigrateTasksIdempotent 同一 fixture 重跑两次，
// 任务与 Attempt 的行数和内容都不变。
func TestMigrateTasksIdempotent(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedLegacyTask(t, fixture,
		"task-a", "text-to-image", "FAILED", "FAILED", 3,
	)
	seedLegacyTask(t, fixture,
		"task-b", "image-to-3d", "IN_PROGRESS", "IN_PROGRESS", 5,
	)
	target := migrateTarget(t)
	dump := func() string {
		db := openTargetDatabase(t, target)
		var result strings.Builder
		for _, query := range []string{
			`SELECT id, owner_id, capability, state, offering_json,
				input_json, created_at, finished_at
			 FROM generation_tasks ORDER BY id`,
			`SELECT id, task_id, ordinal, state, upstream_job_id,
				opaque_json
			 FROM generation_attempts ORDER BY id`,
		} {
			rows, err := db.Query(query)
			if err != nil {
				t.Fatal(err)
			}
			columns, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				values := make([]any, len(columns))
				pointers := make([]any, len(columns))
				for index := range values {
					pointers[index] = &values[index]
				}
				if err := rows.Scan(pointers...); err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(&result, "%v\n", values)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			_ = rows.Close()
		}
		return result.String()
	}
	if code, output := runETL(t,
		"migrate", "--legacy-root", fixture.Root, "--target-db", target,
	); code != 0 {
		t.Fatalf("首跑应成功，输出:\n%s", output)
	}
	first := dump()
	if code, output := runETL(t,
		"migrate", "--legacy-root", fixture.Root, "--target-db", target,
	); code != 0 {
		t.Fatalf("重跑应成功，输出:\n%s", output)
	}
	if second := dump(); second != first {
		t.Errorf("重跑后内容变化:\n首跑:\n%s\n重跑:\n%s", first, second)
	}
	if lines := strings.Count(first, "\n"); lines != 4 {
		t.Errorf("应有 2 任务 + 2 Attempt 共 4 行，实际 %d:\n%s",
			lines, first)
	}
}

// TestMigrateTasksReportsCounts 对账计数：账本源行数、迁移任务数、
// 状态映射分布与无归属记录的无损隔离归档都进命令输出。
func TestMigrateTasksReportsCounts(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedLegacyTask(t, fixture,
		"task-ok", "text-to-image", "SUCCEEDED", "SUCCEEDED", 1,
	)
	seedLegacyImageOutput(t, fixture, "task-ok")
	seedLegacyTask(t, fixture,
		"task-run", "text-to-3d", "PENDING", "PENDING", 2,
	)
	seedLegacyTask(t, fixture,
		"task-gone", "remesh", "CANCELED", "CANCELED", 0,
	)
	// 无账本归属的缓存孤儿无法安全伪造 owner，原始行进入隔离归档。
	fixture.Exec(t,
		`INSERT INTO task_cache (
			taskId, taskType, status, providerId, data, cachedAt, updatedAt
		 ) VALUES ('orphan-task', 'text-to-image', 'SUCCEEDED', 'meshy',
			'{}', '2025-06-01T00:00:00+00:00', '2025-06-01T00:00:00+00:00')`,
	)
	target := migrateTarget(t)
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", target,
	)
	if code != 0 {
		t.Fatalf("可无损归档的孤儿缓存不应阻断迁移，输出:\n%s", output)
	}
	for _, expected := range []string{
		"任务域: 账本源 3 行 / 迁移任务 3 个 / 跳过 0 个 / 金额合计 3",
		"状态映射分布: SUCCEEDED=1 FAILED=1(含中断 1) CANCELED=1",
		"隔离归档 1",
		"orphan-task",
		"无账本归属",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("输出缺少 %q:\n%s", expected, output)
		}
	}
	db := openTargetDatabase(t, target)
	var sourceTable, sourceID, taskType string
	if err := db.QueryRow(
		`SELECT source_table, source_id,
		        json_extract(payload_json, '$.taskType')
		 FROM legacy_migration_records`,
	).Scan(&sourceTable, &sourceID, &taskType); err != nil {
		t.Fatal(err)
	}
	if sourceTable != "task_cache" ||
		sourceID != "orphan-task" ||
		taskType != "text-to-image" {
		t.Fatalf(
			"孤儿缓存归档不完整: %s/%s type=%s",
			sourceTable,
			sourceID,
			taskType,
		)
	}

	code, output = runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", target,
	)
	if code != 0 {
		t.Fatalf("孤儿缓存归档重跑失败，输出:\n%s", output)
	}
	if !strings.Contains(output, "隔离归档=1") {
		t.Fatalf("重跑未报告既有隔离归档:\n%s", output)
	}
	var archived int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM legacy_migration_records`,
	).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if archived != 1 {
		t.Fatalf("孤儿缓存重跑后归档数 = %d，期望 1", archived)
	}
}

func TestMigrateTasksArchivesOrphanSnapshot(t *testing.T) {
	fixture := etltest.NewFixture(t)
	fixture.Exec(t,
		`INSERT INTO task_input_snapshot (
			taskId, capability, providerId, inputParams, inputFiles, createdAt
		 ) VALUES ('orphan-snapshot', 'image-to-3d', 'tripo',
			'{"prompt":"preserve me"}', '[{"storagePath":"large.png"}]',
			'2025-06-01T00:00:00+00:00')`,
	)
	target := migrateTarget(t)
	code, output := runETL(t,
		"migrate", "--legacy-root", fixture.Root, "--target-db", target,
	)
	if code != 0 {
		t.Fatalf("孤儿快照归档失败，输出:\n%s", output)
	}
	for _, expected := range []string{
		"隔离归档 1",
		"task_input_snapshot/orphan-snapshot",
		"无账本归属",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("输出缺少 %q:\n%s", expected, output)
		}
	}
	db := openTargetDatabase(t, target)
	defer func() { _ = db.Close() }()
	var prompt, storagePath string
	if err := db.QueryRow(
		`SELECT json_extract(payload_json, '$.inputParams.prompt'),
		        json_extract(payload_json, '$.inputFiles[0].storagePath')
		 FROM legacy_migration_records
		 WHERE source_table = 'task_input_snapshot'
		   AND source_id = 'orphan-snapshot'`,
	).Scan(&prompt, &storagePath); err != nil {
		t.Fatal(err)
	}
	if prompt != "preserve me" || storagePath != "large.png" {
		t.Fatalf("孤儿快照 payload 不完整: prompt=%q storagePath=%q", prompt, storagePath)
	}
	code, output = runETL(t,
		"migrate", "--legacy-root", fixture.Root, "--target-db", target,
	)
	if code != 0 || !strings.Contains(output, "隔离归档=1") {
		t.Fatalf("孤儿快照归档重跑应幂等，输出:\n%s", output)
	}
}

func TestMigrateTasksArchivesManyOrphanSnapshots(t *testing.T) {
	fixture := etltest.NewFixture(t)
	for index := 0; index < 251; index++ {
		fixture.Exec(t,
			`INSERT INTO task_input_snapshot (
				taskId, capability, providerId, inputParams, inputFiles, createdAt
			 ) VALUES (?, 'text-to-image', 'meshy', ?, '[]',
				'2025-06-01T00:00:00+00:00')`,
			fmt.Sprintf("orphan-snapshot-%03d", index),
			fmt.Sprintf(`{"index":%d}`, index),
		)
	}
	target := migrateTarget(t)
	code, output := runETL(t,
		"migrate", "--legacy-root", fixture.Root, "--target-db", target,
	)
	if code != 0 {
		t.Fatalf("多条孤儿快照归档失败，输出:\n%s", output)
	}
	db := openTargetDatabase(t, target)
	defer func() { _ = db.Close() }()
	var count, lastIndex int
	if err := db.QueryRow(
		`SELECT COUNT(*), MAX(CAST(json_extract(payload_json, '$.inputParams.index') AS INTEGER))
		 FROM legacy_migration_records
		 WHERE source_table = 'task_input_snapshot'`,
	).Scan(&count, &lastIndex); err != nil {
		t.Fatal(err)
	}
	if count != 251 || lastIndex != 250 {
		t.Fatalf("多条孤儿快照归档不完整: count=%d lastIndex=%d", count, lastIndex)
	}
}

// TestMigrateTasksUsesSchemaAliasForBackgroundRemoval 复刻生产库中的
// background-removal 旧类型，能力归一必须来自 Product Schema。
func TestMigrateTasksUsesSchemaAliasForBackgroundRemoval(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedLegacyTask(t, fixture,
		"background-task", "background-removal", "FAILED", "FAILED", 1,
	)
	target := migrateTarget(t)
	code, output := runETL(t,
		"migrate", "--legacy-root", fixture.Root, "--target-db", target,
	)
	if code != 0 {
		t.Fatalf("background-removal 应按 Schema 别名迁移，输出:\n%s", output)
	}
	repository, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	task, err := repository.Get(
		t.Context(), "legacy-user", "background-task",
	)
	if err != nil {
		t.Fatal(err)
	}
	if task.Capability != generation.BackgroundRemove {
		t.Fatalf(
			"background-removal capability=%q want=%q",
			task.Capability,
			generation.BackgroundRemove,
		)
	}
}

// TestMigrateTasksExcludesSchemaDeclaredDeploymentSentinel 复刻生产库中
// deploy-check/test 缓存哨兵。它没有账本归属且不是用户任务，只能由
// 私有 Product Schema runtime 声明显式排除，普通孤儿仍由既有测试拦截。
func TestMigrateTasksExcludesSchemaDeclaredDeploymentSentinel(t *testing.T) {
	fixture := etltest.NewFixture(t)
	fixture.Exec(t,
		`INSERT INTO task_cache (
			taskId, taskType, status, providerId, data, cachedAt, updatedAt
		 ) VALUES ('deploy-check', 'test', 'SUCCEEDED', 'meshy',
			'{}', '2026-04-01T13:24:06+00:00',
			'2026-07-10T00:40:11+00:00')`,
	)
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", migrateTarget(t),
	)
	if code != 0 {
		t.Fatalf("Schema 声明的部署哨兵应可审计排除，输出:\n%s", output)
	}
	for _, expected := range []string{
		"明确排除 1 个",
		"deploy-check",
		"历史部署健康检查",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("输出缺少 %q:\n%s", expected, output)
		}
	}
}

// TestMigrateTasksRetryPendingOverridesStaleCache 复刻旧栈状态
// 决议规则（task-status-resolver.ts）：ledger 处于 RETRY_PENDING
// 时 cache 里残留的旧 FAILED 不算数，任务按进行中→中断处理。
func TestMigrateTasksRetryPendingOverridesStaleCache(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedLegacyTask(t, fixture,
		"task-retry", "text-to-image", "RETRY_PENDING", "FAILED", 1,
	)
	target := migrateTarget(t)
	code, output := runETL(t,
		"migrate", "--legacy-root", fixture.Root, "--target-db", target,
	)
	if code != 0 {
		t.Fatalf("迁移应成功，输出:\n%s", output)
	}
	if !strings.Contains(output, "FAILED=1(含中断 1)") {
		t.Errorf("RETRY_PENDING 应计入中断分布:\n%s", output)
	}
	repository, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	task, err := repository.Get(
		t.Context(), "legacy-user", "task-retry",
	)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskFailed || task.Failure == nil ||
		!strings.Contains(task.Failure.SafeDetail, "中断") {
		t.Errorf(
			"RETRY_PENDING 应按中断处理: state=%s failure=%#v",
			task.State, task.Failure,
		)
	}
}

// TestMigrateTasksRejectsUnknownStatus 未知旧状态是数据异常，
// 必须中止而不是猜测映射。
func TestMigrateTasksRejectsUnknownStatus(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedLegacyTask(t, fixture,
		"task-weird", "text-to-image", "WEIRD_STATE", "WEIRD_STATE", 1,
	)
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", migrateTarget(t),
	)
	if code != 1 {
		t.Fatalf("未知状态应中止，退出码 %d:\n%s", code, output)
	}
	if !strings.Contains(output, "WEIRD_STATE") {
		t.Errorf("错误信息应包含未知状态值:\n%s", output)
	}
}

// TestMigrateTasksRejectsUnknownTaskType 无法经 Product Schema
// 别名归一的 taskType 必须中止。
func TestMigrateTasksRejectsUnknownTaskType(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedLegacyTask(t, fixture,
		"task-bogus", "made-up-type", "SUCCEEDED", "SUCCEEDED", 1,
	)
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", migrateTarget(t),
	)
	if code != 1 {
		t.Fatalf("未知任务类型应中止，退出码 %d:\n%s", code, output)
	}
	if !strings.Contains(output, "made-up-type") {
		t.Errorf("错误信息应包含未知任务类型:\n%s", output)
	}
}

func TestMigrateTasksRejectsInvalidLedgerJSON(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedLegacyTask(t, fixture,
		"invalid-json-task", "text-to-image", "FAILED", "FAILED", 1,
	)
	fixture.Exec(t,
		`UPDATE usage_ledger
		 SET params = '[]', resultUrls = '{"not":"an array"}'
		 WHERE taskId = 'invalid-json-task'`,
	)
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", migrateTarget(t),
	)
	if code == 0 {
		t.Fatalf("非法账本 JSON 必须拒绝迁移:\n%s", output)
	}
	if !strings.Contains(output, "任务输入参数 不是 JSON 对象") {
		t.Fatalf("非法 JSON 错误不清晰:\n%s", output)
	}
}

// TestMigrateTasksRiggingLegacyRowsUnchangedByPrimaryPrefixReplacement
// 覆盖 §3.4 的 V2 parity：rigging 旧账本行迁移后的状态/能力不随
// W1 目录前缀替换变化。旧任务只知道 role，迁移快照不能追溯要求
// 今天 Rigging 的每个格式槽位，否则历史成功记录会被误判为失败。
func TestMigrateTasksRiggingLegacyRowsUnchangedByPrimaryPrefixReplacement(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedLegacyTask(t, fixture,
		"legacy-rigging-task", "rigging", "FAILED", "FAILED", 2,
	)
	target := migrateTarget(t)
	code, output := runETL(t,
		"migrate", "--legacy-root", fixture.Root, "--target-db", target,
	)
	if code != 0 {
		t.Fatalf("rigging 迁移被目录变更破坏，输出:\n%s", output)
	}
	repository, err := sqlite.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	task, err := repository.Get(
		t.Context(), "legacy-user", "legacy-rigging-task",
	)
	if err != nil {
		t.Fatal(err)
	}
	if task.Capability != generation.Rigging ||
		task.State != generation.TaskFailed {
		t.Fatalf("task = %#v", task)
	}
	if len(task.Offering.Outputs) != 1 ||
		task.Offering.Outputs[0].Role != "model" ||
		task.Offering.Outputs[0].Variant != "" ||
		task.Offering.Outputs[0].VariantPrefix != "" ||
		task.Offering.Outputs[0].Minimum == nil ||
		*task.Offering.Outputs[0].Minimum != 1 {
		t.Fatalf("rigging 历史快照必须保留一份 model 的角色级门槛: %#v", task.Offering.Outputs)
	}
}
