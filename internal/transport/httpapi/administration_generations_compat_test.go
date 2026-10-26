package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestAdministrationGenerationDownloadSupportsLegacyHistory(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		nil,
		nil,
		nil,
		nil,
		seedLegacyAdministrationGeneration,
	)

	detailResponse := httptest.NewRecorder()
	handler.ServeHTTP(detailResponse, httptest.NewRequest(
		http.MethodGet,
		"/api/admin/generations/legacy-image-task",
		nil,
	))
	if detailResponse.Code != http.StatusOK ||
		strings.Contains(detailResponse.Body.String(), "/api/gpt-images/") {
		t.Fatalf("legacy public detail status=%d body=%s", detailResponse.Code, detailResponse.Body.String())
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/api/admin/generations/legacy-image-task/download",
			nil,
		),
	)
	if response.Code != http.StatusOK ||
		response.Header().Get("Content-Type") != "image/webp" ||
		!strings.Contains(
			response.Header().Get("Content-Disposition"),
			"historical_0.webp",
		) ||
		response.Body.String() != "historical-image" {
		t.Fatalf(
			"legacy download status=%d headers=%v body=%q",
			response.Code,
			response.Header(),
			response.Body.String(),
		)
	}
}

func TestAdministrationGenerationListAndDetailUseProductSchema(
	t *testing.T,
) {
	t.Parallel()
	module, handler, _ := newHTTPHarnessWithApplication(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			routes := &application.Runtime.HTTP.LegacyRoutes
			routes.AdminGenerations = "/configured/admin/jobs"
			routes.AdminGeneration = "/configured/admin/jobs/{jobID}"
			routes.AdminGenerationDownload =
				"/configured/admin/jobs/{jobID}/download"
			schema := &application.Administration.Generations
			schema.API.PathParameters.TaskID = "jobID"
			schema.API.Query.Page = "current"
			schema.API.Query.PageSize = "count"
			schema.API.Query.UserID = "owner"
			schema.API.Query.TaskType = "kind"
			schema.API.Query.Status = "phase"
			schema.API.Query.From = "startedAfter"
			schema.API.Query.To = "startedBefore"
			schema.Assets.AssetFilenameTemplate =
				"configured-{role}{variant_segment}-{ordinal}{extension}"
			fields := &schema.API.Fields
			fields.Data = "items"
			fields.Total = "itemCount"
			fields.Page = "current"
			fields.PageSize = "count"
			fields.Ledger = "job"
			fields.TaskCache = "cache"
			fields.TaskID = "jobId"
			fields.TaskType = "kind"
			fields.TaskStatus = "phase"
			fields.Params = "arguments"
			fields.ResultURLs = "outputs"
		},
	)
	accepted, err := submitHarnessTask(t, module,
		context.Background(),
		generation.SubmitCommand{
			OwnerID:    "local-user",
			Capability: generation.TextToImage,
			OfferingID: "openai/gpt-image-2/text-to-image",
			Input:      json.RawMessage(`{"prompt":"configured job"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	listRequest := httptest.NewRequest(
		http.MethodGet,
		"/configured/admin/jobs?current=1&count=9&owner=local-user"+
			"&kind=text-to-image&phase=QUEUED",
		nil,
	)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf(
			"list status=%d body=%s",
			listResponse.Code,
			listResponse.Body.String(),
		)
	}
	var list map[string]any
	if err := json.Unmarshal(listResponse.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	items, ok := list["items"].([]any)
	if !ok || len(items) != 1 ||
		int(list["itemCount"].(float64)) != 1 ||
		int(list["count"].(float64)) != 9 {
		t.Fatalf("configured list wire = %#v", list)
	}
	item := items[0].(map[string]any)
	if item["jobId"] != string(accepted.TaskID) ||
		item["kind"] != "text-to-image" ||
		item["phase"] != "QUEUED" {
		t.Fatalf("configured generation item = %#v", item)
	}
	if _, ok := item["arguments"].(string); !ok {
		t.Fatalf("list arguments are not legacy JSON text: %#v", item)
	}
	if _, ok := item["outputs"].(string); !ok {
		t.Fatalf("list outputs are not legacy JSON text: %#v", item)
	}

	detailRequest := httptest.NewRequest(
		http.MethodGet,
		"/configured/admin/jobs/"+url.PathEscape(string(accepted.TaskID)),
		nil,
	)
	detailResponse := httptest.NewRecorder()
	handler.ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf(
			"detail status=%d body=%s",
			detailResponse.Code,
			detailResponse.Body.String(),
		)
	}
	var detail map[string]any
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	job, ok := detail["job"].(map[string]any)
	if !ok || job["jobId"] != string(accepted.TaskID) {
		t.Fatalf("configured detail wire = %#v", detail)
	}
	if _, ok := job["arguments"].(map[string]any); !ok {
		t.Fatalf("detail arguments are not an object: %#v", job)
	}
	if _, ok := job["outputs"].([]any); !ok {
		t.Fatalf("detail outputs are not an array: %#v", job)
	}
	if _, ok := detail["cache"].(map[string]any); !ok {
		t.Fatalf("native cache was not projected: %#v", detail)
	}

	readAuditRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/admin/audit-logs?action=admin_read_generations",
		nil,
	)
	readAuditResponse := httptest.NewRecorder()
	handler.ServeHTTP(readAuditResponse, readAuditRequest)
	if readAuditResponse.Code != http.StatusOK {
		t.Fatalf("read audit status=%d body=%s", readAuditResponse.Code, readAuditResponse.Body.String())
	}
	var readAudit map[string]any
	if err := json.Unmarshal(readAuditResponse.Body.Bytes(), &readAudit); err != nil {
		t.Fatal(err)
	}
	readLogs, ok := readAudit["data"].([]any)
	if !ok || len(readLogs) == 0 {
		t.Fatalf("generation detail read audit = %#v", readAudit)
	}
	readFound := false
	for _, item := range readLogs {
		readLog := item.(map[string]any)
		if readLog["resourceType"] == "admin_task" && readLog["resourceId"] == string(accepted.TaskID) {
			readFound = true
			break
		}
	}
	if !readFound {
		t.Fatalf("generation detail read audit = %#v", readAudit)
	}

	reconcileAdministrationGeneration(
		t,
		module,
		accepted.TaskID,
	)
	downloadRequest := httptest.NewRequest(
		http.MethodGet,
		"/configured/admin/jobs/"+
			url.PathEscape(string(accepted.TaskID))+
			"/download",
		nil,
	)
	downloadResponse := httptest.NewRecorder()
	handler.ServeHTTP(downloadResponse, downloadRequest)
	if downloadResponse.Code != http.StatusOK {
		t.Fatalf(
			"download status=%d body=%s",
			downloadResponse.Code,
			downloadResponse.Body.String(),
		)
	}
	if !strings.Contains(
		downloadResponse.Header().Get("Content-Disposition"),
		"configured-image-2.png",
	) || downloadResponse.Header().Get("Content-Type") != "image/png" ||
		downloadResponse.Body.Len() == 0 {
		t.Fatalf(
			"download headers=%v bytes=%d",
			downloadResponse.Header(),
			downloadResponse.Body.Len(),
		)
	}

	auditRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/admin/audit-logs?action=download_asset",
		nil,
	)
	auditResponse := httptest.NewRecorder()
	handler.ServeHTTP(auditResponse, auditRequest)
	if auditResponse.Code != http.StatusOK {
		t.Fatalf(
			"audit status=%d body=%s",
			auditResponse.Code,
			auditResponse.Body.String(),
		)
	}
	var auditLogs map[string]any
	if err := json.Unmarshal(auditResponse.Body.Bytes(), &auditLogs); err != nil {
		t.Fatal(err)
	}
	logs, ok := auditLogs["data"].([]any)
	if !ok || len(logs) == 0 {
		t.Fatalf("download audit logs = %#v", auditLogs)
	}
	log := logs[0].(map[string]any)
	if log["resourceType"] != "admin_task" ||
		log["resourceId"] != string(accepted.TaskID) {
		t.Fatalf("download audit log = %#v", log)
	}
}

func seedLegacyAdministrationGeneration(
	t *testing.T,
	databasePath string,
) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC().UnixMilli()
	statements := []struct {
		query string
		args  []any
	}{
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
				"legacy-image-ledger",
				"legacy-user",
				"Legacy User",
				"legacy@example.test",
				"text-to-image",
				"legacy-image-task",
				"openai",
				1,
				"SUCCEEDED",
				`{"prompt":"history"}`,
				`["/api/gpt-images/historical_0.webp"]`,
				now,
				now,
			},
		},
		{
			`CREATE TABLE task_cache (
				taskId TEXT PRIMARY KEY,
				taskType TEXT NOT NULL,
				status TEXT NOT NULL,
				providerId TEXT NOT NULL,
				data TEXT NOT NULL,
				cachedAt,
				updatedAt
			)`,
			nil,
		},
		{
			`INSERT INTO task_cache (
				taskId, taskType, status, providerId, data, cachedAt, updatedAt
			) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			[]any{
				"legacy-image-task",
				"text-to-image",
				"SUCCEEDED",
				"openai",
				`{"image_urls":["/api/gpt-images/historical_0.webp"]}`,
				now,
				now,
			},
		},
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAdministrationGenerationContractRequiresConfiguredPermission(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/api/admin/generations",
			nil,
		),
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf(
			"status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
}

func reconcileAdministrationGeneration(
	t *testing.T,
	module *generation.Module,
	taskID generation.TaskID,
) {
	t.Helper()
	for range 8 {
		if _, err := module.ReconcileOnce(
			context.Background(),
			generation.CoordinatorConfig{
				WorkerID:  "administration-download-worker",
				LeaseFor:  time.Minute,
				BatchSize: 10,
			},
		); err != nil {
			t.Fatal(err)
		}
		task, err := module.Get(
			context.Background(),
			"local-user",
			taskID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() {
			if task.State != generation.TaskSucceeded {
				t.Fatalf("task = %#v", task)
			}
			return
		}
		time.Sleep(12 * time.Millisecond)
	}
	t.Fatal("task did not reach a terminal state")
}
