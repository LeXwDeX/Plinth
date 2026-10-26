package etl

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// legacyTaskStateMapping 把旧 TaskStatus（TS 侧 constants.ts 的
// 9 个枚举）映射到原生终态。进行中状态一律落终态 FAILED（中断），
// 不接续在途上游作业（spec 既定决策）。
type legacyTaskStateMapping struct {
	state       generation.TaskState
	attempt     generation.AttemptState
	failure     *generation.Failure
	interrupted bool
}

var interruptedFailure = &generation.Failure{
	Class:      generation.FailureInternal,
	SafeDetail: "生产切换时任务尚未完成，已标记为中断",
}

var expiredFailure = &generation.Failure{
	Class:      generation.FailureTimedOut,
	SafeDetail: "旧任务已过期",
}

var legacyTaskStates = map[string]legacyTaskStateMapping{
	"SUCCEEDED": {
		state:   generation.TaskSucceeded,
		attempt: generation.AttemptUpstreamSucceeded,
	},
	"FAILED": {
		state:   generation.TaskFailed,
		attempt: generation.AttemptFailed,
	},
	"CANCELED": {
		state:   generation.TaskCanceled,
		attempt: generation.AttemptCanceled,
	},
	"EXPIRED": {
		state:   generation.TaskFailed,
		attempt: generation.AttemptFailed,
		failure: expiredFailure,
	},
	"PENDING":            interruptedMapping(),
	"IN_PROGRESS":        interruptedMapping(),
	"PROCESSING":         interruptedMapping(),
	"UPSTREAM_COMPLETED": interruptedMapping(),
	"RETRY_PENDING":      interruptedMapping(),
}

func interruptedMapping() legacyTaskStateMapping {
	return legacyTaskStateMapping{
		state:       generation.TaskFailed,
		attempt:     generation.AttemptFailed,
		failure:     interruptedFailure,
		interrupted: true,
	}
}

// taskDomainReport 是任务域的对账数据。
type taskDomainReport struct {
	ledgerRows        int
	cacheSource       int
	cacheLoaded       int
	cacheMerged       int
	cacheArchived     int
	snapshotSource    int
	snapshotLoaded    int
	snapshotMerged    int
	credits           int
	migrated          int
	states            map[generation.TaskState]int
	interrupted       int
	skipped           []string
	excluded          []string
	archived          []string
	orphanSnapshotIDs []string
}

func (report taskDomainReport) String(sourceRows, sourceCredits, targetCredits int) string {
	var builder strings.Builder
	fmt.Fprintf(&builder,
		"任务域: 账本源 %d 行 / 迁移任务 %d 个 / 跳过 %d 个 / 金额合计 %d\n",
		sourceRows, report.migrated,
		len(report.skipped), sourceCredits,
	)
	if len(report.excluded) > 0 {
		fmt.Fprintf(
			&builder,
			"迁移排除: 明确排除 %d 个非业务缓存哨兵\n",
			len(report.excluded),
		)
		for _, detail := range report.excluded {
			fmt.Fprintf(&builder, "  明确排除: %s\n", detail)
		}
	}
	fmt.Fprintf(
		&builder,
		"旧任务辅表消费: task_cache 源 %d / 合并 %d / "+
			"task_input_snapshot 源 %d / 合并 %d / 隔离归档 %d\n",
		report.cacheSource,
		report.cacheMerged,
		report.snapshotSource,
		report.snapshotMerged,
		len(report.archived),
	)
	for index, detail := range report.archived {
		if index == maximumAssetReportIssues {
			fmt.Fprintf(
				&builder,
				"  隔离归档明细: 另有 %d 条省略\n",
				len(report.archived)-maximumAssetReportIssues,
			)
			break
		}
		fmt.Fprintf(&builder, "  隔离归档: %s\n", detail)
	}
	fmt.Fprintf(
		&builder,
		"账本金额对账: 源 %d / 目标导入 %d / 差额 %d\n",
		sourceCredits,
		targetCredits,
		targetCredits-sourceCredits,
	)
	fmt.Fprintf(&builder,
		"状态映射分布: SUCCEEDED=%d FAILED=%d(含中断 %d) CANCELED=%d\n",
		report.states[generation.TaskSucceeded],
		report.states[generation.TaskFailed],
		report.interrupted,
		report.states[generation.TaskCanceled],
	)
	for _, detail := range report.skipped {
		fmt.Fprintf(&builder, "  跳过: %s\n", detail)
	}
	return builder.String()
}

