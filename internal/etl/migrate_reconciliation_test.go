package etl_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/etl/etltest"
)

func TestMigrateFullDomainReportAndRerunAreExact(t *testing.T) {
	fixture := etltest.NewFixture(t)
	inputDigest, outputDigest := seedFullDomainFixture(t, fixture)
	targetDB := migrateTarget(t)
	targetStorage := filepath.Join(t.TempDir(), "storage-go")
	reportPath := filepath.Join(t.TempDir(), "full-domain-report.txt")
	manifestPath := reportPath + ".manifest.json"
	run := func() (int, string) {
		return runETL(t,
			"migrate",
			"--legacy-root", fixture.Root,
			"--target-db", targetDB,
			"--target-storage", targetStorage,
			"--report", reportPath,
			"--manifest", manifestPath,
		)
	}
	code, output := run()
	if code != 0 {
		t.Fatalf("全域迁移应成功，退出码 %d:\n%s", code, output)
	}
	assertCompletenessManifest(t, manifestPath, map[string]int{
		"users":               1,
		"usage_ledger":        1,
		"generation_tasks":    1,
		"task_cache":          1,
		"task_input_snapshot": 1,
		"asset_registry":      1,
		"task_outputs":        1,
		"chat_sessions":       1,
		"chat_messages":       1,
		"canvas_projects":     1,
		"canvas_nodes":        1,
		"balance_snapshots":   1,
		"audit_logs":          1,
	})
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	report := string(reportBytes)
	for _, expected := range []string{
		"用户域: 源 1 行 / 迁移 1 行 / 跳过 0 行",
		"任务域: 账本源 1 行 / 迁移任务 1 个 / 跳过 0 个 / 金额合计 7",
		"账本金额对账: 源 7 / 目标导入 7 / 差额 0",
		"状态映射分布: SUCCEEDED=1 FAILED=0(含中断 0) CANCELED=0",
		"聊天域: 会话源 1 行 / 迁移 1 行 / 跳过 0 行；消息源 1 行 / 迁移 1 行 / 跳过 0 行",
		"Canvas 域: 项目源 1 行 / 迁移 1 行 / 跳过 0 行；节点源 1 行 / 迁移 1 行 / 跳过 0 行",
		"余额快照域: 源 1 行 / 迁移 1 行 / 跳过 0 行",
		"审计域: 日志源 1 行 / 迁移 1 行 / 跳过 0 行；文件源 1 个 / 迁移 1 个 / 重跑跳过 0 个 / 失败 0 个",
		"输入资产域: 源 1 行 / 固化 1 行 / SHA-256 副本恢复 0 行 / 不可固化 0 行",
		"任务产物域: 声明 1 个 / 固化 1 个 / 不可固化 0 个",
		"目标原生行数: 用户=1 任务=1 Attempt=1 账本=1 任务产物=1 输入资产=1 聊天会话=1 聊天消息=1 Canvas项目=1 Canvas节点=1 余额快照=1 审计日志=1",
		"CAS 对账: 唯一引用 2 个 / 可读 2 个 / 失败 0 个",
		"CAS SHA-256 抽验: 样本 2 个 / 通过 2 个 / 失败 0 个",
		"期望 " + inputDigest + " / 实际 " + inputDigest + " / 通过",
		"期望 " + outputDigest + " / 实际 " + outputDigest + " / 通过",
		"对账结论: 通过",
	} {
		if !strings.Contains(report, expected) {
			t.Errorf("全域报告缺少 %q:\n%s", expected, report)
		}
	}

	firstCounts := nativeDomainCounts(t, targetDB)
	firstCAS := countCASBlobs(t, targetStorage)
	db := openTargetDatabase(t, targetDB)
	if _, err := db.Exec(
		`UPDATE go_chat_sessions
		 SET title = 'Go 运行时后续编辑'
		 WHERE id = 'full-session'`,
	); err != nil {
		t.Fatal(err)
	}

	code, output = run()
	if code != 0 {
		t.Fatalf("完整重跑应成功，退出码 %d:\n%s", code, output)
	}
	secondCounts := nativeDomainCounts(t, targetDB)
	if fmt.Sprint(secondCounts) != fmt.Sprint(firstCounts) {
		t.Fatalf(
			"重跑改变各域行数:\n首跑 %v\n重跑 %v",
			firstCounts,
			secondCounts,
		)
	}
	if secondCAS := countCASBlobs(t, targetStorage); secondCAS != firstCAS {
		t.Fatalf("重跑改变 CAS 数量: 首跑 %d，重跑 %d", firstCAS, secondCAS)
	}
	reportBytes, err = os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	report = string(reportBytes)
	for _, expected := range []string{
		"重跑跳过量: 用户=1 任务=1 Attempt=1 账本=1 任务产物=1 输入资产=1 聊天会话=1 聊天消息=1 Canvas项目=1 Canvas节点=1 余额快照=1 审计日志=1 审计文件=1",
		"CAS 对账: 唯一引用 2 个 / 可读 2 个 / 失败 0 个",
		"对账结论: 通过",
	} {
		if !strings.Contains(report, expected) {
			t.Errorf("重跑报告缺少 %q:\n%s", expected, report)
		}
	}
	var title string
	if err := db.QueryRow(
		`SELECT title FROM go_chat_sessions WHERE id = 'full-session'`,
	).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Go 运行时后续编辑" {
		t.Fatalf("重跑覆盖了 Go 侧后续写入，标题=%q", title)
	}
}

