package httpapi_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestAdministrationUserAssetsRouteQueryWireAndTextUseProductSchema(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			application.Runtime.HTTP.LegacyRoutes.AdminUserAssets =
				"/custom/admin/accounts/{account}/assets"
			schema := &application.Administration.UserAssets
			schema.API.PathParameters.UserID = "account"
			schema.API.Query.PageSize = "limit"
			schema.API.Query.UploadsPage = "upload_cursor"
			schema.API.Query.GenerationsPage = "generation_cursor"
			schema.API.Query.DownloadsPage = "download_cursor"
			schema.API.Query.From = "begin"
			schema.API.Query.To = "finish"
			schema.API.EnvelopeFields.Error = "failure"
			schema.API.EnvelopeFields.User = "owner"
			schema.API.EnvelopeFields.Summary = "metrics"
			schema.API.EnvelopeFields.Uploads = "upload_page"
			schema.API.EnvelopeFields.Generations = "generation_page"
			schema.API.EnvelopeFields.Downloads = "download_page"
			schema.API.PageFields.Data = "rows"
			schema.API.PageFields.Total = "item_count"
			schema.API.PageFields.Page = "cursor"
			schema.API.PageFields.PageSize = "limit_value"
			schema.API.UserFields.ID = "account_id"
			schema.API.UserFields.Username = "login_name"
			schema.API.UserFields.Nickname = "display_name"
			schema.API.UserFields.Email = "mail"
			schema.API.UserFields.Name = "legal_name"
			schema.API.UserFields.IsAdmin = "administrator"
			schema.API.UserFields.IsActive = "enabled"
			schema.API.UserFields.CreatedAt = "registered"
			schema.API.UserFields.LastLogin = "last_seen"
			schema.API.SummaryFields.TotalUploads = "upload_count"
			schema.API.SummaryFields.TotalGenerations = "generation_count"
			schema.API.SummaryFields.TotalDownloads = "download_count"
			schema.API.SummaryFields.TotalStorageBytes = "stored_bytes"
			schema.API.UploadFields.ID = "upload_id"
			schema.API.UploadFields.AssetType = "asset_kind"
			schema.API.UploadFields.OriginalFilename = "source_name"
			schema.API.UploadFields.StoredFilename = "stored_name"
			schema.API.UploadFields.MimeType = "content_type"
			schema.API.UploadFields.FileSize = "byte_size"
			schema.API.UploadFields.StoragePath = "storage_key"
			schema.API.UploadFields.CreatedAt = "uploaded"
			schema.API.GenerationFields.ID = "record_id"
			schema.API.GenerationFields.TaskType = "capability"
			schema.API.GenerationFields.TaskID = "task"
			schema.API.GenerationFields.TaskStatus = "state"
			schema.API.GenerationFields.ResultURLs = "outputs"
			schema.API.GenerationFields.Params = "inputs"
			schema.API.GenerationFields.EstimatedCredits = "credits"
			schema.API.GenerationFields.ThumbnailURL = "preview"
			schema.API.GenerationFields.CreatedAt = "created"
			schema.API.GenerationFields.FinishedAt = "completed"
			schema.API.DownloadFields.ID = "event_id"
			schema.API.DownloadFields.Action = "event_type"
			schema.API.DownloadFields.ResourceType = "subject_type"
			schema.API.DownloadFields.ResourceID = "subject_id"
			schema.API.DownloadFields.Detail = "metadata"
			schema.API.DownloadFields.CreatedAt = "observed"
			schema.Messages.UserNotFound = "schema owner missing"
		},
		nil,
		nil,
		nil,
		seedAdministrationUserAssetsHTTPContract,
	)

	response := administrationRequest(
		handler,
		http.MethodGet,
		"/custom/admin/accounts/asset-user/assets?limit=10"+
			"&upload_cursor=1&generation_cursor=1&download_cursor=1",
		"",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body.String(), "assets.test") ||
		strings.Contains(response.Body.String(), "provider-secret") {
		t.Fatalf("historical provider locator leaked in user-assets response: %s", response.Body.String())
	}
	owner := body["owner"].(map[string]any)
	metrics := body["metrics"].(map[string]any)
	uploadPage := body["upload_page"].(map[string]any)
	generationPage := body["generation_page"].(map[string]any)
	downloadPage := body["download_page"].(map[string]any)
	if owner["account_id"] != "asset-user" ||
		owner["display_name"] != "Asset Alice" ||
		owner["mail"] != "asset@example.test" ||
		metrics["upload_count"] != float64(1) ||
		metrics["generation_count"] != float64(1) ||
		metrics["download_count"] != float64(1) ||
		metrics["stored_bytes"] != float64(1234) {
		t.Fatalf("owner=%#v metrics=%#v", owner, metrics)
	}
	upload := uploadPage["rows"].([]any)[0].(map[string]any)
	generation := generationPage["rows"].([]any)[0].(map[string]any)
	download := downloadPage["rows"].([]any)[0].(map[string]any)
	if upload["upload_id"] != "legacy-upload" ||
		upload["source_name"] != "input.png" ||
		uploadPage["cursor"] != float64(1) ||
		uploadPage["limit_value"] != float64(10) ||
		generation["task"] != "legacy-task" ||
		generation["outputs"] != `[]` ||
		download["event_id"] != "legacy-download" ||
		download["event_type"] != "download_asset" {
		t.Fatalf(
			"upload=%#v generation=%#v download=%#v",
			upload,
			generation,
			download,
		)
	}
	if _, leaked := body["summary"]; leaked {
		t.Fatalf("hard-coded envelope field leaked: %#v", body)
	}

	missing := administrationRequest(
		handler,
		http.MethodGet,
		"/custom/admin/accounts/missing/assets",
		"",
		nil,
	)
	if missing.Code != http.StatusNotFound {
		t.Fatalf(
			"missing status=%d body=%s",
			missing.Code,
			missing.Body.String(),
		)
	}
	var missingBody map[string]any
	if err := json.Unmarshal(missing.Body.Bytes(), &missingBody); err != nil {
		t.Fatal(err)
	}
	if missingBody["failure"] != "schema owner missing" {
		t.Fatalf("missing body = %#v", missingBody)
	}
}