type legacyLedgerRow struct {
	id          string
	taskID      string
	ownerID     string
	userName    string
	userEmail   string
	taskType    string
	providerID  string
	credits     int
	status      string
	params      string
	resultURLs  string
	retryCount  int
	maxRetries  int
	nextRetryAt *time.Time
	createdAt   time.Time
	finishedAt  *time.Time
}

// legacyLedgerGroup 是同一 taskId 的任务聚合输入。逐笔 rows 永久保留；
// Task Offering 的预计金额仅是这些账本行的合计投影。
type legacyLedgerGroup struct {
	taskID     string
	ownerID    string
	taskType   string
	providerID string
	params     string
	credits    int
	status     string
	createdAt  time.Time
	finishedAt *time.Time
	rows       []legacyLedgerRow
}

type legacyCacheRow struct {
	taskType   string
	status     string
	providerID string
	data       string
	cachedAt   time.Time
	updatedAt  time.Time
}

type legacySnapshotRow struct {
	capability string
	providerID string
	params     string
	inputFiles string
	createdAt  time.Time
}

// legacyTaskProductSchema 把 ETL 所需的 Schema 细节封装在一个内部模块内，
// 调用者只需消费归一表、语义参数和精确排除查询。
type legacyTaskProductSchema struct {
	aliases             map[string]generation.Capability
	semanticParameters  map[generation.Capability][]generation.Parameter
	outputRequirements  map[generation.Capability][]generation.AssetRequirement
	cacheOnlyExclusions map[string]map[string]string
}

func (schema legacyTaskProductSchema) cacheOnlyExclusionReason(
	taskID string,
	externalType string,
) (string, bool) {
	types, exists := schema.cacheOnlyExclusions[taskID]
	if !exists {
		return "", false
	}
	reason, exists := types[externalType]
	return reason, exists
}

// loadLegacyTasks 合并旧三表（usage_ledger + task_cache +
// task_input_snapshot）为原生任务导入行。规则：
//   - usage_ledger 是任务主集（旧后台与用户历史都以账本为主源），
//     无账本归属的 cache/snapshot 孤儿跳过并进明细；仅 Product Schema
//     runtime 精确声明的非业务缓存哨兵可审计排除
//   - 状态真源优先 task_cache.status（TS 侧状态机真源），
//     无缓存行回退 ledger.taskStatus；未知状态中止
//   - capability 优先快照声明，否则 ledger.taskType 经 Product
//     Schema task_stream.aliases 归一；无法归一中止
func loadLegacyTasks(
	db legacyQuery,
) (
	[]sqlite.GenerationTaskImport,
	[]sqlite.LegacyMigrationRecordImport,
	taskDomainReport,
	error,
) {
	schema, err := taskProductSchema()
	if err != nil {
		return nil, nil, taskDomainReport{}, err
	}
	return loadLegacyTasksWithSchema(db, schema)
}

