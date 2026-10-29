package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestLegacyTaskHistoryUsesPrincipalScopeAndSchemaProjection(t *testing.T) {
	t.Parallel()
	module, regularHandler, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	own := submitHistoryFixture(t, module, "owner-a", generation.TextToImage, "alpha owl")
	_ = submitHistoryFixture(t, module, "owner-b", generation.TextToImage, "beta fox")

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/tasks?page=1&pageSize=32",
		nil,
	)
	request.Header.Set("X-User-ID", "owner-a")
	response := httptest.NewRecorder()
	regularHandler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	var body struct {
		Data []struct {
			ID        generation.TaskID `json:"id"`
			TaskType  string            `json:"taskType"`
			Status    string            `json:"status"`
			Progress  int               `json:"progress"`
			Prompt    string            `json:"prompt"`
			CreatedAt string            `json:"createdAt"`
		} `json:"data"`
		Total    int `json:"total"`
		Page     int `json:"page"`
		PageSize int `json:"pageSize"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 ||
		body.Page != 1 ||
		body.PageSize != 32 ||
		len(body.Data) != 1 ||
		body.Data[0].ID != own.TaskID ||
		body.Data[0].TaskType != string(generation.TextToImage) ||
		body.Data[0].Status != "PENDING" ||
		body.Data[0].Prompt != "alpha owl" ||
		body.Data[0].CreatedAt == "" {
		t.Fatalf("history body = %#v", body)
	}
}

func TestLegacyTaskHistoryAdminFiltersPagesAndRecentAreDataDriven(t *testing.T) {
	t.Parallel()
	module, adminHandler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})
	first := submitHistoryFixture(t, module, "admin-user", generation.TextToImage, "first")
	second := submitHistoryFixture(t, module, "admin-user", generation.TextTo3D, "second")
	// 他人的任务不会出现在管理员的历史列表里（#158）。
	submitHistoryFixture(t, module, "owner-b", generation.TextTo3D, "foreign")

	response := serveTaskHistoryRequest(
		adminHandler,
		"/api/tasks?page=1&pageSize=1&type=text-to-3d",
		"admin-user",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("filter status = %d, body = %s", response.Code, response.Body.String())
	}
	var filtered struct {
		Data []struct {
			ID generation.TaskID `json:"id"`
		} `json:"data"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(response.Body).Decode(&filtered); err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 1 ||
		len(filtered.Data) != 1 ||
		filtered.Data[0].ID != second.TaskID {
		t.Fatalf("filtered = %#v", filtered)
	}

	response = serveTaskHistoryRequest(
		adminHandler,
		"/api/tasks?search="+string(first.TaskID)+"&status=PENDING",
		"admin-user",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("search status = %d, body = %s", response.Code, response.Body.String())
	}
	var searched struct {
		Data []struct {
			ID generation.TaskID `json:"id"`
		} `json:"data"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(response.Body).Decode(&searched); err != nil {
		t.Fatal(err)
	}
	if searched.Total != 1 ||
		len(searched.Data) != 1 ||
		searched.Data[0].ID != first.TaskID {
		t.Fatalf("searched = %#v", searched)
	}

	response = serveTaskHistoryRequest(
		adminHandler,
		"/api/tasks?recent=1",
		"admin-user",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("recent status = %d, body = %s", response.Code, response.Body.String())
	}
	var recent struct {
		Data  []json.RawMessage `json:"data"`
		Total *int              `json:"total"`
	}
	if err := json.NewDecoder(response.Body).Decode(&recent); err != nil {
		t.Fatal(err)
	}
	if len(recent.Data) != 1 || recent.Total != nil {
		t.Fatalf("recent = %#v", recent)
	}
}

func TestLegacyTaskHistoryWireContractComesFromProductSchema(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarnessWithApplication(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			application.Runtime.HTTP.LegacyRoutes.Tasks =
				"/configured/task-history"
			api := &application.TaskHistory.API
			api.Query.Page = "current"
			api.Query.PageSize = "limit"
			api.Query.Recent = "latest"
			api.Query.Type = "kind"
			api.Query.Search = "identifier"
			api.Query.Status = "phase"
			api.Query.HideFailures = "withoutFailures"
			api.Fields.Data = "records"
			api.Fields.Total = "recordCount"
			api.Fields.Page = "current"
			api.Fields.PageSize = "limit"
			api.Fields.ID = "task_id"
			api.Fields.TaskType = "kind"
			api.Fields.Status = "phase"
			api.Fields.Progress = "percentage"
			api.Fields.ThumbnailURL = "preview"
			api.Fields.CreatedAt = "created_at"
			api.Fields.Prompt = "description"
			application.TaskHistory.Values.AllCapability = "*"
			application.TaskHistory.Values.Enabled = "yes"
		},
	)
	accepted := submitHistoryFixture(
		t,
		module,
		"admin-user",
		generation.TextToImage,
		"configured history",
	)

	response := serveTaskHistoryRequest(
		handler,
		"/configured/task-history?current=1&limit=8&kind=*&withoutFailures=yes",
		"admin-user",
	)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	records, ok := body["records"].([]any)
	if !ok || len(records) != 1 ||
		body["recordCount"] != float64(1) ||
		body["current"] != float64(1) ||
		body["limit"] != float64(8) {
		t.Fatalf("configured history envelope = %#v", body)
	}
	record, ok := records[0].(map[string]any)
	if !ok ||
		record["task_id"] != string(accepted.TaskID) ||
		record["kind"] != "text-to-image" ||
		record["phase"] != "PENDING" ||
		record["percentage"] != float64(0) ||
		record["description"] != "configured history" ||
		record["created_at"] == "" {
		t.Fatalf("configured history record = %#v", records[0])
	}
	for _, leaked := range []string{
		"data",
		"total",
		"page",
		"pageSize",
		"id",
		"taskType",
		"status",
		"progress",
		"thumbnailUrl",
		"createdAt",
		"prompt",
	} {
		if _, exists := body[leaked]; exists {
			t.Fatalf("default field %q leaked in envelope %#v", leaked, body)
		}
		if _, exists := record[leaked]; exists {
			t.Fatalf("default field %q leaked in record %#v", leaked, record)
		}
	}
}

func submitHistoryFixture(
	t *testing.T,
	module *generation.Module,
	owner generation.OwnerID,
	capability generation.Capability,
	prompt string,
) generation.Accepted {
	t.Helper()
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID:    owner,
		Capability: capability,
		Input:      json.RawMessage(`{"prompt":` + quoteJSON(t, prompt) + `}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return accepted
}

func quoteJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func serveTaskHistoryRequest(
	handler http.Handler,
	target string,
	owner string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("X-User-ID", owner)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestTaskHistoryHugePageDoesNotWrapToFirstPage(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	submitHistoryFixture(t, module, "owner", generation.TextToImage, "first page")
	response := serveTaskHistoryRequest(handler, "/api/tasks?page=4611686018427387905&pageSize=4", "owner")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var body struct {
		Data  []json.RawMessage `json:"data"`
		Total int               `json:"total"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 0 || body.Total != 1 {
		t.Fatalf("huge page wrapped: %+v", body)
	}
}