func TestMigrateWritesReconciliationReport(t *testing.T) {
	fixture := etltest.NewFixture(t)
	fixture.Exec(t,
		`INSERT INTO users (
			id, username, passwordHash, createdAt, updatedAt
		 ) VALUES (
			'report-user', 'report-user', 'hash',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:05:06+00:00'
		 )`,
	)
	targetDB := migrateTarget(t)
	reportPath := filepath.Join(t.TempDir(), "reports", "cutover.txt")
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", targetDB,
		"--report", reportPath,
	)
	if code != 0 {
		t.Fatalf("迁移应成功，退出码 %d:\n%s", code, output)
	}
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	report := string(reportBytes)
	for _, expected := range []string{
		"用户域: 源 1 行 / 迁移 1 行 / 跳过 0 行",
		"任务域:",
		"输入资产域:",
		"聊天域:",
		"Canvas 域:",
		"余额快照域:",
		"审计域:",
		"账本金额对账:",
		"对账结论: 通过",
	} {
		if !strings.Contains(report, expected) {
			t.Errorf("报告缺少 %q:\n%s", expected, report)
		}
	}
	if !strings.Contains(output, "对账报告: "+reportPath) {
		t.Fatalf("命令输出未声明报告路径:\n%s", output)
	}
	info, err := os.Stat(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		t.Fatalf("报告不应允许组/其他用户写入，权限 = %o", info.Mode().Perm())
	}
}