func loadLegacyTasksWithSchema(db legacyQuery, schema legacyTaskProductSchema) ([]sqlite.GenerationTaskImport, []sqlite.LegacyMigrationRecordImport, taskDomainReport, error) {
	report := taskDomainReport{states: map[generation.TaskState]int{}}
	groups, order, ledgerRows, err := loadLegacyLedgerGroups(db)
	if err != nil {
		return nil, nil, report, err
	}
	report.ledgerRows = ledgerRows
	caches, cacheSource, cacheLoaded, err := loadLegacyTaskCaches(db)
	if err != nil {
		return nil, nil, report, err
	}
	report.cacheLoaded = cacheLoaded
	report.cacheSource = cacheSource
	snapshots, orphanSnapshotIDs, snapshotSource, snapshotLoaded, err := loadLegacyTaskSnapshots(db, groups)
	if err != nil {
		return nil, nil, report, err
	}
	report.snapshotLoaded = snapshotLoaded
	report.snapshotSource = snapshotSource
	report.orphanSnapshotIDs = orphanSnapshotIDs
	imports := []sqlite.GenerationTaskImport{}
	archives := []sqlite.LegacyMigrationRecordImport{}
	for _, taskID := range order {
		group := groups[taskID]
		if caches[taskID] != nil {
			report.cacheMerged++
		}
		if snapshots[taskID] != nil {
			report.snapshotMerged++
		}
		item, mapping, err := buildTaskImport(
			group, caches[taskID], snapshots[taskID],
			schema.aliases,
			schema.semanticParameters,
			schema.outputRequirements,
		)
		if err != nil {
			return nil, nil, report, err
		}
		imports = append(imports, item)
		report.migrated++
		report.credits += group.credits
		report.states[item.Task.State]++
		if mapping.interrupted {
			report.interrupted++
		}
	}
	for taskID, cache := range caches {
		if _, exists := groups[taskID]; !exists {
			reason := "无账本归属，无法安全伪造用户所有权"
			if declaredReason, excluded := schema.cacheOnlyExclusionReason(
				taskID,
				cache.taskType,
			); excluded {
				reason = declaredReason
				report.excluded = append(report.excluded, fmt.Sprintf(
					"任务 %s（旧类型 %s）：%s",
					taskID,
					cache.taskType,
					declaredReason,
				))
			}
			report.archived = append(report.archived, fmt.Sprintf(
				"task_cache/%s：%s",
				taskID,
				reason,
			))
			archive, archiveErr := archiveLegacyTaskCache(
				taskID,
				cache,
				reason,
			)
			if archiveErr != nil {
				return nil, nil, report, archiveErr
			}
			archives = append(archives, archive)
			report.cacheArchived++
		}
	}
	for _, taskID := range orphanSnapshotIDs {
		_, inCache := caches[taskID]
		reason := "无账本归属，无法安全关联到原生任务"
		if inCache {
			reason = "仅与无账本 task_cache 同 ID，无法安全关联到原生任务"
		}
		report.archived = append(report.archived, fmt.Sprintf(
			"task_input_snapshot/%s：%s",
			taskID,
			reason,
		))
	}
	sort.Strings(report.skipped)
	sort.Strings(report.excluded)
	sort.Strings(report.archived)
	sort.Slice(archives, func(left, right int) bool {
		if archives[left].SourceTable == archives[right].SourceTable {
			return archives[left].SourceID < archives[right].SourceID
		}
		return archives[left].SourceTable < archives[right].SourceTable
	})
	return imports, archives, report, nil
}

