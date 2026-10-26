package etl

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func runMigrate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	legacyRoot := flags.String(
		"legacy-root",
		"",
		"旧 Next.js 版本项目根目录（必填）",
	)
	legacyDatabase := flags.String(
		"legacy-database",
		"",
		"旧 SQLite 数据库路径（可选，默认 <旧根>/storage/data/prod.db）",
	)
	targetDB := flags.String(
		"target-db",
		"",
		"Go 原生库文件路径（必填，不存在则自动建库建表）",
	)
	targetStorage := flags.String(
		"target-storage",
		"",
		"Go 原生内容寻址资产目录（默认与目标库同目录的 storage-go）",
	)
	reportPath := flags.String(
		"report",
		"",
		"对账报告文件（默认 <target-db>.migration-report.txt）",
	)
	manifestPath := flags.String(
		"manifest",
		"",
		"机器可读 ETL completeness manifest（默认 <报告文件>.manifest.json）",
	)
	sourceLossTemplatePath := flags.String(
		"source-loss-template",
		"",
		"输出未批准的历史源损失候选清单（可选，拒绝覆盖已有文件）",
	)
	sourceLossAcknowledgementPath := flags.String(
		"source-loss-acknowledgement",
		"",
		"经人工批准的历史源损失确认清单（可选，权限须仅限 owner）",
	)
	if err := flags.Parse(args); err != nil {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	if *legacyRoot == "" || *targetDB == "" {
		fmt.Fprintln(
			stderr,
			"缺少必填参数 --legacy-root 或 --target-db，不做任何猜测。",
		)
		fmt.Fprint(stderr, usageText)
		return 2
	}
	layout := resolveLegacyLayoutWithDatabase(*legacyRoot, *legacyDatabase)
	if _, failures := precheck(layout); len(failures) > 0 {
		reportPrecheckFailures(stderr, failures)
		return 1
	}
	if strings.TrimSpace(*targetStorage) == "" {
		*targetStorage = filepath.Join(filepath.Dir(*targetDB), "storage-go")
	}
	if strings.TrimSpace(*reportPath) == "" {
		*reportPath = *targetDB + ".migration-report.txt"
	}
	if strings.TrimSpace(*manifestPath) == "" {
		*manifestPath = *reportPath + ".manifest.json"
	}
	if samePath(*reportPath, *targetDB) {
		fmt.Fprintln(stderr, "对账报告路径不能覆盖目标数据库。")
		return 2
	}
	*sourceLossTemplatePath = strings.TrimSpace(*sourceLossTemplatePath)
	*sourceLossAcknowledgementPath = strings.TrimSpace(
		*sourceLossAcknowledgementPath,
	)
	if *sourceLossTemplatePath != "" &&
		*sourceLossAcknowledgementPath != "" {
		fmt.Fprintln(
			stderr,
			"源损失候选清单与已批准确认清单不能在同一次迁移中同时使用。",
		)
		return 2
	}
	for label, candidate := range map[string]string{
		"源损失候选清单": *sourceLossTemplatePath,
		"源损失确认清单": *sourceLossAcknowledgementPath,
	} {
		if candidate != "" &&
			(samePath(candidate, *targetDB) ||
				samePath(candidate, *reportPath)) {
			fmt.Fprintf(
				stderr,
				"%s路径不能覆盖目标数据库或对账报告。\n",
				label,
			)
			return 2
		}
	}
	options := migrationOptions{}
	if *sourceLossTemplatePath != "" ||
		*sourceLossAcknowledgementPath != "" {
		databaseSHA256, hashErr := legacyDatabaseSHA256(
			layout.databasePath,
		)
		if hashErr != nil {
			fmt.Fprintf(stderr, "计算旧数据库指纹失败：%v\n", hashErr)
			return 1
		}
		options.legacyDatabaseSHA256 = databaseSHA256
	}
	if *sourceLossAcknowledgementPath != "" {
		loaded, loadErr := loadSourceLossAcknowledgement(
			*sourceLossAcknowledgementPath,
			options.legacyDatabaseSHA256,
		)
		if loadErr != nil {
			fmt.Fprintf(stderr, "源损失确认清单无效：%v\n", loadErr)
			return 1
		}
		options.sourceLossAcknowledgement = &loaded
	}
	result, err := migrateWithContextOptions(
		context.Background(),
		layout,
		*targetDB,
		*targetStorage,
		options,
	)
	if err != nil {
		failureReport := fmt.Sprintf(
			"迁移目标: %s\n对账结论: 失败\n迁移错误: %v\n",
			*targetDB,
			err,
		)
		if reportErr := writeMigrationReport(
			*reportPath,
			failureReport,
		); reportErr != nil {
			fmt.Fprintf(stderr, "写入失败报告失败：%v\n", reportErr)
		} else {
			fmt.Fprintf(stderr, "对账报告: %s\n", *reportPath)
		}
		fmt.Fprintf(stderr, "迁移失败：%v\n", err)
		return 1
	}
	if err := writeMigrationReport(*reportPath, result.report); err != nil {
		fmt.Fprintf(stderr, "写入对账报告失败：%v\n", err)
		return 1
	}
	if err := writeCompletenessManifest(*manifestPath, result.manifest); err != nil {
		fmt.Fprintf(stderr, "写入 ETL manifest 失败：%v\n", err)
		return 1
	}
	fmt.Fprint(stdout, result.report)
	fmt.Fprintf(stdout, "对账报告: %s\n", *reportPath)
	fmt.Fprintf(stdout, "ETL manifest: %s\n", *manifestPath)
	if *sourceLossTemplatePath != "" {
		if err := writeSourceLossTemplate(
			*sourceLossTemplatePath,
			options.legacyDatabaseSHA256,
			result.sourceLosses,
			result.inputAssetSourceLosses,
		); err != nil {
			fmt.Fprintf(
				stderr,
				"写入源损失候选清单失败：%v\n",
				err,
			)
			return 1
		}
		fmt.Fprintf(
			stdout,
			"源损失候选清单: %s\n",
			*sourceLossTemplatePath,
		)
	}
	if result.failureCount > 0 {
		fmt.Fprintf(
			stderr,
			"迁移数据已提交，但对账未通过（%d 条失败明细）。\n",
			result.failureCount,
		)
		return 1
	}
	return 0
}

