package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestV1TaskCreateRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			audit := &application.Runtime.TaskAudit
			audit.CreateAction = "login"
			audit.DeleteAction = "logout"
			audit.ResourceType = "configured_task"
			audit.DetailFields.Capability = "configured_capability"
			audit.DetailFields.OfferingID = "configured_offering"
			audit.DetailFields.Replayed = "configured_replayed"
		},
		nil,
		nil,
		nil,
	)
	submit := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/tasks",
		bytes.NewBufferString(`{
			"capability":"text-to-image",
			"offering_id":"openai/gpt-image-2/text-to-image",
			"input":{"prompt":"audit task"}
		}`),
	)
	submit.Header.Set("Content-Type", "application/json")
	submit.Header.Set("X-User-ID", "owner-a")
	submitted := httptest.NewRecorder()
	handler.ServeHTTP(submitted, submit)
	if submitted.Code != http.StatusForbidden {
		t.Fatalf("write status=%d body=%s", submitted.Code, submitted.Body)
	}
}

func TestLegacyTaskCreateRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			audit := &application.Runtime.TaskAudit
			audit.CreateAction = "login"
			audit.ResourceType = "configured_legacy_task"
			audit.DetailFields.Capability = "configured_capability"
			audit.DetailFields.OfferingID = "configured_offering"
			audit.DetailFields.Replayed = "configured_replayed"
		},
		nil,
		nil,
		nil,
	)
	submitted := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/text-to-image",
		strings.NewReader(`{
			"prompt":"legacy audit task",
			"ai_model":"gpt-image-2.5-sunburst",
			"size":"1024x1024",
			"quality":"high"
		}`),
	)
	if submitted.Code != http.StatusForbidden {
		t.Fatalf("write status=%d body=%s", submitted.Code, submitted.Body)
	}
}

func TestV1TaskAssetDownloadAuditUsesProductSchema(t *testing.T) {
	t.Parallel()
	module, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			audit := &application.Runtime.AssetDelivery.TaskAssetAudit
			audit.DownloadAction = "login"
			audit.ResourceType = "configured_output_asset"
			audit.DetailFields.TaskID = "configured_task"
			audit.DetailFields.AssetID = "configured_asset"
		},
		nil,
		nil,
		nil,
	)
	task := createSucceededImageTask(t, module, "owner-a")
	if len(task.Assets) != 1 {
		t.Fatalf("task assets=%#v", task.Assets)
	}
	assetID := task.Assets[0].ID
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/tasks/"+string(task.ID)+"/assets/"+string(assetID),
		nil,
	)
	request.Header.Set("X-User-ID", "owner-a")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.Len() == 0 {
		t.Fatalf("download status=%d body=%s", response.Code, response.Body)
	}
	assertPersistedTaskAudit(
		t,
		handler,
		"login",
		"configured_output_asset",
		string(assetID),
		map[string]any{
			"configured_task":  string(task.ID),
			"configured_asset": string(assetID),
		},
	)
}

func assertPersistedTaskAudit(
	t *testing.T,
	handler http.Handler,
	action string,
	resourceType string,
	resourceID string,
	detail map[string]any,
) {
	t.Helper()
	response := administrationRequest(
		handler,
		http.MethodGet,
		"/api/admin/audit-logs?action="+action,
		"",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", response.Code, response.Body)
	}
	body := decodeAdministrationBody(t, response)
	if body["total"] != float64(1) {
		t.Fatalf("%s audit body=%#v", action, body)
	}
	event := body["data"].([]any)[0].(map[string]any)
	if event["action"] != action ||
		event["userId"] != "owner-a" ||
		event["resourceType"] != resourceType ||
		event["resourceId"] != resourceID {
		t.Fatalf("%s audit event=%#v", action, event)
	}
	var gotDetail map[string]any
	if err := json.Unmarshal([]byte(event["detail"].(string)), &gotDetail); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotDetail, detail) {
		t.Fatalf("%s audit detail=%#v", action, gotDetail)
	}
}