func archiveLegacyTaskCache(
	taskID string,
	cache *legacyCacheRow,
	reason string,
) (sqlite.LegacyMigrationRecordImport, error) {
	if !json.Valid([]byte(cache.data)) {
		return sqlite.LegacyMigrationRecordImport{}, fmt.Errorf(
			"孤立 task_cache/%s 的 data 不是合法 JSON",
			taskID,
		)
	}
	payload, err := json.Marshal(struct {
		TaskID     string          `json:"taskId"`
		TaskType   string          `json:"taskType"`
		Status     string          `json:"status"`
		ProviderID string          `json:"providerId"`
		Data       json.RawMessage `json:"data"`
		CachedAt   string          `json:"cachedAt"`
		UpdatedAt  string          `json:"updatedAt"`
	}{
		TaskID:     taskID,
		TaskType:   cache.taskType,
		Status:     cache.status,
		ProviderID: cache.providerID,
		Data:       json.RawMessage(cache.data),
		CachedAt:   cache.cachedAt.Format(time.RFC3339Nano),
		UpdatedAt:  cache.updatedAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return sqlite.LegacyMigrationRecordImport{}, fmt.Errorf(
			"编码孤立 task_cache/%s: %w",
			taskID,
			err,
		)
	}
	return sqlite.LegacyMigrationRecordImport{
		SourceTable: "task_cache",
		SourceID:    taskID,
		Reason:      reason,
		Payload:     payload,
	}, nil
}

func archiveLegacyTaskSnapshot(
	taskID string,
	snapshot *legacySnapshotRow,
	reason string,
) (sqlite.LegacyMigrationRecordImport, error) {
	if !json.Valid([]byte(snapshot.params)) ||
		!json.Valid([]byte(snapshot.inputFiles)) {
		return sqlite.LegacyMigrationRecordImport{}, fmt.Errorf(
			"孤立 task_input_snapshot/%s 的 JSON 字段无效",
			taskID,
		)
	}
	payload, err := json.Marshal(struct {
		TaskID      string          `json:"taskId"`
		Capability  string          `json:"capability"`
		ProviderID  string          `json:"providerId"`
		InputParams json.RawMessage `json:"inputParams"`
		InputFiles  json.RawMessage `json:"inputFiles"`
		CreatedAt   string          `json:"createdAt"`
	}{
		TaskID:      taskID,
		Capability:  snapshot.capability,
		ProviderID:  snapshot.providerID,
		InputParams: json.RawMessage(snapshot.params),
		InputFiles:  json.RawMessage(snapshot.inputFiles),
		CreatedAt:   snapshot.createdAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return sqlite.LegacyMigrationRecordImport{}, fmt.Errorf(
			"编码孤立 task_input_snapshot/%s: %w",
			taskID,
			err,
		)
	}
	return sqlite.LegacyMigrationRecordImport{
		SourceTable: "task_input_snapshot",
		SourceID:    taskID,
		Reason:      reason,
		Payload:     payload,
	}, nil
}

func importOrphanTaskSnapshots(
	ctx context.Context,
	db legacyQuery,
	target *sqlite.Repository,
	taskIDs []string,
) (sqlite.ImportStats, error) {
	var total sqlite.ImportStats
	for _, taskID := range taskIDs {
		var snapshot legacySnapshotRow
		var createdAt any
		var hasCache bool
		err := db.QueryRowContext(ctx, `
			SELECT snapshot.capability, snapshot.providerId,
			       snapshot.inputParams, snapshot.inputFiles,
			       snapshot.createdAt, cache.taskId IS NOT NULL
			FROM task_input_snapshot AS snapshot
			LEFT JOIN task_cache AS cache ON cache.taskId = snapshot.taskId
			WHERE snapshot.taskId = ?`, taskID).Scan(
			&snapshot.capability, &snapshot.providerID,
			&snapshot.params, &snapshot.inputFiles, &createdAt,
			&hasCache,
		)
		if err != nil {
			return total, fmt.Errorf("读取孤立任务快照 %s: %w", taskID, err)
		}
		if snapshot.createdAt, err = requireLegacyTime(
			taskID, "createdAt", createdAt,
		); err != nil {
			return total, err
		}
		reason := "无账本归属，无法安全关联到原生任务"
		if hasCache {
			reason = "仅与无账本 task_cache 同 ID，无法安全关联到原生任务"
		}
		record, err := archiveLegacyTaskSnapshot(taskID, &snapshot, reason)
		if err != nil {
			return total, err
		}
		stats, err := target.ImportLegacyMigrationRecords(
			ctx,
			[]sqlite.LegacyMigrationRecordImport{record},
		)
		if err != nil {
			return total, err
		}
		total.Migrated += stats.Migrated
		total.Skipped += stats.Skipped
	}
	return total, nil
}

// legacyTerminalStatuses / legacyActiveStatuses 复刻 TS 侧
// constants.ts 的 TERMINAL_STATUSES / ACTIVE_STATUSES（旧数据
// 词汇解析，非产品内容）。
var legacyTerminalStatuses = map[string]bool{
	"SUCCEEDED": true, "FAILED": true, "CANCELED": true, "EXPIRED": true,
}

var legacyActiveStatuses = map[string]bool{
	"IN_PROGRESS": true, "PROCESSING": true, "UPSTREAM_COMPLETED": true,
}

// legacyTaskStatus 复刻旧栈状态决议规则（src/lib/
// task-status-resolver.ts 的 resolveTaskStatus）：
// ledger 处于 RETRY_PENDING 时以 ledger 为准（cache 可能存着
// 旧的 FAILED），否则 cache 终态优先，再否则 cache 活跃态仅在
// ledger 空/PENDING 时生效，最后回退 ledger。
func legacyTaskStatus(
	group legacyLedgerGroup,
	cache *legacyCacheRow,
) string {
	if group.status == "RETRY_PENDING" {
		return group.status
	}
	cacheStatus := ""
	if cache != nil {
		cacheStatus = strings.TrimSpace(cache.status)
	}
	if legacyTerminalStatuses[cacheStatus] {
		return cacheStatus
	}
	if legacyActiveStatuses[cacheStatus] &&
		(group.status == "" || group.status == "PENDING") {
		return cacheStatus
	}
	if group.status != "" {
		return group.status
	}
	return cacheStatus
}

func buildTaskImport(
	group legacyLedgerGroup,
	cache *legacyCacheRow,
	snapshot *legacySnapshotRow,
	aliases map[string]generation.Capability,
	semanticParameters map[generation.Capability][]generation.Parameter,
	outputRequirements map[generation.Capability][]generation.AssetRequirement,
) (sqlite.GenerationTaskImport, legacyTaskStateMapping, error) {
	status := legacyTaskStatus(group, cache)
	mapping, known := legacyTaskStates[status]
	if !known {
		return sqlite.GenerationTaskImport{}, mapping, fmt.Errorf(
			"任务 %s 的旧状态 %q 无法识别，拒绝猜测映射",
			group.taskID, status,
		)
	}
	capability, err := resolveLegacyCapability(group, snapshot, aliases)
	if err != nil {
		return sqlite.GenerationTaskImport{}, mapping, err
	}
	providerID := group.providerID
	if cache != nil && cache.providerID != "" {
		providerID = cache.providerID
	}
	if snapshot != nil && snapshot.providerID != "" {
		providerID = snapshot.providerID
	}
	input := group.params
	if snapshot != nil && strings.TrimSpace(snapshot.params) != "" {
		input = snapshot.params
	}
	if err := requireLegacyJSONObject(
		group.taskID,
		"任务输入参数",
		input,
	); err != nil {
		return sqlite.GenerationTaskImport{}, mapping, err
	}
	progress := 0
	if mapping.state == generation.TaskSucceeded {
		progress = 100
	}
	updatedAt := group.createdAt
	if group.finishedAt != nil && group.finishedAt.After(updatedAt) {
		updatedAt = *group.finishedAt
	}
	if cache != nil && cache.updatedAt.After(updatedAt) {
		updatedAt = cache.updatedAt
	}
	task := generation.Task{
		ID:         generation.TaskID(group.taskID),
		OwnerID:    generation.OwnerID(group.ownerID),
		Capability: capability,
		Offering: generation.Offering{
			ID: generation.OfferingID(
				"legacy/" + providerID + "/" + string(capability),
			),
			ProviderID:       providerID,
			Capability:       capability,
			EstimatedCredits: group.credits,
			// 语义参数声明来自 Product Schema，任务历史投影
			// 靠它从 input_json 里按 semantic 定位 prompt。
			Parameters: semanticParameters[capability],
			Outputs: cloneLegacyAssetRequirements(
				outputRequirements[capability],
			),
		},
		Input:      json.RawMessage(input),
		State:      mapping.state,
		Progress:   progress,
		Failure:    mapping.failure,
		Version:    1,
		CreatedAt:  group.createdAt,
		UpdatedAt:  updatedAt,
		FinishedAt: group.finishedAt,
	}
	item := sqlite.GenerationTaskImport{Task: task}
	for index, row := range group.rows {
		if err := requireLegacyJSONObject(
			row.id,
			"账本 params",
			row.params,
		); err != nil {
			return sqlite.GenerationTaskImport{}, mapping, err
		}
		if err := requireLegacyStringArray(
			row.id,
			"账本 resultUrls",
			row.resultURLs,
		); err != nil {
			return sqlite.GenerationTaskImport{}, mapping, err
		}
		rowMapping, known := legacyTaskStates[row.status]
		if !known {
			return sqlite.GenerationTaskImport{}, mapping, fmt.Errorf(
				"账本 %s 的旧状态 %q 无法识别，拒绝猜测映射",
				row.id,
				row.status,
			)
		}
		ordinal := index + 1
		attempt := sqlite.GenerationAttemptImport{
			ID: generation.AttemptID(fmt.Sprintf(
				"%s-attempt-%d",
				group.taskID,
				ordinal,
			)),
			Ordinal: ordinal,
			Offering: generation.Offering{
				ID: generation.OfferingID(
					"legacy/" + row.providerID + "/" + string(capability),
				),
				ProviderID:       row.providerID,
				Capability:       capability,
				EstimatedCredits: row.credits,
				Parameters:       semanticParameters[capability],
				Outputs: cloneLegacyAssetRequirements(
					outputRequirements[capability],
				),
			},
			State:         rowMapping.attempt,
			SubmissionKey: "legacy-ledger:" + row.id,
			Failure:       rowMapping.failure,
			StartedAt:     row.createdAt,
			UpdatedAt:     row.createdAt,
			FinishedAt:    row.finishedAt,
		}
		if row.finishedAt != nil {
			attempt.UpdatedAt = *row.finishedAt
		}
		isCurrent := index == len(group.rows)-1
		if isCurrent {
			attempt.State = mapping.attempt
			attempt.Failure = mapping.failure
			attempt.UpdatedAt = updatedAt
			attempt.FinishedAt = group.finishedAt
			if providerID != "" {
				// 旧栈用上游 Job ID 充当 taskId（ADR-0001 背景），
				// 只挂在当前 Attempt，避免伪造多个相同上游作业。
				attempt.UpstreamJob = &generation.UpstreamJob{
					ProviderID: providerID,
					RemoteID:   group.taskID,
				}
			}
			if cache != nil && json.Valid([]byte(cache.data)) {
				attempt.Opaque = json.RawMessage(cache.data)
			}
		}
		item.Attempts = append(item.Attempts, attempt)
		item.UsageLedgerEntries = append(
			item.UsageLedgerEntries,
			sqlite.GenerationUsageLedgerImport{
				ID:               row.id,
				TaskID:           generation.TaskID(row.taskID),
				UserID:           generation.OwnerID(row.ownerID),
				UserName:         row.userName,
				UserEmail:        row.userEmail,
				TaskType:         row.taskType,
				ProviderID:       row.providerID,
				EstimatedCredits: row.credits,
				TaskStatus:       row.status,
				Params:           json.RawMessage(row.params),
				ResultURLs:       json.RawMessage(row.resultURLs),
				RetryCount:       row.retryCount,
				MaxRetries:       row.maxRetries,
				NextRetryAt:      row.nextRetryAt,
				CreatedAt:        row.createdAt,
				FinishedAt:       row.finishedAt,
			},
		)
	}
	return item, mapping, nil
}

func cloneLegacyAssetRequirements(
	source []generation.AssetRequirement,
) []generation.AssetRequirement {
	result := append([]generation.AssetRequirement(nil), source...)
	for index := range result {
		result[index].MediaTypes = append(
			[]string(nil),
			source[index].MediaTypes...,
		)
	}
	return result
}

func requireLegacyJSONObject(
	recordID string,
	field string,
	raw string,
) error {
	var object map[string]any
	if err := json.Unmarshal([]byte(raw), &object); err != nil ||
		object == nil {
		return fmt.Errorf(
			"%s 的 %s 不是 JSON 对象，拒绝导入",
			recordID,
			field,
		)
	}
	return nil
}

func requireLegacyStringArray(
	recordID string,
	field string,
	raw string,
) error {
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil ||
		values == nil {
		return fmt.Errorf(
			"%s 的 %s 不是字符串数组，拒绝导入",
			recordID,
			field,
		)
	}
	return nil
}

func resolveLegacyCapability(
	group legacyLedgerGroup,
	snapshot *legacySnapshotRow,
	aliases map[string]generation.Capability,
) (generation.Capability, error) {
	if snapshot != nil {
		capability := generation.Capability(
			strings.TrimSpace(snapshot.capability),
		)
		if capability.Valid() {
			return capability, nil
		}
	}
	if capability, exists := aliases[group.taskType]; exists {
		return capability, nil
	}
	return "", fmt.Errorf(
		"任务 %s 的旧类型 %q 无法经 Product Schema 别名归一",
		group.taskID, group.taskType,
	)
}

// taskProductSchema 从 Product Schema 读取 ETL 真源（ETL 不得自建第二
// 词汇表）：任务类型归一、语义参数，以及精确的非业务哨兵排除声明。
func taskProductSchema() (legacyTaskProductSchema, error) {
	product, err := catalog.LoadDefault()
	if err != nil {
		return legacyTaskProductSchema{}, fmt.Errorf(
			"加载 Product Schema: %w",
			err,
		)
	}
	return taskProductSchemaFromCatalog(product), nil
}

func taskProductSchemaFromCatalog(product *catalog.Catalog) legacyTaskProductSchema {
	application := product.Application()
	aliases := map[string]generation.Capability{}
	for _, alias := range application.TaskStream.Aliases {
		aliases[alias.ExternalType] = alias.Capability
	}
	cacheOnlyExclusions := map[string]map[string]string{}
	for _, exclusion := range application.Runtime.LegacyMigration.
		CacheOnlyTaskExclusions {
		if cacheOnlyExclusions[exclusion.TaskID] == nil {
			cacheOnlyExclusions[exclusion.TaskID] = map[string]string{}
		}
		cacheOnlyExclusions[exclusion.TaskID][exclusion.ExternalType] =
			exclusion.Reason
	}
	semanticParameters := map[generation.Capability][]generation.Parameter{}
	outputRequirements := map[generation.Capability][]generation.AssetRequirement{}
	seen := map[generation.Capability]map[string]bool{}
	for _, offering := range product.List("") {
		// 旧任务只有 role 级产物身份，不能把今天一个 Offering 的六种
		// 必需格式追溯要求到旧任务。仍从 Catalog 取得角色与可选资源，
		// 但将当前格式槽位收敛为旧栈每个必需角色至少一份的契约。
		if _, exists := outputRequirements[offering.Capability]; !exists {
			outputRequirements[offering.Capability] =
				legacyRoleRequirements(offering.Outputs)
		}
		for _, parameter := range offering.Parameters {
			if parameter.Semantic == "" {
				continue
			}
			if seen[offering.Capability] == nil {
				seen[offering.Capability] = map[string]bool{}
			}
			if seen[offering.Capability][parameter.Name] {
				continue
			}
			seen[offering.Capability][parameter.Name] = true
			semanticParameters[offering.Capability] = append(
				semanticParameters[offering.Capability],
				generation.Parameter{
					Name:     parameter.Name,
					Semantic: parameter.Semantic,
				},
			)
		}
	}
	return legacyTaskProductSchema{
		aliases:             aliases,
		semanticParameters:  semanticParameters,
		outputRequirements:  outputRequirements,
		cacheOnlyExclusions: cacheOnlyExclusions,
	}
}

func legacyRoleRequirements(outputs []generation.AssetRequirement) []generation.AssetRequirement {
	result := make([]generation.AssetRequirement, 0, len(outputs))
	seen := make(map[string]bool)
	for _, output := range outputs {
		if output.Delivery == generation.AssetDeliveryBackground ||
			output.Delivery == generation.AssetDeliveryOnDemand ||
			(output.Minimum != nil && *output.Minimum == 0) ||
			seen[output.Role] {
			continue
		}
		seen[output.Role] = true
		one := 1
		result = append(result, generation.AssetRequirement{
			Role: output.Role, Minimum: &one, MaxBytes: output.MaxBytes,
		})
	}
	for _, output := range outputs {
		if output.Delivery != generation.AssetDeliveryBackground &&
			output.Delivery != generation.AssetDeliveryOnDemand {
			continue
		}
		result = append(result, cloneLegacyAssetRequirements([]generation.AssetRequirement{output})...)
	}
	return result
}

func loadLegacyLedgerGroups(
	db legacyQuery,
) (map[string]legacyLedgerGroup, []string, int, error) {
	rows, err := db.Query(
		`SELECT id, taskId, userId, userName, userEmail, taskType,
			providerId, estimatedCredits, taskStatus, params, resultUrls,
			retryCount, maxRetries, nextRetryAt, createdAt, finishedAt
		 FROM usage_ledger ORDER BY createdAt, id`,
	)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("读取旧 usage_ledger 表: %w", err)
	}
	defer func() { _ = rows.Close() }()
	groups := map[string]legacyLedgerGroup{}
	order := []string{}
	total := 0
	for rows.Next() {
		var (
			row                                legacyLedgerRow
			nextRetryAt, createdAt, finishedAt any
		)
		if err := rows.Scan(
			&row.id,
			&row.taskID,
			&row.ownerID,
			&row.userName,
			&row.userEmail,
			&row.taskType,
			&row.providerID,
			&row.credits,
			&row.status,
			&row.params,
			&row.resultURLs,
			&row.retryCount,
			&row.maxRetries,
			&nextRetryAt,
			&createdAt,
			&finishedAt,
		); err != nil {
			return nil, nil, 0, fmt.Errorf("读取旧账本行: %w", err)
		}
		if strings.TrimSpace(row.taskID) == "" {
			return nil, nil, 0, fmt.Errorf("旧账本 %s 的 taskId 为空", row.id)
		}
		total++
		created, err := requireLegacyTime(
			row.id,
			"createdAt",
			createdAt,
		)
		if err != nil {
			return nil, nil, 0, err
		}
		row.createdAt = created
		row.nextRetryAt, err = optionalLegacyTime(
			row.id,
			"nextRetryAt",
			nextRetryAt,
		)
		if err != nil {
			return nil, nil, 0, err
		}
		row.finishedAt, err = optionalLegacyTime(
			row.id,
			"finishedAt",
			finishedAt,
		)
		if err != nil {
			return nil, nil, 0, err
		}
		if existing, exists := groups[row.taskID]; exists {
			if existing.ownerID != row.ownerID ||
				existing.taskType != row.taskType {
				return nil, nil, 0, fmt.Errorf(
					"任务 %s 的账本归属或类型不一致（%s/%s 与 %s/%s）",
					row.taskID,
					existing.ownerID,
					existing.taskType,
					row.ownerID,
					row.taskType,
				)
			}
			existing.credits += row.credits
			existing.rows = append(existing.rows, row)
			existing.status = row.status
			existing.params = row.params
			if row.providerID != "" {
				existing.providerID = row.providerID
			}
			if row.finishedAt != nil && (existing.finishedAt == nil ||
				row.finishedAt.After(*existing.finishedAt)) {
				existing.finishedAt = row.finishedAt
			}
			groups[row.taskID] = existing
			continue
		}
		groups[row.taskID] = legacyLedgerGroup{
			taskID:     row.taskID,
			ownerID:    row.ownerID,
			taskType:   row.taskType,
			providerID: row.providerID,
			params:     row.params,
			credits:    row.credits,
			status:     row.status,
			createdAt:  created,
			finishedAt: row.finishedAt,
			rows:       []legacyLedgerRow{row},
		}
		order = append(order, row.taskID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, 0, fmt.Errorf("遍历旧 usage_ledger 表: %w", err)
	}
	return groups, order, total, nil
}

func loadLegacyTaskCaches(
	db legacyQuery,
) (map[string]*legacyCacheRow, int, int, error) {
	var sourceCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_cache`).Scan(&sourceCount); err != nil {
		return nil, 0, 0, fmt.Errorf("统计旧 task_cache 表: %w", err)
	}
	rows, err := db.Query(
		`SELECT taskId, taskType, status, providerId, data,
		        cachedAt, updatedAt
		 FROM task_cache`,
	)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("读取旧 task_cache 表: %w", err)
	}
	defer func() { _ = rows.Close() }()
	caches := map[string]*legacyCacheRow{}
	loaded := 0
	for rows.Next() {
		var taskID string
		var cache legacyCacheRow
		var cachedAt, updatedAt any
		if err := rows.Scan(
			&taskID, &cache.taskType, &cache.status,
			&cache.providerID, &cache.data, &cachedAt, &updatedAt,
		); err != nil {
			return nil, 0, 0, fmt.Errorf("读取旧任务缓存行: %w", err)
		}
		loaded++
		if cache.cachedAt, err = requireLegacyTime(
			taskID, "cachedAt", cachedAt,
		); err != nil {
			return nil, 0, 0, err
		}
		if cache.updatedAt, err = requireLegacyTime(
			taskID, "updatedAt", updatedAt,
		); err != nil {
			return nil, 0, 0, err
		}
		caches[taskID] = &cache
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, fmt.Errorf("遍历旧 task_cache 表: %w", err)
	}
	return caches, sourceCount, loaded, nil
}

func loadLegacyTaskSnapshots(
	db legacyQuery,
	groups map[string]legacyLedgerGroup,
) (map[string]*legacySnapshotRow, []string, int, int, error) {
	var sourceCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_input_snapshot`).Scan(&sourceCount); err != nil {
		return nil, nil, 0, 0, fmt.Errorf("统计旧 task_input_snapshot 表: %w", err)
	}
	rows, err := db.Query(
		`SELECT taskId, capability, providerId, inputParams,
		        inputFiles, createdAt
		 FROM task_input_snapshot`,
	)
	if err != nil {
		return nil, nil, 0, 0, fmt.Errorf("读取旧 task_input_snapshot 表: %w", err)
	}
	defer func() { _ = rows.Close() }()
	snapshots := map[string]*legacySnapshotRow{}
	orphanIDs := []string{}
	loaded := 0
	for rows.Next() {
		var taskID string
		var snapshot legacySnapshotRow
		var params, inputFiles sql.RawBytes
		var createdAt any
		if err := rows.Scan(
			&taskID, &snapshot.capability,
			&snapshot.providerID, &params,
			&inputFiles, &createdAt,
		); err != nil {
			return nil, nil, 0, 0, fmt.Errorf("读取旧任务快照行: %w", err)
		}
		loaded++
		if _, inLedger := groups[taskID]; !inLedger {
			orphanIDs = append(orphanIDs, taskID)
			continue
		}
		snapshot.params = string(params)
		snapshot.inputFiles = string(inputFiles)
		if snapshot.createdAt, err = requireLegacyTime(
			taskID, "createdAt", createdAt,
		); err != nil {
			return nil, nil, 0, 0, err
		}
		snapshots[taskID] = &snapshot
	}
	if err := rows.Err(); err != nil {
		return nil, nil, 0, 0, fmt.Errorf("遍历旧 task_input_snapshot 表: %w", err)
	}
	sort.Strings(orphanIDs)
	return snapshots, orphanIDs, sourceCount, loaded, nil
}