func seedAdministrationUserAssetsHTTPContract(
	t *testing.T,
	databasePath string,
) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	statements := []struct {
		query string
		args  []any
	}{
		{
			`INSERT INTO authentication_users (
				id, username, nickname, name, email, roles_json,
				is_admin, is_active, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, '["user"]', 0, 1, ?, ?)`,
			[]any{
				"asset-user",
				"asset-alice",
				"Asset Alice",
				"Alice",
				"asset@example.test",
				now.Format(time.RFC3339Nano),
				now.Format(time.RFC3339Nano),
			},
		},
		{
			`CREATE TABLE asset_registry (
				id TEXT PRIMARY KEY,
				assetType TEXT NOT NULL,
				originalFilename TEXT NOT NULL,
				storedFilename TEXT NOT NULL,
				mimeType TEXT NOT NULL,
				fileSize INTEGER NOT NULL,
				storagePath TEXT NOT NULL,
				uploaderUserId TEXT NOT NULL,
				createdAt
			)`,
			nil,
		},
		{
			`INSERT INTO asset_registry (
				id, assetType, originalFilename, storedFilename, mimeType,
				fileSize, storagePath, uploaderUserId, createdAt
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{
				"legacy-upload",
				"image",
				"input.png",
				"stored.png",
				"image/png",
				1234,
				"uploads/stored.png",
				"asset-user",
				now.UnixMilli(),
			},
		},
		{
			`CREATE TABLE usage_ledger (
				id TEXT PRIMARY KEY,
				userId TEXT NOT NULL,
				userName TEXT NOT NULL,
				userEmail TEXT NOT NULL,
				taskType TEXT NOT NULL,
				taskId TEXT NOT NULL,
				providerId TEXT NOT NULL,
				estimatedCredits INTEGER NOT NULL,
				taskStatus TEXT NOT NULL,
				params TEXT NOT NULL,
				resultUrls TEXT NOT NULL,
				createdAt,
				finishedAt
			)`,
			nil,
		},
		{
			`INSERT INTO usage_ledger (
				id, userId, userName, userEmail, taskType, taskId,
				providerId, estimatedCredits, taskStatus, params,
				resultUrls, createdAt, finishedAt
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{
				"legacy-ledger",
				"asset-user",
				"Asset Alice",
				"asset@example.test",
				"text-to-image",
				"legacy-task",
				"openai",
				1,
				"SUCCEEDED",
				`{"prompt":"schema"}`,
				`["https://assets.test/result.png"]`,
				now.UnixMilli(),
				now.UnixMilli(),
			},
		},
		{
			`CREATE TABLE audit_logs (
				id TEXT PRIMARY KEY,
				action TEXT NOT NULL,
				userId TEXT NOT NULL,
				userName TEXT NOT NULL DEFAULT '',
				resourceType TEXT NOT NULL DEFAULT '',
				resourceId TEXT NOT NULL DEFAULT '',
				detail TEXT NOT NULL DEFAULT '{}',
				ipAddress TEXT NOT NULL DEFAULT '',
				userAgent TEXT NOT NULL DEFAULT '',
				createdAt
			)`,
			nil,
		},
		{
			`INSERT INTO audit_logs (
				id, action, userId, userName, resourceType, resourceId,
				detail, createdAt
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{
				"legacy-download",
				"download_asset",
				"asset-user",
				"Asset Alice",
				"task_asset",
				"legacy-task",
				`{"source":"schema"}`,
				now.UnixMilli(),
			},
		},
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}