type migrationResult struct {
	report                 string
	manifest               completenessManifest
	failureCount           int
	sourceLosses           []taskOutputSourceLoss
	inputAssetSourceLosses []inputAssetSourceLoss
}

type migrationOptions struct {
	legacyDatabaseSHA256      string
	sourceLossAcknowledgement *loadedSourceLossAcknowledgement
}

// migrate 预检通过后执行各原生业务域迁移，并返回逐域对账计数报告。
func migrate(
	layout legacyLayout,
	targetDB string,
	targetStorage string,
) (migrationResult, error) {
	return migrateWithContext(
		context.Background(),
		layout,
		targetDB,
		targetStorage,
	)
}

func migrateWithContext(
	ctx context.Context,
	layout legacyLayout,
	targetDB string,
	targetStorage string,
) (migrationResult, error) {
	return migrateWithContextOptions(
		ctx,
		layout,
		targetDB,
		targetStorage,
		migrationOptions{},
	)
}

func migrateWithContextOptions(
	ctx context.Context,
	layout legacyLayout,
	targetDB string,
	targetStorage string,
	options migrationOptions,
) (migrationResult, error) {
	legacyDB, err := sql.Open("sqlite", readOnlyDSN(layout.databasePath))
	if err != nil {
		return migrationResult{}, fmt.Errorf("打开旧库: %w", err)
	}
	defer func() { _ = legacyDB.Close() }()
	legacy, err := legacyDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return migrationResult{}, fmt.Errorf("创建旧库只读快照: %w", err)
	}
	defer func() { _ = legacy.Rollback() }()
	databaseSHA, err := legacyDatabaseSHA256(layout.databasePath)
	if err != nil {
		return migrationResult{}, fmt.Errorf("计算旧库指纹: %w", err)
	}
	baseline, err := readLegacyBaseline(legacy)
	if err != nil {
		return migrationResult{}, err
	}
	// 源行数独立于逐行读取统计，对账才能暴露读取遗漏。
	var sourceCount int64
	if err := legacy.QueryRow(
		`SELECT COUNT(*) FROM users`,
	).Scan(&sourceCount); err != nil {
		return migrationResult{}, fmt.Errorf("统计旧 users 表行数: %w", err)
	}
	users, err := loadLegacyUsers(legacy)
	if err != nil {
		return migrationResult{}, err
	}
	canvasProjects, canvasNodes, canvasReport, err := loadLegacyCanvas(
		ctx,
		legacy,
	)
	if err != nil {
		return migrationResult{}, err
	}
	tasks, taskArchives, taskReport, err := loadLegacyTasks(legacy)
	if err != nil {
		return migrationResult{}, err
	}
	legacyArchives := append(
		[]sqlite.LegacyMigrationRecordImport(nil),
		taskArchives...,
	)
	legacyArchives = append(legacyArchives, canvasReport.archives...)
	sort.Slice(legacyArchives, func(left, right int) bool {
		if legacyArchives[left].SourceTable ==
			legacyArchives[right].SourceTable {
			return legacyArchives[left].SourceID <
				legacyArchives[right].SourceID
		}
		return legacyArchives[left].SourceTable <
			legacyArchives[right].SourceTable
	})
	chatSessions, chatMessages, chatReport, err := loadLegacyChat(
		ctx,
		legacy,
	)
	if err != nil {
		return migrationResult{}, err
	}
	balanceSnapshots, balanceReport, err := loadLegacyBalanceSnapshots(legacy)
	if err != nil {
		return migrationResult{}, err
	}
	blobs, err := local.New(targetStorage)
	if err != nil {
		return migrationResult{}, fmt.Errorf("打开目标资产目录: %w", err)
	}
	sourceAuditRoot := filepath.Join(layout.storageRoot, "audit")
	targetAuditRoot := filepath.Join(targetStorage, "audit")
	auditEvents, auditReport, err := loadLegacyAuditEvents(
		ctx,
		legacy,
		sourceAuditRoot,
		targetAuditRoot,
	)
	if err != nil {
		return migrationResult{}, err
	}
	migratedAuditFiles, err := materializeLegacyAuditFiles(
		sourceAuditRoot,
		targetAuditRoot,
		&auditReport,
	)
	if err != nil {
		return migrationResult{}, err
	}
	inputAssets, inputAssetReport, err := loadLegacyInputAssets(
		ctx,
		legacy,
		layout.storageRoot,
		blobs,
	)
	if err != nil {
		return migrationResult{}, err
	}
	tasks, outputAssets, outputAssetReport, err := materializeLegacyTaskOutputs(
		ctx,
		legacy,
		layout.storageRoot,
		blobs,
		tasks,
	)
	if err != nil {
		return migrationResult{}, err
	}
	if options.sourceLossAcknowledgement != nil {
		currentDatabaseSHA256, err := legacyDatabaseSHA256(
			layout.databasePath,
		)
		if err != nil {
			return migrationResult{}, err
		}
		if currentDatabaseSHA256 != options.legacyDatabaseSHA256 {
			return migrationResult{}, errors.New(
				"旧数据库在源损失确认后发生变化，拒绝继续",
			)
		}
		if err := applySourceLossAcknowledgement(
			&inputAssetReport,
			&outputAssetReport,
			*options.sourceLossAcknowledgement,
		); err != nil {
			return migrationResult{}, err
		}
	}
	taskReport.states = map[generation.TaskState]int{}
	for _, item := range tasks {
		taskReport.states[item.Task.State]++
	}
	thumbnailFiles, thumbnailBytes, err := measureDirectory(
		filepath.Join(layout.storageRoot, "thumbnails"),
	)
	if err != nil {
		return migrationResult{}, fmt.Errorf("统计旧缩略图目录: %w", err)
	}
	thumbnailReport := fmt.Sprintf(
		"缩略图域: 源 %d 个 / 按需重建 %d 个（旧缓存 %d 字节不复制）\n",
		thumbnailFiles,
		thumbnailFiles,
		thumbnailBytes,
	)
	target, err := sqlite.Open(targetDB)
	if err != nil {
		return migrationResult{}, fmt.Errorf("打开目标库: %w", err)
	}
	defer func() { _ = target.Close() }()
	userImportStats, err := target.ImportAuthenticationUsers(
		ctx, users,
	)
	if err != nil {
		return migrationResult{}, err
	}
	chatImportStats, err := target.ImportChatHistory(
		ctx, chatSessions, chatMessages,
	)
	if err != nil {
		return migrationResult{}, err
	}
	canvasImportStats, err := target.ImportCanvasHistory(
		ctx, canvasProjects, canvasNodes,
	)
	if err != nil {
		return migrationResult{}, err
	}
	administrationImportStats, err := target.ImportAdministrationHistory(
		ctx, balanceSnapshots, auditEvents,
	)
	if err != nil {
		return migrationResult{}, err
	}
	generationImportStats, err := target.ImportGenerationTasksAndAssets(
		ctx, tasks, outputAssets,
	)
	if err != nil {
		return migrationResult{}, err
	}
	inputImportStats, err := target.ImportInputAssets(
		ctx, inputAssets,
	)
	if err != nil {
		return migrationResult{}, err
	}
	legacyArchiveImportStats, err := target.ImportLegacyMigrationRecords(
		ctx,
		legacyArchives,
	)
	if err != nil {
		return migrationResult{}, err
	}
	orphanSnapshotImportStats, err := importOrphanTaskSnapshots(
		ctx,
		legacy,
		target,
		taskReport.orphanSnapshotIDs,
	)
	if err != nil {
		return migrationResult{}, err
	}
	legacyArchiveImportStats.Migrated += orphanSnapshotImportStats.Migrated
	legacyArchiveImportStats.Skipped += orphanSnapshotImportStats.Skipped
	reconciliation, err := target.ReconcileETL(ctx)
	if err != nil {
		return migrationResult{}, err
	}
	auxiliaryTargets, err := countTaskAuxiliaryTargets(ctx, legacy, targetDB)
	if err != nil {
		return migrationResult{}, err
	}

	reconciliationIssues := compareETLReconciliation(
		reconciliation,
		sqlite.ETLReconciliation{
			Users:            len(users),
			Tasks:            baseline.tasks,
			Attempts:         taskReport.ledgerRows,
			UsageLedger:      taskReport.ledgerRows,
			OutputAssets:     len(outputAssets),
			InputAssets:      len(inputAssets),
			ChatSessions:     len(chatSessions),
			ChatMessages:     len(chatMessages),
			CanvasProjects:   len(canvasProjects),
			CanvasNodes:      len(canvasNodes),
			BalanceSnapshots: len(balanceSnapshots),
			AuditEvents:      len(auditEvents),
			LegacyRecords: len(legacyArchives) +
				len(taskReport.orphanSnapshotIDs),
			Credits: baseline.credits,
		},
	)
	casVerification := verifyCASReferences(
		ctx,
		blobs,
		inputAssets,
		outputAssets,
	)
	reconciliationIssues = append(
		reconciliationIssues,
		casVerification.issues...,
	)
	var reconciliationReport strings.Builder
	fmt.Fprintf(
		&reconciliationReport,
		"目标原生行数: 用户=%d 任务=%d Attempt=%d 账本=%d 任务产物=%d "+
			"输入资产=%d 聊天会话=%d 聊天消息=%d Canvas项目=%d "+
			"Canvas节点=%d 余额快照=%d 审计日志=%d 隔离归档=%d\n",
		reconciliation.Users,
		reconciliation.Tasks,
		reconciliation.Attempts,
		reconciliation.UsageLedger,
		reconciliation.OutputAssets,
		reconciliation.InputAssets,
		reconciliation.ChatSessions,
		reconciliation.ChatMessages,
		reconciliation.CanvasProjects,
		reconciliation.CanvasNodes,
		reconciliation.BalanceSnapshots,
		reconciliation.AuditEvents,
		reconciliation.LegacyRecords,
	)
	fmt.Fprintf(
		&reconciliationReport,
		"CAS 对账: 唯一引用 %d 个 / 可读 %d 个 / 失败 %d 个\n",
		casVerification.references,
		casVerification.readable,
		len(casVerification.issues),
	)
	fmt.Fprintf(
		&reconciliationReport,
		"CAS SHA-256 抽验: 样本 %d 个 / 通过 %d 个 / 失败 %d 个\n",
		len(casVerification.samples),
		casVerification.matches,
		len(casVerification.samples)-casVerification.matches,
	)
	for _, sample := range casVerification.samples {
		fmt.Fprintf(
			&reconciliationReport,
			"SHA-256 样本: %s / 期望 %s / 实际 %s / %s\n",
			sample.storageKey,
			sample.expected,
			sample.actual,
			sample.result,
		)
	}
	for _, issue := range reconciliationIssues {
		fmt.Fprintf(&reconciliationReport, "对账失败: %s\n", issue)
	}
	rerunReport := fmt.Sprintf(
		"重跑跳过量: 用户=%d 任务=%d Attempt=%d 账本=%d 任务产物=%d 输入资产=%d "+
			"聊天会话=%d 聊天消息=%d Canvas项目=%d Canvas节点=%d "+
			"余额快照=%d 审计日志=%d 审计文件=%d 隔离归档=%d\n",
		userImportStats.Skipped,
		generationImportStats.Tasks.Skipped,
		generationImportStats.Attempts.Skipped,
		generationImportStats.Ledger.Skipped,
		generationImportStats.Assets.Skipped,
		inputImportStats.Skipped,
		chatImportStats.Sessions.Skipped,
		chatImportStats.Messages.Skipped,
		canvasImportStats.Projects.Skipped,
		canvasImportStats.Nodes.Skipped,
		administrationImportStats.BalanceSnapshots.Skipped,
		administrationImportStats.AuditEvents.Skipped,
		auditReport.fileExisting,
		legacyArchiveImportStats.Skipped,
	)
	failureCount := len(taskReport.skipped) +
		len(chatReport.sessionSkipped) +
		len(chatReport.messageSkipped) +
		len(canvasReport.projectSkipped) +
		len(canvasReport.nodeSkipped) +
		len(balanceReport.skipped) +
		len(auditReport.logSkipped) +
		len(auditReport.fileSkipped) +
		inputAssetReport.unacknowledgedIssueCount() +
		outputAssetReport.unacknowledgedIssueCount()
	failureCount += len(reconciliationIssues)
	conclusion := "通过"
	if failureCount > 0 {
		conclusion = fmt.Sprintf("失败（%d 条明细）", failureCount)
	}
	manifest := buildCompletenessManifest(
		databaseSHA,
		taskReport,
		chatReport,
		canvasReport,
		balanceReport,
		auditReport,
		inputAssetReport,
		outputAssetReport,
		reconciliation,
		casVerification,
		baseline,
		auxiliaryTargets,
		len(users),
		failureCount,
	)
	manifestFailures := completenessManifestFailures(manifest)
	if manifestFailures > 0 {
		failureCount += manifestFailures
		manifest.Overall.FailureCount = failureCount
		manifest.Overall.Conclusion = "fail"
		conclusion = fmt.Sprintf("失败（%d 条明细）", failureCount)
	}
	report := fmt.Sprintf(
		"用户域: 源 %d 行 / 迁移 %d 行 / 跳过 %d 行\n"+
			"%s%s%s%s%s%s%s%s%s%s"+
			"迁移完成: %s\n目标资产目录: %s\n对账结论: %s\n",
		sourceCount, len(users), sourceCount-int64(len(users)),
		taskReport.String(baseline.counts["usage_ledger"], baseline.credits, reconciliation.Credits),
		chatReport.String(len(chatSessions), len(chatMessages)),
		canvasReport.String(len(canvasProjects), len(canvasNodes)),
		balanceReport.String(len(balanceSnapshots)),
		auditReport.String(len(auditEvents), migratedAuditFiles),
		inputAssetReport.String(), outputAssetReport.String(),
		thumbnailReport, reconciliationReport.String(), rerunReport,
		targetDB, targetStorage, conclusion,
	)
	return migrationResult{
		report:                 report,
		manifest:               manifest,
		failureCount:           failureCount,
		sourceLosses:           outputAssetReport.sourceLosses,
		inputAssetSourceLosses: inputAssetReport.sourceLosses,
	}, nil
}

