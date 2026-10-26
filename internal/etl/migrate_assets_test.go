package etl_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/etl/etltest"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestMigrateInputAssetsMaterializesNativeCAS(t *testing.T) {
	fixture := etltest.NewFixture(t)
	source := validPNGFixture(t)
	digest := sha256.Sum256(source)
	expectedSHA256 := hex.EncodeToString(digest[:])
	storagePath := filepath.Join(
		"image", "ab", "cd", "ef", "legacy-image.png",
	)
	sourcePath := filepath.Join(
		fixture.Root, "storage", "assets", storagePath,
	)
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, source, 0o640); err != nil {
		t.Fatal(err)
	}
	fixture.Exec(t,
		`INSERT INTO asset_registry (
			id, assetType, originalFilename, storedFilename, mimeType,
			fileSize, sourceSha256, storedSha256, storagePath,
			uploaderUserId, uploaderEmail, createdAt
		 ) VALUES (
			'legacy-asset-1', 'image', '原图.png', 'legacy-image.png',
			'image/png', ?, ?, ?, ?, 'owner-a', 'a@example.test',
			'2025-02-03T04:05:06.789+00:00'
		 )`,
		len(source), expectedSHA256, expectedSHA256,
		filepath.ToSlash(storagePath),
	)

	targetDB := migrateTarget(t)
	targetStorage := filepath.Join(t.TempDir(), "storage-go")
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", targetDB,
		"--target-storage", targetStorage,
	)
	if code != 0 {
		t.Fatalf("迁移应成功，退出码 %d，输出:\n%s", code, output)
	}
	if !strings.Contains(
		output,
		"输入资产域: 源 1 行 / 固化 1 行 / SHA-256 副本恢复 0 行 / 不可固化 0 行",
	) {
		t.Fatalf("迁移报告缺少输入资产对账:\n%s", output)
	}

	repository, err := sqlite.Open(targetDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	store, err := local.New(targetStorage)
	if err != nil {
		t.Fatal(err)
	}
	service, err := inputassets.New(repository, store)
	if err != nil {
		t.Fatal(err)
	}
	asset, body, err := service.OpenOwned(
		t.Context(), "owner-a", "legacy-asset-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	migrated, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(migrated) != string(source) {
		t.Fatalf("迁移内容不一致: %q", migrated)
	}
	if asset.ID != "legacy-asset-1" ||
		asset.OwnerID != "owner-a" ||
		asset.OriginalFilename != "原图.png" ||
		asset.AssetType != "image" ||
		asset.MediaType != "image/png" ||
		asset.Size != int64(len(source)) ||
		asset.SHA256 != expectedSHA256 ||
		asset.CreatedAt.Format(time.RFC3339Nano) !=
			"2025-02-03T04:05:06.789Z" {
		t.Fatalf("输入资产字段保真失败: %#v", asset)
	}
	if want := "blobs/" + expectedSHA256[:2] + "/" +
		expectedSHA256 + ".png"; asset.StorageKey != want {
		t.Fatalf("storageKey = %q, want %q", asset.StorageKey, want)
	}
}

