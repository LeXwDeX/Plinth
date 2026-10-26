package httpapi_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/etl"
	"github.com/LeXwDeX/Plinth/internal/etl/etltest"
)

func TestETLMigrated3DHistoryUsesModelThumbnailInsteadOfTexture(
	t *testing.T,
) {
	t.Parallel()
	fixture := etltest.NewFixture(t)
	const (
		taskID                  = "task-with-model-preview"
		taskWithoutThumbnailID  = "task-without-model-preview"
		modelURL                = "https://assets.meshy.ai/demo/tasks/task-with-model-preview/output/model.glb?signature=expired"
		thumbnailURL            = "https://assets.meshy.ai/demo/tasks/task-with-model-preview/output/preview.png?signature=expired"
		textureURL              = "https://assets.meshy.ai/demo/tasks/task-with-model-preview/output/base_color.png?signature=expired"
		modelWithoutThumbnail   = "https://assets.meshy.ai/demo/tasks/task-without-model-preview/output/model.glb?signature=expired"
		textureWithoutThumbnail = "https://assets.meshy.ai/demo/tasks/task-without-model-preview/output/base_color.png?signature=expired"
	)
	writeLegacyTaskOutputCache(
		t,
		fixture.Root,
		"/demo/tasks/task-with-model-preview/output/model.glb",
		".glb",
		"model/gltf-binary",
		httpFixtureGLB("model-with-preview"),
	)
	writeLegacyTaskOutputCache(
		t,
		fixture.Root,
		"/demo/tasks/task-with-model-preview/output/preview.png",
		".png",
		"image/png",
		validETLFixturePNG(t, color.RGBA{R: 255, A: 255}),
	)
	writeLegacyTaskOutputCache(
		t,
		fixture.Root,
		"/demo/tasks/task-with-model-preview/output/base_color.png",
		".png",
		"image/png",
		validETLFixturePNG(t, color.RGBA{G: 255, A: 255}),
	)
	writeLegacyTaskOutputCache(
		t,
		fixture.Root,
		"/demo/tasks/task-without-model-preview/output/model.glb",
		".glb",
		"model/gltf-binary",
		httpFixtureGLB("model-without-preview"),
	)
	writeLegacyTaskOutputCache(
		t,
		fixture.Root,
		"/demo/tasks/task-without-model-preview/output/base_color.png",
		".png",
		"image/png",
		validETLFixturePNG(t, color.RGBA{B: 255, A: 255}),
	)
	fixture.Exec(t,
		`INSERT INTO users (
			id, username, passwordHash, nickname, email, isAdmin,
			isActive, createdAt, updatedAt
		 ) VALUES (
			'legacy-preview-user', 'legacy-preview-user', ?,
			'Preview User', 'preview@example.test', 0, 1,
			'2025-01-01T00:00:00+00:00',
			'2025-01-01T00:00:00+00:00'
		 )`,
		etltest.PasswordHash,
	)
	fixture.Exec(t,
		`INSERT INTO usage_ledger (
			id, userId, userName, userEmail, taskType, taskId,
			providerId, estimatedCredits, taskStatus, params,
			resultUrls, createdAt, finishedAt
		 ) VALUES (
			'ledger-preview', 'legacy-preview-user', 'Preview User',
			'preview@example.test', 'text-to-3d', ?, 'meshy-6', 5,
			'SUCCEEDED', '{"prompt":"preview fixture"}', '[]',
			'2025-06-01T00:00:00+00:00',
			'2025-06-01T00:05:00+00:00'
		 )`,
		taskID,
	)
	fixture.Exec(t,
		`INSERT INTO usage_ledger (
			id, userId, userName, userEmail, taskType, taskId,
			providerId, estimatedCredits, taskStatus, params,
			resultUrls, createdAt, finishedAt
		 ) VALUES (
			'ledger-without-preview', 'legacy-preview-user', 'Preview User',
			'preview@example.test', 'text-to-3d', ?, 'meshy-6', 5,
			'SUCCEEDED', '{"prompt":"missing preview fixture"}', '[]',
			'2025-06-02T00:00:00+00:00',
			'2025-06-02T00:05:00+00:00'
		 )`,
		taskWithoutThumbnailID,
	)
	taskData, err := json.Marshal(map[string]any{
		"model_urls":    map[string]string{"glb": modelURL},
		"thumbnail_url": thumbnailURL,
		"texture_urls": []map[string]string{
			{"base_color": textureURL},
		},
		"progress": 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.Exec(t,
		`INSERT INTO task_cache (
			taskId, taskType, status, providerId, data, cachedAt, updatedAt
		 ) VALUES (
			?, 'text-to-3d', 'SUCCEEDED', 'meshy-6', ?,
			'2025-06-01T00:00:00+00:00',
			'2025-06-01T00:05:00+00:00'
		 )`,
		taskID,
		string(taskData),
	)
	taskWithoutThumbnailData, err := json.Marshal(map[string]any{
		"model_urls": map[string]string{
			"glb": modelWithoutThumbnail,
		},
		"texture_urls": []map[string]string{
			{"base_color": textureWithoutThumbnail},
		},
		"progress": 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.Exec(t,
		`INSERT INTO task_cache (
			taskId, taskType, status, providerId, data, cachedAt, updatedAt
		 ) VALUES (
			?, 'text-to-3d', 'SUCCEEDED', 'meshy-6', ?,
			'2025-06-02T00:00:00+00:00',
			'2025-06-02T00:05:00+00:00'
		 )`,
		taskWithoutThumbnailID,
		string(taskWithoutThumbnailData),
	)

	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		nil,
		nil,
		nil,
		nil,
		func(t *testing.T, databasePath string) {
			t.Helper()
			var stdout, stderr bytes.Buffer
			if code := etl.Main([]string{
				"migrate",
				"--legacy-root", fixture.Root,
				"--target-db", databasePath,
				"--target-storage", filepath.Join(
					filepath.Dir(databasePath),
					"storage",
				),
			}, &stdout, &stderr); code != 0 {
				t.Fatalf(
					"ETL 迁移失败，退出码 %d:\n%s%s",
					code,
					stdout.String(),
					stderr.String(),
				)
			}
		},
	)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodGet,
		"/api/tasks?page=1&pageSize=10",
		nil,
	))
	if response.Code != http.StatusOK {
		t.Fatalf(
			"任务历史 status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	var history struct {
		Data []struct {
			ID           string `json:"id"`
			ThumbnailURL string `json:"thumbnailUrl"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	expected := "/api/v1/tasks/" + taskID + "/assets/" +
		taskID + "-asset-2"
	thumbnailByTask := make(map[string]string, len(history.Data))
	for _, task := range history.Data {
		thumbnailByTask[task.ID] = task.ThumbnailURL
	}
	if len(history.Data) != 2 ||
		thumbnailByTask[taskID] != expected ||
		thumbnailByTask[taskWithoutThumbnailID] != "" {
		t.Fatalf(
			"模型预览投影错误: got=%#v want_thumbnail=%q "+
				"missing_thumbnail_task_must_not_use_texture",
			history.Data,
			expected,
		)
	}
}

func validETLFixturePNG(t *testing.T, pixel color.RGBA) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 1, 1))
	canvas.Set(0, 0, pixel)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

// TestETLMigratedTasksMatchHTTPContracts 把 ETL 迁移后的库直接交给
// 现有 HTTP 栈（切换票 03 验收）：任务历史列表可见、单任务详情可查
// （旧 task_cache.data 保留）、用量后台合计与切换前一致。
func TestETLMigratedTasksMatchHTTPContracts(t *testing.T) {
	t.Parallel()
	fixture := etltest.NewFixture(t)
	outputBytes := validETLFixturePNG(t, color.RGBA{R: 255, A: 255})
	if err := os.WriteFile(
		filepath.Join(
			fixture.Root, "storage", "generated", "task-ok.png",
		),
		outputBytes,
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	inputBytes := validETLFixturePNG(t, color.RGBA{B: 255, A: 255})
	inputStoragePath := filepath.Join("image", "legacy-input.png")
	inputPath := filepath.Join(
		fixture.Root, "storage", "assets", inputStoragePath,
	)
	if err := os.MkdirAll(filepath.Dir(inputPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, inputBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	fixture.Exec(t,
		`INSERT INTO users (
			id, username, passwordHash, nickname, email, isAdmin,
			isActive, createdAt, updatedAt
		 ) VALUES ('legacy-user', 'legacy-artist', ?, '老画师',
			'artist@example.test', 0, 1,
			'2025-01-01T00:00:00+00:00', '2025-01-01T00:00:00+00:00')`,
		etltest.PasswordHash,
	)
	fixture.Exec(t,
		`INSERT INTO asset_registry (
			id, assetType, originalFilename, storedFilename, mimeType,
			fileSize, sourceSha256, storedSha256, storagePath,
			uploaderUserId, uploaderEmail, createdAt
		 ) VALUES (
			'legacy-input', 'image', 'legacy-input.png', 'legacy-input.png',
			'image/png', ?, '', '', ?, 'legacy-user',
			'artist@example.test', '2025-05-31T00:00:00+00:00'
		 )`,
		len(inputBytes), filepath.ToSlash(inputStoragePath),
	)
	fixture.Exec(t,
		`INSERT INTO usage_ledger (
			id, userId, userName, userEmail, taskType, taskId,
			providerId, estimatedCredits, taskStatus, params,
			resultUrls, createdAt, finishedAt
		 ) VALUES
		 ('ledger-ok', 'legacy-user', '老画师', 'artist@example.test',
		  'text-to-image', 'task-ok', 'meshy', 3, 'SUCCEEDED',
		  '{"prompt":"finished art"}', '["/api/gpt-images/task-ok.png"]',
		  '2025-06-01T00:00:00+00:00', '2025-06-01T00:05:00+00:00'),
		 ('ledger-run', 'legacy-user', '老画师', 'artist@example.test',
		  'text-to-3d', 'task-run', 'meshy', 5, 'IN_PROGRESS',
		  '{"prompt":"still running"}', '[]',
		  '2025-06-02T00:00:00+00:00', NULL)`,
	)
	fixture.Exec(t,
		`INSERT INTO task_cache (
			taskId, taskType, status, providerId, data, cachedAt, updatedAt
		 ) VALUES
		 ('task-ok', 'text-to-image', 'SUCCEEDED', 'meshy',
		  '{"image_urls":["/api/gpt-images/task-ok.png"],"progress":100}',
		  '2025-06-01T00:00:00+00:00', '2025-06-01T00:05:00+00:00'),
		 ('task-run', 'text-to-3d', 'IN_PROGRESS', 'meshy',
		  '{"progress":40}',
		  '2025-06-02T00:00:00+00:00', '2025-06-02T00:01:00+00:00')`,
	)
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		nil,
		nil,
		nil,
		nil,
		func(t *testing.T, databasePath string) {
			t.Helper()
			var stdout, stderr bytes.Buffer
			if code := etl.Main([]string{
				"migrate",
				"--legacy-root", fixture.Root,
				"--target-db", databasePath,
				"--target-storage", filepath.Join(
					filepath.Dir(databasePath),
					"storage",
				),
			}, &stdout, &stderr); code != 0 {
				t.Fatalf(
					"ETL 迁移失败，退出码 %d:\n%s%s",
					code, stdout.String(), stderr.String(),
				)
			}
		},
	)

	// 任务历史列表：切换前全部任务可见，状态已映射为原生终态。
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, httptest.NewRequest(
		http.MethodGet, "/api/tasks?page=1&pageSize=10", nil,
	))
	if listResponse.Code != http.StatusOK {
		t.Fatalf(
			"任务历史 status=%d body=%s",
			listResponse.Code, listResponse.Body.String(),
		)
	}
	var history struct {
		Data []struct {
			ID       string `json:"id"`
			TaskType string `json:"taskType"`
			Status   string `json:"status"`
			Prompt   string `json:"prompt"`
		} `json:"data"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(
		listResponse.Body.Bytes(), &history,
	); err != nil {
		t.Fatal(err)
	}
	if history.Total != 2 || len(history.Data) != 2 {
		t.Fatalf("任务历史 = %#v", history)
	}
	statuses := map[string]string{}
	prompts := map[string]string{}
	for _, task := range history.Data {
		statuses[task.ID] = task.Status
		prompts[task.ID] = task.Prompt
	}
	if statuses["task-ok"] != "SUCCEEDED" ||
		statuses["task-run"] != "FAILED" ||
		prompts["task-ok"] != "finished art" {
		t.Fatalf("任务历史投影 = %#v", history.Data)
	}

	// 单任务详情保留账本事实；旧 task_cache.data 只留服务端使用。
	detailResponse := httptest.NewRecorder()
	handler.ServeHTTP(detailResponse, httptest.NewRequest(
		http.MethodGet, "/api/admin/generations/task-ok", nil,
	))
	if detailResponse.Code != http.StatusOK {
		t.Fatalf(
			"任务详情 status=%d body=%s",
			detailResponse.Code, detailResponse.Body.String(),
		)
	}
	var detail struct {
		Ledger struct {
			TaskID           string `json:"taskId"`
			UserID           string `json:"userId"`
			EstimatedCredits int    `json:"estimatedCredits"`
			TaskStatus       string `json:"taskStatus"`
		} `json:"ledger"`
		TaskCache struct {
			Data map[string]any `json:"data"`
		} `json:"taskCache"`
	}
	if err := json.Unmarshal(
		detailResponse.Body.Bytes(), &detail,
	); err != nil {
		t.Fatal(err)
	}
	if detail.Ledger.TaskID != "task-ok" ||
		detail.Ledger.UserID != "legacy-user" ||
		detail.Ledger.EstimatedCredits != 3 ||
		detail.Ledger.TaskStatus != "SUCCEEDED" {
		t.Fatalf("任务详情账本 = %#v", detail.Ledger)
	}
	if len(detail.TaskCache.Data) != 0 {
		t.Fatalf("旧 task_cache.data 不应公开 = %#v", detail.TaskCache.Data)
	}

	// 迁移后的输出 Asset 走原生下载链路，字节与旧 generated 文件一致。
	downloadResponse := httptest.NewRecorder()
	handler.ServeHTTP(downloadResponse, httptest.NewRequest(
		http.MethodGet,
		"/api/admin/generations/task-ok/download",
		nil,
	))
	if downloadResponse.Code != http.StatusOK ||
		!bytes.Equal(downloadResponse.Body.Bytes(), outputBytes) {
		t.Fatalf(
			"历史产物下载 status=%d body=%q",
			downloadResponse.Code,
			downloadResponse.Body.Bytes(),
		)
	}

	// 旧上传资产已进入原生 InputAsset/CAS，统一资产路由可直接读取。
	inputResponse := httptest.NewRecorder()
	handler.ServeHTTP(inputResponse, httptest.NewRequest(
		http.MethodGet,
		"/api/assets/legacy-input",
		nil,
	))
	if inputResponse.Code != http.StatusOK ||
		!bytes.Equal(inputResponse.Body.Bytes(), inputBytes) {
		t.Fatalf(
			"历史输入资产 status=%d body=%q",
			inputResponse.Code,
			inputResponse.Body.Bytes(),
		)
	}

	// 用量后台：历史成本总额与切换前一致（3 + 5 = 8）。
	usageResponse := httptest.NewRecorder()
	handler.ServeHTTP(usageResponse, httptest.NewRequest(
		http.MethodGet, "/api/admin/usage?days=1000", nil,
	))
	if usageResponse.Code != http.StatusOK {
		t.Fatalf(
			"用量后台 status=%d body=%s",
			usageResponse.Code, usageResponse.Body.String(),
		)
	}
	var usage struct {
		Data []struct {
			UserID           string `json:"userId"`
			UserName         string `json:"userName"`
			TotalTasks       int    `json:"totalTasks"`
			SucceededTasks   int    `json:"succeededTasks"`
			FailedTasks      int    `json:"failedTasks"`
			EstimatedCredits int    `json:"estimatedCredits"`
		} `json:"data"`
	}
	if err := json.Unmarshal(
		usageResponse.Body.Bytes(), &usage,
	); err != nil {
		t.Fatal(err)
	}
	if len(usage.Data) != 1 {
		t.Fatalf("用量后台 = %s", usageResponse.Body.String())
	}
	summary := usage.Data[0]
	if summary.UserID != "legacy-user" ||
		!strings.Contains(summary.UserName, "老画师") ||
		summary.TotalTasks != 2 ||
		summary.SucceededTasks != 1 ||
		summary.FailedTasks != 1 ||
		summary.EstimatedCredits != 8 {
		t.Fatalf("用量合计 = %#v", summary)
	}
}

func writeLegacyTaskOutputCache(
	t *testing.T,
	legacyRoot string,
	sourcePath string,
	extension string,
	mediaType string,
	content []byte,
) {
	t.Helper()
	digest := sha256.Sum256([]byte(sourcePath))
	hash := hex.EncodeToString(digest[:])
	cacheDirectory := filepath.Join(
		legacyRoot,
		"storage",
		"cache",
		hash[:2],
	)
	if err := os.MkdirAll(cacheDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(cacheDirectory, hash+extension),
		content,
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(map[string]string{
		"contentType": mediaType,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(cacheDirectory, hash+".meta.json"),
		metadata,
		0o640,
	); err != nil {
		t.Fatal(err)
	}
}