func completenessManifestFailures(manifest completenessManifest) int {
	failures := 0
	remaining := map[string]bool{"generation_tasks": true, "task_outputs": true}
	for _, name := range legacyTables {
		remaining[name] = true
	}
	for _, domain := range manifest.Domains {
		if !remaining[domain.Domain] {
			failures++
		}
		delete(remaining, domain.Domain)
		if domain.Status != "pass" || domain.ReadGap != 0 ||
			domain.UnexplainedGap != 0 || !domain.completeDisposition() {
			failures++
		}
	}
	failures += len(remaining)
	if !manifest.Credits.Equal || manifest.CAS.SHA256Mismatch != 0 ||
		manifest.SourceLoss.Unacknowledged != 0 {
		failures++
	}
	return failures
}

func buildCompletenessManifest(
	databaseSHA string,
	tasks taskDomainReport,
	chatReport chatMigrationReport,
	canvasReport canvasMigrationReport,
	balanceReport balanceMigrationReport,
	auditReport auditMigrationReport,
	input inputAssetMigrationReport,
	output taskOutputMigrationReport,
	reconciliation sqlite.ETLReconciliation,
	cas casVerificationResult,
	baseline legacyBaseline,
	auxiliaryTargets map[string]int,
	usersLoaded int,
	failureCount int,
) completenessManifest {
	sourceCounts := baseline.counts
	domains := []manifestDomain{
		{Domain: "users", SourceCount: sourceCounts["users"], LoadedCount: usersLoaded, ImportedCount: usersLoaded, TargetCount: reconciliation.Users},
		{Domain: "generation_tasks", SourceCount: baseline.tasks, LoadedCount: tasks.migrated, ImportedCount: tasks.migrated, TargetCount: reconciliation.Tasks},
		{Domain: "usage_ledger", SourceCount: sourceCounts["usage_ledger"], LoadedCount: tasks.ledgerRows, ImportedCount: tasks.ledgerRows, TargetCount: reconciliation.UsageLedger},
		{Domain: "task_cache", SourceCount: sourceCounts["task_cache"], LoadedCount: tasks.cacheLoaded, ImportedCount: tasks.cacheMerged + tasks.cacheArchived, TargetCount: auxiliaryTargets["task_cache"], ArchivedCount: tasks.cacheArchived},
		{Domain: "task_input_snapshot", SourceCount: sourceCounts["task_input_snapshot"], LoadedCount: tasks.snapshotLoaded, ImportedCount: tasks.snapshotMerged + len(tasks.orphanSnapshotIDs), TargetCount: auxiliaryTargets["task_input_snapshot"], ArchivedCount: len(tasks.orphanSnapshotIDs)},
		{Domain: "asset_registry", SourceCount: sourceCounts["asset_registry"], LoadedCount: input.loaded, ImportedCount: input.materialized, TargetCount: reconciliation.InputAssets, SkippedCount: len(input.unavailable)},
		{Domain: "task_outputs", SourceCount: output.declared, LoadedCount: output.declared, ImportedCount: output.materialized, TargetCount: reconciliation.OutputAssets, SkippedCount: len(output.unavailable) + len(output.optionalUnavailable)},
		{Domain: "chat_sessions", SourceCount: sourceCounts["chat_sessions"], LoadedCount: chatReport.sessionLoaded, ImportedCount: chatReport.sessionLoaded - len(chatReport.sessionSkipped), TargetCount: reconciliation.ChatSessions, SkippedCount: len(chatReport.sessionSkipped)},
		{Domain: "chat_messages", SourceCount: sourceCounts["chat_messages"], LoadedCount: chatReport.messageLoaded, ImportedCount: chatReport.messageLoaded - len(chatReport.messageSkipped), TargetCount: reconciliation.ChatMessages, SkippedCount: len(chatReport.messageSkipped)},
		{Domain: "canvas_projects", SourceCount: sourceCounts["canvas_projects"], LoadedCount: canvasReport.projectLoaded, ImportedCount: canvasReport.projectLoaded - len(canvasReport.projectSkipped), TargetCount: reconciliation.CanvasProjects, SkippedCount: len(canvasReport.projectSkipped)},
		{Domain: "canvas_nodes", SourceCount: sourceCounts["canvas_nodes"], LoadedCount: canvasReport.nodeLoaded, ImportedCount: canvasReport.nodeLoaded - len(canvasReport.nodeSkipped), TargetCount: reconciliation.CanvasNodes, SkippedCount: len(canvasReport.nodeSkipped)},
		{Domain: "balance_snapshots", SourceCount: sourceCounts["balance_snapshots"], LoadedCount: balanceReport.loaded, ImportedCount: balanceReport.loaded - len(balanceReport.skipped), TargetCount: reconciliation.BalanceSnapshots, SkippedCount: len(balanceReport.skipped)},
		{Domain: "audit_logs", SourceCount: sourceCounts["audit_logs"], LoadedCount: auditReport.logLoaded, ImportedCount: auditReport.logLoaded - len(auditReport.logSkipped), TargetCount: reconciliation.AuditEvents, SkippedCount: len(auditReport.logSkipped)},
	}
	for index := range domains {
		domains[index].ReadGap = domains[index].SourceCount - domains[index].LoadedCount
		// A processed skip cannot account for a row the loader never read.
		domains[index].UnexplainedGap = domains[index].ReadGap
		domains[index].Status = "pass"
		if domains[index].UnexplainedGap != 0 || !domains[index].completeDisposition() {
			domains[index].Status = "fail"
		}
	}
	return completenessManifest{
		LegacyDatabaseSHA256: databaseSHA,
		Domains:              domains,
		Credits:              manifestCredits{SourceSum: baseline.credits, LoadedSum: tasks.credits, TargetSum: reconciliation.Credits, Equal: baseline.credits == tasks.credits && baseline.credits == reconciliation.Credits},
		CAS:                  manifestCAS{References: cas.references, Readable: cas.readable, SHA256Sampled: len(cas.samples), SHA256Mismatch: len(cas.samples) - cas.matches},
		SourceLoss:           manifestSourceLoss{CandidateCount: len(output.sourceLosses) + len(input.sourceLosses), Acknowledged: output.acknowledgedIssues + input.acknowledgedAssets, Unacknowledged: output.unacknowledgedIssueCount() + input.unacknowledgedSourceLossCount()},
		Overall:              manifestOverall{FailureCount: failureCount, Conclusion: map[bool]string{true: "pass", false: "fail"}[failureCount == 0]},
	}
}