func assertCompletenessManifest(
	t *testing.T,
	path string,
	expectedSources map[string]int,
) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Format               string `json:"format"`
		LegacyDatabaseSHA256 string `json:"legacy_database_sha256"`
		Domains              []struct {
			Domain         string `json:"domain"`
			SourceCount    int    `json:"source_count"`
			LoadedCount    int    `json:"loaded_count"`
			ImportedCount  int    `json:"imported_count"`
			TargetCount    int    `json:"target_count"`
			SkippedCount   int    `json:"skipped_count"`
			ReadGap        int    `json:"read_gap"`
			UnexplainedGap int    `json:"unexplained_gap"`
			Status         string `json:"status"`
		} `json:"domains"`
		Credits struct {
			SourceSum int  `json:"source_sum"`
			TargetSum int  `json:"target_sum"`
			Equal     bool `json:"equal"`
		} `json:"credits"`
		CAS struct {
			References     int `json:"references"`
			Readable       int `json:"readable"`
			SHA256Sampled  int `json:"sha256_sampled"`
			SHA256Mismatch int `json:"sha256_mismatch"`
		} `json:"cas"`
		SourceLoss struct {
			Unacknowledged int `json:"unacknowledged"`
		} `json:"source_loss"`
		Overall struct {
			FailureCount int    `json:"failure_count"`
			Conclusion   string `json:"conclusion"`
		} `json:"overall"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	if document.Format != "art-ai-etl-completeness-manifest-v1" ||
		len(document.LegacyDatabaseSHA256) != 64 ||
		document.Overall.FailureCount != 0 ||
		document.Overall.Conclusion != "pass" {
		t.Fatalf("manifest 总结无效: %+v domains=%+v", document.Overall, document.Domains)
	}
	domains := map[string]struct {
		source, loaded, imported, target, skipped, readGap, unexplained int
		status                                                          string
	}{}
	for _, domain := range document.Domains {
		domains[domain.Domain] = struct {
			source, loaded, imported, target, skipped, readGap, unexplained int
			status                                                          string
		}{domain.SourceCount, domain.LoadedCount, domain.ImportedCount,
			domain.TargetCount, domain.SkippedCount, domain.ReadGap,
			domain.UnexplainedGap, domain.Status}
	}
	for name, source := range expectedSources {
		domain, exists := domains[name]
		if !exists || domain.source != source || domain.readGap != 0 ||
			domain.unexplained != 0 || domain.status != "pass" {
			t.Errorf("manifest 域 %s 无效: %+v", name, domain)
		}
	}
	if !document.Credits.Equal || document.Credits.SourceSum != 7 ||
		document.Credits.TargetSum != 7 || document.CAS.References != 2 ||
		document.CAS.Readable != 2 || document.CAS.SHA256Sampled != 2 ||
		document.CAS.SHA256Mismatch != 0 ||
		document.SourceLoss.Unacknowledged != 0 {
		t.Fatalf("manifest 对账摘要无效")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest 权限 = %o，期望 600", info.Mode().Perm())
	}
}

func seedFullDomainFixture(
	t *testing.T,
	fixture *etltest.Fixture,
) (string, string) {
	t.Helper()
	inputBytes := validPNGFixture(t)
	outputBytes := validPNGFixtureColor(t, color.NRGBA{R: 200, G: 10, B: 30, A: 255})
	inputHash := fmt.Sprintf("%x", sha256.Sum256(inputBytes))
	outputHash := fmt.Sprintf("%x", sha256.Sum256(outputBytes))
	inputRelative := filepath.Join("image", "full", "input.png")
	inputPath := filepath.Join(
		fixture.Root,
		"storage",
		"assets",
		inputRelative,
	)
	if err := os.MkdirAll(filepath.Dir(inputPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, inputBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(
		fixture.Root,
		"storage",
		"generated",
		"full-task.png",
	)
	if err := os.WriteFile(outputPath, outputBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	auditRelative := filepath.Join("full-user", "upload", "evidence.txt")
	auditPath := filepath.Join(
		fixture.Root,
		"storage",
		"audit",
		auditRelative,
	)
	if err := os.MkdirAll(filepath.Dir(auditPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auditPath, []byte("audit-evidence"), 0o640); err != nil {
		t.Fatal(err)
	}

	fixture.Exec(t,
		`INSERT INTO users (
			id, username, passwordHash, nickname, email, roles,
			isAdmin, isActive, createdAt, updatedAt
		 ) VALUES (
			'full-user', 'full-user', ?, '完整用户', 'full@example.test',
			'["artist"]', 0, 1,
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:05:06+00:00'
		 )`,
		etltest.PasswordHash,
	)
	fixture.Exec(t,
		`INSERT INTO usage_ledger (
			id, userId, userName, userEmail, taskType, taskId,
			providerId, estimatedCredits, taskStatus, params,
			resultUrls, createdAt, finishedAt
		 ) VALUES (
			'full-ledger', 'full-user', '完整用户', 'full@example.test',
			'text-to-image', 'full-task', 'gpt-image', 7, 'SUCCEEDED',
			'{"prompt":"full fixture"}',
			'["/api/gpt-images/full-task.png"]',
			'2025-02-03T04:06:06+00:00',
			'2025-02-03T04:07:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO task_cache (
			taskId, taskType, status, providerId, data, cachedAt, updatedAt
		 ) VALUES (
			'full-task', 'text-to-image', 'SUCCEEDED', 'gpt-image',
			'{"image_urls":["/api/gpt-images/full-task.png"],"progress":100}',
			'2025-02-03T04:06:06+00:00',
			'2025-02-03T04:07:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO task_input_snapshot (
			taskId, capability, providerId, inputParams, inputFiles, createdAt
		 ) VALUES (
			'full-task', 'text-to-image', 'gpt-image',
			'{"prompt":"full fixture"}',
			'[{"assetId":"full-input"}]',
			'2025-02-03T04:06:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO asset_registry (
			id, assetType, originalFilename, storedFilename, mimeType,
			fileSize, sourceSha256, storedSha256, storagePath,
			uploaderUserId, uploaderEmail, createdAt
		 ) VALUES (
			'full-input', 'image', 'input.png', 'input.png', 'image/png',
			?, ?, ?, ?, 'full-user', 'full@example.test',
			'2025-02-03T04:05:30+00:00'
		 )`,
		len(inputBytes),
		inputHash,
		inputHash,
		filepath.ToSlash(inputRelative),
	)
	fixture.Exec(t,
		`INSERT INTO balance_snapshots (id, balance, capturedAt)
		 VALUES (
			'full-balance', 812.75, '2025-02-03T04:08:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO chat_sessions (
			id, userId, userName, title, createdAt, updatedAt
		 ) VALUES (
			'full-session', 'full-user', '完整用户', '完整会话',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:09:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO chat_messages (
			id, sessionId, role, content, imageUrls, toolCalls, createdAt
		 ) VALUES (
			'full-message', 'full-session', 'assistant', '已完成',
			'["/api/gpt-images/full-task.png"]',
			'[{"name":"generate_image","arguments":"{\"prompt\":\"full fixture\"}","taskId":"full-task"}]',
			'2025-02-03T04:07:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO canvas_projects (
			id, userId, name, sessionId, viewport, bgStyle,
			createdAt, updatedAt
		 ) VALUES (
			'full-project', 'full-user', '完整画板', 'full-session',
			'{"zoom":1,"panX":0,"panY":0}', 'dots',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:09:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO canvas_nodes (
			id, projectId, type, x, y, width, height, zIndex, data,
			visible, locked, createdAt, updatedAt
		 ) VALUES (
			'full-node', 'full-project', 'image', 10, 20, 256, 256, 1,
			'{"assetId":"full-input"}', 1, 0,
			'2025-02-03T04:08:06+00:00',
			'2025-02-03T04:09:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO audit_logs (
			id, action, userId, userName, resourceType, resourceId,
			detail, ipAddress, userAgent, createdAt
		 ) VALUES (
			'full-audit', 'upload_file', 'full-user', '完整用户',
			'asset', 'full-input', ?, '192.0.2.10', 'fixture',
			'2025-02-03T04:05:30+00:00'
		 )`,
		`{"auditPath":"`+filepath.ToSlash(auditPath)+`"}`,
	)
	return inputHash, outputHash
}

func nativeDomainCounts(t *testing.T, databasePath string) map[string]int {
	t.Helper()
	db := openTargetDatabase(t, databasePath)
	result := map[string]int{}
	for _, table := range []string{
		"authentication_users",
		"generation_tasks",
		"generation_attempts",
		"generation_usage_ledger",
		"generation_assets",
		"generation_input_assets",
		"go_chat_sessions",
		"go_chat_messages",
		"go_canvas_projects",
		"go_canvas_nodes",
		"generation_balance_snapshots",
		"administration_audit_events",
	} {
		var count int
		if err := db.QueryRow(
			"SELECT COUNT(*) FROM " + table,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		result[table] = count
	}
	return result
}

func TestMigrateFailureDetailWritesReportAndReturnsNonZero(t *testing.T) {
	fixture := etltest.NewFixture(t)
	sourcePath := filepath.Join(
		fixture.Root,
		"storage",
		"assets",
		"image",
		"corrupted.png",
	)
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("corrupted"), 0o640); err != nil {
		t.Fatal(err)
	}
	fixture.Exec(t,
		`INSERT INTO asset_registry (
			id, assetType, originalFilename, storedFilename, mimeType,
			fileSize, sourceSha256, storedSha256, storagePath,
			uploaderUserId, createdAt
		 ) VALUES (
			'bad-report-asset', 'image', 'corrupted.png', 'corrupted.png',
			'image/png', 9, '',
			'0000000000000000000000000000000000000000000000000000000000000000',
			'image/corrupted.png', 'owner-a',
			'2025-02-03T04:05:06+00:00'
		 )`,
	)
	targetDB := migrateTarget(t)
	reportPath := targetDB + ".migration-report.txt"
	manifestPath := reportPath + ".manifest.json"
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", targetDB,
	)
	if code == 0 {
		t.Fatalf("存在失败明细时必须非零退出:\n%s", output)
	}
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	report := string(reportBytes)
	if !strings.Contains(report, "bad-report-asset") ||
		!strings.Contains(report, "SHA-256 不一致") ||
		!strings.Contains(report, "对账结论: 失败") {
		t.Fatalf("失败报告不完整:\n%s", report)
	}
	if !strings.Contains(output, "迁移数据已提交，但对账未通过") {
		t.Fatalf("退出原因不清晰:\n%s", output)
	}
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"conclusion": "fail"`) {
		t.Fatalf("失败迁移 manifest 未标记 fail:\n%s", body)
	}
}