func TestMigrateInputAssetRecoversExactSHA256Copy(t *testing.T) {
	fixture := etltest.NewFixture(t)
	source := validPNGFixture(t)
	digest := sha256.Sum256(source)
	expectedSHA256 := hex.EncodeToString(digest[:])
	recoveryPath := filepath.Join(
		fixture.Root,
		"storage",
		"audit",
		"Owner_A",
		"upload",
		"2025-02-03",
		"ref_image_0.png",
	)
	if err := os.MkdirAll(filepath.Dir(recoveryPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recoveryPath, source, 0o640); err != nil {
		t.Fatal(err)
	}
	fixture.Exec(t,
		`INSERT INTO asset_registry (
			id, assetType, originalFilename, storedFilename, mimeType,
			fileSize, sourceSha256, storedSha256, storagePath,
			uploaderUserId, uploaderEmail, createdAt
		 ) VALUES (
			'recovered-input', 'image', 'input.png', 'missing.png', 'image/png',
			?, ?, ?, 'image/missing/input.png',
			'owner-a', 'a@example.test', '2025-02-03T04:05:06+00:00'
		 )`,
		len(source), expectedSHA256, expectedSHA256,
	)

	targetDB := migrateTarget(t)
	targetStorage := filepath.Join(t.TempDir(), "storage-go")
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", targetDB,
		"--target-storage", targetStorage,
	)
	if code != 0 {
		t.Fatalf("同 SHA-256 副本应恢复输入资产，输出:\n%s", output)
	}
	if !strings.Contains(
		output,
		"输入资产域: 源 1 行 / 固化 1 行 / SHA-256 副本恢复 1 行 / 不可固化 0 行",
	) {
		t.Fatalf("迁移报告缺少输入资产恢复计数:\n%s", output)
	}
	repository, err := sqlite.Open(targetDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	store, err := local.New(targetStorage)
	if err != nil {
		t.Fatal(err)
	}
	service, err := inputassets.New(repository, store)
	if err != nil {
		t.Fatal(err)
	}
	asset, body, err := service.OpenOwned(
		t.Context(), "owner-a", "recovered-input",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	migrated, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(migrated) != string(source) || asset.SHA256 != expectedSHA256 {
		t.Fatalf("恢复输入资产不一致: asset=%#v body=%q", asset, migrated)
	}
}

func TestMigrateTaskOutputsMaterializesNativeAsset(t *testing.T) {
	fixture := etltest.NewFixture(t)
	source := validPNGFixture(t)
	digest := sha256.Sum256(source)
	expectedSHA256 := hex.EncodeToString(digest[:])
	if err := os.WriteFile(
		filepath.Join(
			fixture.Root,
			"storage",
			"generated",
			"historical-output.png",
		),
		source,
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	fixture.Exec(t,
		`INSERT INTO usage_ledger (
			id, userId, userName, userEmail, taskType, taskId,
			providerId, estimatedCredits, taskStatus, params,
			resultUrls, createdAt, finishedAt
		 ) VALUES (
			'ledger-output', 'owner-a', 'Owner A', 'a@example.test',
			'text-to-image', 'historical-task', 'gpt-image', 3,
			'SUCCEEDED', '{"prompt":"legacy"}',
			'["/api/gpt-images/historical-output.png"]',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:06:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO task_cache (
			taskId, taskType, status, providerId, data, cachedAt, updatedAt
		 ) VALUES (
			'historical-task', 'text-to-image', 'SUCCEEDED', 'gpt-image',
			'{"image_urls":["/api/gpt-images/historical-output.png"],"progress":100}',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:06:06+00:00'
		 )`,
	)

	targetDB := migrateTarget(t)
	targetStorage := filepath.Join(t.TempDir(), "storage-go")
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", targetDB,
		"--target-storage", targetStorage,
	)
	if code != 0 {
		t.Fatalf("迁移应成功，退出码 %d，输出:\n%s", code, output)
	}
	if !strings.Contains(
		output,
		"任务产物域: 声明 1 个 / 固化 1 个 / 不可固化 0 个",
	) {
		t.Fatalf("迁移报告缺少任务产物对账:\n%s", output)
	}

	repository, err := sqlite.Open(targetDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	task, err := repository.Get(
		t.Context(),
		"owner-a",
		"historical-task",
	)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskSucceeded {
		t.Fatalf("state = %s", task.State)
	}
	if len(task.Assets) != 1 {
		t.Fatalf("assets = %#v", task.Assets)
	}
	asset := task.Assets[0]
	if asset.ID != "historical-task-asset-1" ||
		asset.TaskID != "historical-task" ||
		asset.AttemptID != "historical-task-attempt-1" ||
		asset.Role != "image" ||
		asset.Variant != "0" ||
		!asset.Required ||
		asset.Ordinal != 1 ||
		asset.MediaType != "image/png" ||
		asset.Size != int64(len(source)) ||
		asset.SHA256 != expectedSHA256 {
		t.Fatalf("任务产物字段保真失败: %#v", asset)
	}
	store, err := local.New(targetStorage)
	if err != nil {
		t.Fatal(err)
	}
	body, err := store.Open(t.Context(), asset)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	migrated, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(migrated) != string(source) {
		t.Fatalf("迁移产物内容不一致: %q", migrated)
	}
}

func TestMigrateTaskOutputReadsLegacyCacheWithoutNetwork(t *testing.T) {
	fixture := etltest.NewFixture(t)
	sourceURL := "https://cdn.meshy.ai/task/model.glb?signature=expired"
	missingOptionalURL :=
		"https://cdn.meshy.ai/task/model.obj?signature=expired"
	proxiedSourceURL := "/api/meshy/proxy-asset?url=" +
		url.QueryEscape(sourceURL)
	proxiedMissingOptionalURL := "/api/meshy/proxy-asset?url=" +
		url.QueryEscape(missingOptionalURL)
	cacheDigest := sha256.Sum256([]byte("/task/model.glb"))
	cacheHash := hex.EncodeToString(cacheDigest[:])
	cacheDirectory := filepath.Join(
		fixture.Root,
		"storage",
		"cache",
		cacheHash[:2],
	)
	if err := os.MkdirAll(cacheDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	source := modelinputtest.Triangle(modelinputtest.Options{})
	if err := os.WriteFile(
		filepath.Join(cacheDirectory, cacheHash+".glb"),
		source,
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(cacheDirectory, cacheHash+".meta.json"),
		[]byte(`{"contentType":"model/gltf-binary"}`),
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	fixture.Exec(t,
		`INSERT INTO usage_ledger (
			id, userId, userName, userEmail, taskType, taskId,
			providerId, estimatedCredits, taskStatus, params,
			resultUrls, createdAt, finishedAt
		 ) VALUES (
			'ledger-cache', 'owner-a', 'Owner A', 'a@example.test',
			'text-to-3d', 'cached-task', 'meshy', 5, 'SUCCEEDED',
			'{"prompt":"legacy"}', ?,
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:06:06+00:00'
		 )`,
		`["`+proxiedSourceURL+`"]`,
	)
	fixture.Exec(t,
		`INSERT INTO task_cache (
			taskId, taskType, status, providerId, data, cachedAt, updatedAt
		 ) VALUES (
			'cached-task', 'text-to-3d', 'SUCCEEDED', 'meshy', ?,
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:06:06+00:00'
		 )`,
		`{"model_urls":{"glb":"`+proxiedSourceURL+
			`","obj":"`+proxiedMissingOptionalURL+`"},"progress":100}`,
	)

	targetDB := migrateTarget(t)
	targetStorage := filepath.Join(t.TempDir(), "storage-go")
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", targetDB,
		"--target-storage", targetStorage,
	)
	if code != 0 {
		t.Fatalf("迁移应成功，退出码 %d，输出:\n%s", code, output)
	}
	repository, err := sqlite.Open(targetDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	task, err := repository.Get(t.Context(), "owner-a", "cached-task")
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskSucceeded || len(task.Assets) != 1 {
		t.Fatalf("缓存产物未固化: %#v", task)
	}
	if len(task.Offering.Outputs) < 1 ||
		task.Offering.Outputs[0].Role != "model" ||
		task.Offering.Outputs[0].Minimum == nil ||
		*task.Offering.Outputs[0].Minimum != 1 {
		t.Fatalf("历史 Offering 未保留 Schema 输出要求: %#v", task.Offering)
	}
	// W6 起目录真源携带 background/on-demand 可选槽位（如缩略图）；
	// 历史固化保持 role-only 语义：主产物声明必须仍居首位且为旧语义。
	for _, output := range task.Offering.Outputs[1:] {
		if output.Delivery != generation.AssetDeliveryBackground &&
			output.Delivery != generation.AssetDeliveryOnDemand {
			t.Fatalf("历史快照出现非可选的追加声明: %#v", output)
		}
	}
	asset := task.Assets[0]
	if asset.Role != "model" ||
		asset.Variant != "glb" ||
		asset.MediaType != "model/gltf-binary" ||
		!asset.Required {
		t.Fatalf("缓存产物元数据错误: %#v", asset)
	}
	if !strings.Contains(output, "可选产物未固化 1 个") {
		t.Fatalf("可选变体缺失未进入可解释报告:\n%s", output)
	}
	if strings.Contains(output, missingOptionalURL) ||
		strings.Contains(output, "signature=expired") {
		t.Fatalf("迁移报告泄露了历史签名 URL:\n%s", output)
	}
	if !strings.Contains(output, "历史缓存中无可用字节") {
		t.Fatalf("迁移报告缺少脱敏后的失败类别:\n%s", output)
	}
	store, err := local.New(targetStorage)
	if err != nil {
		t.Fatal(err)
	}
	body, err := store.Open(t.Context(), asset)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	migrated, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(migrated) != string(source) {
		t.Fatalf("缓存产物字节不一致: %q", migrated)
	}
}

func TestMigrateTaskOutputRecoversExactAuditCopy(t *testing.T) {
	fixture := etltest.NewFixture(t)
	sourceURL := "https://cdn.meshy.ai/task/model.glb?signature=expired"
	fixture.Exec(t,
		`INSERT INTO usage_ledger (
			id, userId, userName, userEmail, taskType, taskId,
			providerId, estimatedCredits, taskStatus, params,
			resultUrls, createdAt, finishedAt
		 ) VALUES (
			'ledger-audit', 'owner-a', 'Owner A', 'a@example.test',
			'text-to-3d', 'audit-task', 'meshy-6', 5, 'SUCCEEDED',
			'{"prompt":"legacy"}', ?,
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:06:06+00:00'
		 )`,
		`["`+sourceURL+`"]`,
	)
	fixture.Exec(t,
		`INSERT INTO task_cache (
			taskId, taskType, status, providerId, data, cachedAt, updatedAt
		 ) VALUES (
			'audit-task', 'text-to-3d', 'SUCCEEDED', 'meshy-6', ?,
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:06:06+00:00'
		 )`,
		`{"model_urls":{"glb":"`+sourceURL+`"},"progress":100}`,
	)
	auditCopy := filepath.Join(
		fixture.Root,
		"storage",
		"audit",
		"Owner_A",
		"text-to-3d",
		"2025-02-03",
		"audit-task_model.glb",
	)
	if err := os.MkdirAll(filepath.Dir(auditCopy), 0o750); err != nil {
		t.Fatal(err)
	}
	source := modelinputtest.Triangle(modelinputtest.Options{Indexed: true})
	if err := os.WriteFile(auditCopy, source, 0o640); err != nil {
		t.Fatal(err)
	}

	targetDB := migrateTarget(t)
	targetStorage := filepath.Join(t.TempDir(), "storage-go")
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", targetDB,
		"--target-storage", targetStorage,
	)
	if code != 0 {
		t.Fatalf("审计副本应恢复必需产物，输出:\n%s", output)
	}
	if !strings.Contains(output, "审计副本恢复 1 个") {
		t.Fatalf("迁移报告缺少审计副本恢复计数:\n%s", output)
	}
	repository, err := sqlite.Open(targetDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	task, err := repository.Get(t.Context(), "owner-a", "audit-task")
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskSucceeded || len(task.Assets) != 1 {
		t.Fatalf("审计副本未恢复任务: %#v", task)
	}
	store, err := local.New(targetStorage)
	if err != nil {
		t.Fatal(err)
	}
	body, err := store.Open(t.Context(), task.Assets[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	migrated, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(migrated) != string(source) {
		t.Fatalf("审计副本字节不一致: %q", migrated)
	}
}

func TestMigrateMissingTaskOutputReportsAndDowngradesMetadata(t *testing.T) {
	fixture := etltest.NewFixture(t)
	seedMissingTaskOutput(t, fixture)

	targetDB := migrateTarget(t)
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", targetDB,
	)
	if code == 0 {
		t.Fatalf("缺产物必须触发非零对账门禁，退出码 %d，输出:\n%s", code, output)
	}
	if !strings.Contains(
		output,
		"任务产物域: 声明 1 个 / 固化 0 个 / 不可固化 1 个",
	) || !strings.Contains(
		output,
		"任务产物不可固化: missing-task/image[0]",
	) {
		t.Fatalf("缺失产物报告不完整:\n%s", output)
	}

	repository, err := sqlite.Open(targetDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	task, err := repository.Get(t.Context(), "owner-a", "missing-task")
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "missing-task" ||
		task.State != generation.TaskFailed ||
		task.Failure == nil ||
		task.Failure.Class != generation.FailureIntegrity ||
		task.Failure.SafeDetail != "历史任务产物不可固化" ||
		len(task.Assets) != 0 {
		t.Fatalf("缺产物任务降级不完整: %#v", task)
	}
}

func TestSourceLossAcknowledgementIsExplicitSnapshotBoundAndAuditable(
	t *testing.T,
) {
	fixture := etltest.NewFixture(t)
	seedMissingTaskOutput(t, fixture)
	templatePath := filepath.Join(t.TempDir(), "source-loss.json")

	strictTarget := migrateTarget(t)
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", strictTarget,
		"--source-loss-template", templatePath,
	)
	if code == 0 {
		t.Fatalf("候选清单不能自行放行源损失:\n%s", output)
	}
	templateBytes, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	var acknowledgement map[string]any
	if err := json.Unmarshal(templateBytes, &acknowledgement); err != nil {
		t.Fatal(err)
	}
	if acknowledgement["format"] != "art-ai-go-source-loss-ack-v1" ||
		acknowledgement["approved"] != false ||
		len(acknowledgement["legacy_database_sha256"].(string)) != 64 {
		t.Fatalf("源损失候选清单头错误: %#v", acknowledgement)
	}
	tasks := acknowledgement["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("源损失候选任务数=%d want=1", len(tasks))
	}
	entry := tasks[0].(map[string]any)
	if entry["task_id"] != "missing-task" ||
		entry["capability"] != "text-to-image" ||
		entry["provider_id"] != "gpt-image" ||
		entry["issue_count"] != float64(1) ||
		len(entry["issue_sha256"].(string)) != 64 {
		t.Fatalf("源损失候选任务不精确: %#v", entry)
	}
	info, err := os.Stat(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("源损失候选清单权限=%o want=600", info.Mode().Perm())
	}

	acknowledgement["approved"] = true
	acknowledgement["approved_by"] = "migration-owner"
	acknowledgement["approved_at_utc"] = "2026-07-29T00:00:00Z"
	acknowledgement["approval_reason"] = "确认原始产物字节已经不可恢复"
	entry["reason"] = "旧本地文件缺失且没有审计副本"
	approvedBytes, err := json.MarshalIndent(acknowledgement, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(templatePath, approvedBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	approvedTarget := migrateTarget(t)
	code, output = runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", approvedTarget,
		"--source-loss-acknowledgement", templatePath,
	)
	if code != 0 {
		t.Fatalf("精确确认的源损失应允许迁移完成，退出码 %d:\n%s", code, output)
	}
	if !strings.Contains(
		output,
		"历史源损失确认: 任务 1 个 / 明细 1 条 / 未确认 0 条",
	) || !strings.Contains(output, "对账结论: 通过") {
		t.Fatalf("放行报告缺少可审计计数:\n%s", output)
	}
	repository, err := sqlite.Open(approvedTarget)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	task, err := repository.Get(t.Context(), "owner-a", "missing-task")
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskFailed ||
		task.Failure == nil ||
		task.Failure.SafeDetail != "历史任务产物不可固化" {
		t.Fatalf("确认源损失不得伪造成功任务: %#v", task)
	}

	entry["issue_sha256"] = strings.Repeat("0", 64)
	tamperedBytes, err := json.MarshalIndent(acknowledgement, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(templatePath, tamperedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	tamperedTarget := migrateTarget(t)
	code, output = runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", tamperedTarget,
		"--source-loss-acknowledgement", templatePath,
	)
	if code == 0 ||
		!strings.Contains(output, "源损失确认清单与实际问题不一致") {
		t.Fatalf("篡改问题指纹必须被拒绝，退出码 %d:\n%s", code, output)
	}
}

func seedMissingTaskOutput(
	t *testing.T,
	fixture *etltest.Fixture,
) {
	t.Helper()
	fixture.Exec(t,
		`INSERT INTO usage_ledger (
			id, userId, userName, userEmail, taskType, taskId,
			providerId, estimatedCredits, taskStatus, params,
			resultUrls, createdAt, finishedAt
		 ) VALUES (
			'ledger-missing', 'owner-a', 'Owner A', 'a@example.test',
			'text-to-image', 'missing-task', 'gpt-image', 3,
			'SUCCEEDED', '{"prompt":"legacy"}',
			'["/api/gpt-images/missing.png"]',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:06:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO task_cache (
			taskId, taskType, status, providerId, data, cachedAt, updatedAt
		 ) VALUES (
			'missing-task', 'text-to-image', 'SUCCEEDED', 'gpt-image',
			'{"image_urls":["/api/gpt-images/missing.png"],"progress":100}',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:06:06+00:00'
		 )`,
	)
}

func TestMigrateInputAssetHashMismatchLeavesNoNativeRowOrBlob(t *testing.T) {
	fixture := etltest.NewFixture(t)
	source := []byte("\x89PNG\r\n\x1a\ncorrupted")
	storagePath := filepath.Join("image", "bad", "corrupted.png")
	sourcePath := filepath.Join(
		fixture.Root, "storage", "assets", storagePath,
	)
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, source, 0o640); err != nil {
		t.Fatal(err)
	}
	fixture.Exec(t,
		`INSERT INTO asset_registry (
			id, assetType, originalFilename, storedFilename, mimeType,
			fileSize, sourceSha256, storedSha256, storagePath,
			uploaderUserId, uploaderEmail, createdAt
		 ) VALUES (
			'corrupted-asset', 'image', 'corrupted.png', 'corrupted.png',
			'image/png', ?, '', ?, ?, 'owner-a', 'a@example.test',
			'2025-02-03T04:05:06+00:00'
		 )`,
		len(source), strings.Repeat("0", 64), filepath.ToSlash(storagePath),
	)

	targetDB := migrateTarget(t)
	targetStorage := filepath.Join(t.TempDir(), "storage-go")
	templatePath := filepath.Join(t.TempDir(), "source-loss.json")
	code, output := runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", targetDB,
		"--target-storage", targetStorage,
		"--source-loss-template", templatePath,
	)
	if code == 0 {
		t.Fatalf("单条损坏资产必须触发非零对账门禁，输出:\n%s", output)
	}
	if !strings.Contains(
		output,
		"输入资产域: 源 1 行 / 固化 0 行 / SHA-256 副本恢复 0 行 / 不可固化 1 行",
	) || !strings.Contains(output, "SHA-256 不一致") {
		t.Fatalf("损坏资产报告不完整:\n%s", output)
	}
	db := openTargetDatabase(t, targetDB)
	var rows int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM generation_input_assets`,
	).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("损坏资产不应生成原生行，实际 %d", rows)
	}
	if blobs := countCASBlobs(t, targetStorage); blobs != 0 {
		t.Fatalf("损坏资产不应遗留 CAS Blob，实际 %d", blobs)
	}

	templateBytes, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(templateBytes, &document); err != nil {
		t.Fatal(err)
	}
	tasks := document["tasks"].([]any)
	inputAssets := document["input_assets"].([]any)
	if len(tasks) != 0 || len(inputAssets) != 1 ||
		inputAssets[0].(map[string]any)["asset_id"] != "corrupted-asset" {
		t.Fatalf("输入资产源损失候选不精确: %#v", document)
	}
	document["approved"] = true
	document["approved_by"] = "migration-owner"
	document["approved_at_utc"] = "2026-07-29T00:00:00Z"
	document["approval_reason"] = "确认冻结源中已不存在登记字节"
	inputAssets[0].(map[string]any)["reason"] =
		"登记 SHA-256 与现存字节不符且无一致副本"
	approvedBytes, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(templatePath, approvedBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	approvedTargetDB := migrateTarget(t)
	approvedTargetStorage := filepath.Join(t.TempDir(), "approved-storage-go")
	code, output = runETL(t,
		"migrate",
		"--legacy-root", fixture.Root,
		"--target-db", approvedTargetDB,
		"--target-storage", approvedTargetStorage,
		"--source-loss-acknowledgement", templatePath,
	)
	if code != 0 ||
		!strings.Contains(output, "历史输入资产源损失确认: 资产 1 个 / 未确认 0 个") ||
		!strings.Contains(output, "对账结论: 通过") {
		t.Fatalf("精确批准的输入资产源损失应通过对账，退出码 %d:\n%s", code, output)
	}
	approvedDB := openTargetDatabase(t, approvedTargetDB)
	defer approvedDB.Close()
	if err := approvedDB.QueryRow(
		`SELECT COUNT(*) FROM generation_input_assets`,
	).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 || countCASBlobs(t, approvedTargetStorage) != 0 {
		t.Fatalf("源损失批准不得伪造输入资产或 Blob: rows=%d", rows)
	}
}

func TestMigrateAssetsIsIdempotentAndDeduplicatesCAS(t *testing.T) {
	fixture := etltest.NewFixture(t)
	source := validPNGFixture(t)
	digest := sha256.Sum256(source)
	expectedSHA256 := hex.EncodeToString(digest[:])
	inputStoragePath := filepath.Join("image", "shared", "input.png")
	inputPath := filepath.Join(
		fixture.Root, "storage", "assets", inputStoragePath,
	)
	if err := os.MkdirAll(filepath.Dir(inputPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, source, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(
			fixture.Root, "storage", "generated", "shared-output.png",
		),
		source,
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(fixture.Root, "storage", "thumbnails", "shared.webp"),
		[]byte("rebuildable-thumbnail"),
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	fixture.Exec(t,
		`INSERT INTO asset_registry (
			id, assetType, originalFilename, storedFilename, mimeType,
			fileSize, sourceSha256, storedSha256, storagePath,
			uploaderUserId, uploaderEmail, createdAt
		 ) VALUES (
			'shared-input', 'image', 'input.png', 'input.png', 'image/png',
			?, ?, ?, ?, 'owner-a', 'a@example.test',
			'2025-02-03T04:05:06+00:00'
		 )`,
		len(source), expectedSHA256, expectedSHA256,
		filepath.ToSlash(inputStoragePath),
	)
	fixture.Exec(t,
		`INSERT INTO usage_ledger (
			id, userId, userName, userEmail, taskType, taskId,
			providerId, estimatedCredits, taskStatus, params,
			resultUrls, createdAt, finishedAt
		 ) VALUES (
			'ledger-shared', 'owner-a', 'Owner A', 'a@example.test',
			'text-to-image', 'shared-task', 'gpt-image', 3, 'SUCCEEDED',
			'{"prompt":"legacy"}',
			'["/api/gpt-images/shared-output.png"]',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:06:06+00:00'
		 )`,
	)
	fixture.Exec(t,
		`INSERT INTO task_cache (
			taskId, taskType, status, providerId, data, cachedAt, updatedAt
		 ) VALUES (
			'shared-task', 'text-to-image', 'SUCCEEDED', 'gpt-image',
			'{"image_urls":["/api/gpt-images/shared-output.png"],"progress":100}',
			'2025-02-03T04:05:06+00:00',
			'2025-02-03T04:06:06+00:00'
		 )`,
	)

	targetDB := migrateTarget(t)
	targetStorage := filepath.Join(t.TempDir(), "storage-go")
	run := func() string {
		t.Helper()
		code, output := runETL(t,
			"migrate",
			"--legacy-root", fixture.Root,
			"--target-db", targetDB,
			"--target-storage", targetStorage,
		)
		if code != 0 {
			t.Fatalf("迁移应成功，退出码 %d，输出:\n%s", code, output)
		}
		return output
	}
	run()
	firstInputRows, firstOutputRows := assetRowCounts(t, targetDB)
	firstBlobs := countCASBlobs(t, targetStorage)
	secondOutput := run()
	secondInputRows, secondOutputRows := assetRowCounts(t, targetDB)
	secondBlobs := countCASBlobs(t, targetStorage)

	if firstInputRows != 1 || firstOutputRows != 1 || firstBlobs != 1 {
		t.Fatalf(
			"首轮计数 input=%d output=%d blobs=%d",
			firstInputRows,
			firstOutputRows,
			firstBlobs,
		)
	}
	if secondInputRows != firstInputRows ||
		secondOutputRows != firstOutputRows ||
		secondBlobs != firstBlobs {
		t.Fatalf(
			"重跑计数漂移: first=(%d,%d,%d) second=(%d,%d,%d)",
			firstInputRows,
			firstOutputRows,
			firstBlobs,
			secondInputRows,
			secondOutputRows,
			secondBlobs,
		)
	}
	if !strings.Contains(secondOutput, expectedSHA256) {
		t.Fatalf("重跑报告缺少 SHA-256 样本:\n%s", secondOutput)
	}
	if !strings.Contains(
		secondOutput,
		"缩略图域: 源 1 个 / 按需重建 1 个",
	) {
		t.Fatalf("重跑报告未解释缩略图处理:\n%s", secondOutput)
	}
}

func assetRowCounts(t *testing.T, targetDB string) (int, int) {
	t.Helper()
	db := openTargetDatabase(t, targetDB)
	var inputs, outputs int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM generation_input_assets`,
	).Scan(&inputs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM generation_assets`,
	).Scan(&outputs); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return inputs, outputs
}

func countCASBlobs(t *testing.T, storageRoot string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(
		filepath.Join(storageRoot, "blobs"),
		func(_ string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type().IsRegular() {
				count++
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return count
}