func countLegacyTables(db legacyQuery) (map[string]int, error) {
	counts := make(map[string]int, len(legacyTables))
	for _, table := range legacyTables {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM "` + table + `"`,
		).Scan(&count); err != nil {
			return nil, fmt.Errorf("统计旧 %s 表行数: %w", table, err)
		}
		counts[table] = count
	}
	return counts, nil
}

func compareETLReconciliation(
	actual sqlite.ETLReconciliation,
	expected sqlite.ETLReconciliation,
) []string {
	type dimension struct {
		name     string
		actual   int
		expected int
	}
	dimensions := []dimension{
		{"用户", actual.Users, expected.Users},
		{"任务", actual.Tasks, expected.Tasks},
		{"Attempt", actual.Attempts, expected.Attempts},
		{"账本", actual.UsageLedger, expected.UsageLedger},
		{"任务产物", actual.OutputAssets, expected.OutputAssets},
		{"输入资产", actual.InputAssets, expected.InputAssets},
		{"聊天会话", actual.ChatSessions, expected.ChatSessions},
		{"聊天消息", actual.ChatMessages, expected.ChatMessages},
		{"Canvas 项目", actual.CanvasProjects, expected.CanvasProjects},
		{"Canvas 节点", actual.CanvasNodes, expected.CanvasNodes},
		{"余额快照", actual.BalanceSnapshots, expected.BalanceSnapshots},
		{"审计日志", actual.AuditEvents, expected.AuditEvents},
		{"隔离归档", actual.LegacyRecords, expected.LegacyRecords},
		{"账本金额", actual.Credits, expected.Credits},
	}
	issues := []string{}
	for _, item := range dimensions {
		if item.actual != item.expected {
			issues = append(
				issues,
				fmt.Sprintf(
					"%s目标=%d，期望=%d",
					item.name,
					item.actual,
					item.expected,
				),
			)
		}
	}
	return issues
}

const casSHA256SampleLimit = 20

type casSHA256Sample struct {
	storageKey string
	expected   string
	actual     string
	result     string
}

type casVerificationResult struct {
	references int
	readable   int
	matches    int
	samples    []casSHA256Sample
	issues     []string
}

func verifyCASReferences(
	ctx context.Context,
	blobs *local.Store,
	inputAssets []generation.InputAsset,
	outputAssets []generation.Asset,
) casVerificationResult {
	expectedByKey := map[string]string{}
	var result casVerificationResult
	register := func(storageKey string, expected string) {
		expected = strings.ToLower(strings.TrimSpace(expected))
		if current, found := expectedByKey[storageKey]; found &&
			current != expected {
			result.issues = append(
				result.issues,
				fmt.Sprintf(
					"CAS %s 元数据 SHA-256 冲突: %s / %s",
					storageKey,
					current,
					expected,
				),
			)
			return
		}
		expectedByKey[storageKey] = expected
	}
	for _, asset := range inputAssets {
		register(asset.StorageKey, asset.SHA256)
	}
	for _, asset := range outputAssets {
		register(asset.StorageKey, asset.SHA256)
	}
	keys := make([]string, 0, len(expectedByKey))
	for key := range expectedByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result.references = len(keys)
	for index, key := range keys {
		body, err := blobs.OpenBlob(ctx, key)
		if err != nil {
			result.issues = append(
				result.issues,
				fmt.Sprintf("CAS %s 不可读: %v", key, err),
			)
			continue
		}
		if index < casSHA256SampleLimit {
			hasher := sha256.New()
			if _, err := io.Copy(hasher, body); err != nil {
				result.issues = append(
					result.issues,
					fmt.Sprintf("CAS %s 抽验读取失败: %v", key, err),
				)
				_ = body.Close()
				continue
			}
			actual := fmt.Sprintf("%x", hasher.Sum(nil))
			sample := casSHA256Sample{
				storageKey: key,
				expected:   expectedByKey[key],
				actual:     actual,
				result:     "通过",
			}
			if actual != expectedByKey[key] {
				sample.result = "失败"
				result.issues = append(
					result.issues,
					fmt.Sprintf(
						"CAS %s SHA-256 不一致: 期望 %s，实际 %s",
						key,
						expectedByKey[key],
						actual,
					),
				)
			} else {
				result.matches++
			}
			result.samples = append(result.samples, sample)
		}
		if err := body.Close(); err != nil {
			result.issues = append(
				result.issues,
				fmt.Sprintf("CAS %s 关闭失败: %v", key, err),
			)
			continue
		}
		result.readable++
	}
	return result
}

func samePath(left string, right string) bool {
	leftAbsolute, leftErr := filepath.Abs(left)
	rightAbsolute, rightErr := filepath.Abs(right)
	return leftErr == nil &&
		rightErr == nil &&
		filepath.Clean(leftAbsolute) == filepath.Clean(rightAbsolute)
}

func writeMigrationReport(path string, report string) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("创建报告目录: %w", err)
	}
	file, err := os.CreateTemp(directory, ".migration-report-*")
	if err != nil {
		return fmt.Errorf("创建报告临时文件: %w", err)
	}
	tempPath := file.Name()
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := file.Chmod(0o640); err != nil {
		return fmt.Errorf("设置报告权限: %w", err)
	}
	if _, err := io.WriteString(file, report); err != nil {
		return fmt.Errorf("写入报告: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步报告: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭报告: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("提交报告: %w", err)
	}
	committed = true
	return nil
}

// loadLegacyUsers 读取旧 users 表并转为 Go 原生用户行。映射规则：
//   - username 为 NULL/空（SSO 用户）时按 Go 侧先例回退 openId，再回退 id，
//     保证目标表 username NOT NULL UNIQUE 成立
//   - roles 逗号分隔串转数组（Go 侧存 roles_json）
//   - 时间三种旧形态统一转 UTC（见 parseLegacyTime）
func loadLegacyUsers(db legacyQuery) ([]sqlite.AuthenticationUserImport, error) {
	rows, err := db.Query(
		`SELECT id, openId, username, passwordHash, nickname, email,
			name, roles, isAdmin, isActive, createdAt, updatedAt,
			deletedAt, lastLogin
		 FROM users ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("读取旧 users 表: %w", err)
	}
	defer func() { _ = rows.Close() }()
	users := []sqlite.AuthenticationUserImport{}
	for rows.Next() {
		var (
			id, passwordHash, nickname, email, name, roles string
			openID, username                               sql.NullString
			admin, active                                  int64
			createdAt, updatedAt, deletedAt, lastLogin     any
		)
		if err := rows.Scan(
			&id, &openID, &username, &passwordHash, &nickname, &email,
			&name, &roles, &admin, &active, &createdAt, &updatedAt,
			&deletedAt, &lastLogin,
		); err != nil {
			return nil, fmt.Errorf("读取旧用户行: %w", err)
		}
		user := sqlite.AuthenticationUserImport{
			Record: authentication.UserRecord{
				ID:           id,
				OpenID:       openID.String,
				Username:     legacyUsername(username, openID, id),
				PasswordHash: passwordHash,
				Nickname:     nickname,
				Name:         name,
				Email:        email,
				Roles:        splitLegacyRoles(roles),
				Admin:        admin != 0,
				Active:       active != 0,
			},
		}
		if user.CreatedAt, err = requireLegacyTime(
			id, "createdAt", createdAt,
		); err != nil {
			return nil, err
		}
		if user.UpdatedAt, err = requireLegacyTime(
			id, "updatedAt", updatedAt,
		); err != nil {
			return nil, err
		}
		if user.DeletedAt, err = optionalLegacyTime(
			id, "deletedAt", deletedAt,
		); err != nil {
			return nil, err
		}
		if user.LastLogin, err = optionalLegacyTime(
			id, "lastLogin", lastLogin,
		); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历旧 users 表: %w", err)
	}
	return users, nil
}