func TestMigrateProgressesWhileServiceReadsAndWritesWAL(t *testing.T) {
	fixture := etltest.NewFixture(t)
	for index := range 1_000 {
		id := fmt.Sprintf("wal-user-%04d", index)
		fixture.Exec(t,
			`INSERT INTO users (
				id, username, passwordHash, createdAt, updatedAt
			 ) VALUES (?, ?, 'hash',
				'2025-02-03T04:05:06+00:00',
				'2025-02-03T04:05:06+00:00')`,
			id,
			id,
		)
	}
	targetDB := migrateTarget(t)
	serviceRepository, err := sqlite.Open(targetDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serviceRepository.Close() }()

	started := make(chan struct{})
	stop := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		for index := 0; ; index++ {
			select {
			case <-stop:
				writerDone <- nil
				return
			default:
			}
			if err := serviceRepository.RecordAudit(
				t.Context(),
				administration.AuditEvent{
					ID:        fmt.Sprintf("live-audit-%06d", index),
					Action:    "service_write_during_etl",
					ActorID:   "live-service",
					ActorName: "Go Runtime",
					Detail:    map[string]any{"index": index},
					CreatedAt: time.Now().UTC(),
				},
			); err != nil {
				writerDone <- err
				return
			}
			if _, _, err := serviceRepository.QueryAdministrationAuditEvents(
				t.Context(),
				administration.AuditQuery{
					Page:     1,
					PageSize: 1,
					UserID:   "live-service",
				},
			); err != nil {
				writerDone <- err
				return
			}
			if index == 0 {
				close(started)
			}
			runtime.Gosched()
		}
	}()
	select {
	case <-started:
	case err := <-writerDone:
		t.Fatalf("启动在线 WAL 读写失败: %v", err)
	}
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", targetDB,
	)
	close(stop)
	if err := <-writerDone; err != nil {
		t.Fatalf("迁移期间在线连接读写失败: %v", err)
	}
	if code != 0 {
		t.Fatalf("WAL 并发读写时迁移失败，退出码 %d:\n%s", code, output)
	}
	db := openTargetDatabase(t, targetDB)
	var migratedUsers, liveWrites int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM authentication_users
		 WHERE id LIKE 'wal-user-%'`,
	).Scan(&migratedUsers); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM administration_audit_events
		 WHERE id LIKE 'live-audit-%'`,
	).Scan(&liveWrites); err != nil {
		t.Fatal(err)
	}
	if migratedUsers != 1_000 || liveWrites < 1 {
		t.Fatalf(
			"WAL 并发结果不完整: 迁移用户=%d，在线写入=%d",
			migratedUsers,
			liveWrites,
		)
	}
}