func legacyUsername(username, openID sql.NullString, id string) string {
	for _, candidate := range []string{
		strings.TrimSpace(username.String),
		strings.TrimSpace(openID.String),
	} {
		if candidate != "" {
			return candidate
		}
	}
	return id
}

func splitLegacyRoles(roles string) []string {
	result := []string{}
	for _, role := range strings.Split(roles, ",") {
		if role = strings.TrimSpace(role); role != "" {
			result = append(result, role)
		}
	}
	return result
}

func requireLegacyTime(
	userID string,
	column string,
	value any,
) (time.Time, error) {
	parsed, err := parseLegacyTime(value)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"用户 %s 的 %s 无法解析: %w", userID, column, err,
		)
	}
	return parsed, nil
}

func optionalLegacyTime(
	userID string,
	column string,
	value any,
) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	parsed, err := requireLegacyTime(userID, column, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

// legacyTimeLayouts 覆盖旧库字符串时间的实际形态：
// Prisma better-sqlite3 adapter 默认写 ISO 8601（Z 被替换为 +00:00），
// SQLite CURRENT_TIMESTAMP 默认值写 "YYYY-MM-DD HH:MM:SS"（UTC）。
var legacyTimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
}

// parseLegacyTime 把旧库时间统一转为 UTC。数值形态是 adapter 的
// unixepoch-ms 选项与 Prisma 读取路径认定的 Unix 毫秒。
func parseLegacyTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		// modernc 驱动对 DATETIME 声明列自动解析字符串形态。
		return typed.UTC(), nil
	case int64:
		return time.UnixMilli(typed).UTC(), nil
	case float64:
		return time.UnixMilli(int64(typed)).UTC(), nil
	case string:
		for _, layout := range legacyTimeLayouts {
			if parsed, err := time.ParseInLocation(
				layout, typed, time.UTC,
			); err == nil {
				return parsed.UTC(), nil
			}
		}
		return time.Time{}, fmt.Errorf("时间格式无法识别 %q", typed)
	default:
		return time.Time{}, fmt.Errorf("时间类型无法识别 %T", value)
	}
}
