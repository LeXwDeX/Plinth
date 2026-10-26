package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func ownerTaskCount(t *testing.T, module *generation.Module, owner generation.OwnerID) int {
	t.Helper()
	page, err := module.List(context.Background(), generation.OwnTasks(owner), generation.ListQuery{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return len(page.Tasks)
}

func assertOwnerTaskCount(t *testing.T, module *generation.Module, owner generation.OwnerID, want int) {
	t.Helper()
	if got := ownerTaskCount(t, module, owner); got != want {
		t.Fatalf("owner task count = %d, want %d", got, want)
	}
}

// Legacy writes are not an admission path. A 403 with an unchanged task set also
// proves no Attempt can reach Provider.Start through the Generation coordinator.
func TestLegacyAndBareTaskCreationRoutesFailClosedWithoutTask(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	const owner = generation.OwnerID("owner-a")
	before := ownerTaskCount(t, module, owner)

	cases := []struct {
		name string
		path string
		body string
	}{
		{"text-to-image", "/api/meshy/text-to-image", `{"prompt":"test","ai_model":"gpt-image-2.5-sunburst"}`},
		{"image-to-image", "/api/meshy/image-to-image", `{"prompt":"test","reference_image_urls":["https://assets.test/ref.png"]}`},
		{"text-to-3d", "/api/meshy/text-to-3d", `{"mode":"preview","prompt":"test","ai_model":"meshy-7"}`},
		{"image-to-3d", "/api/meshy/image-to-3d", `{"image_url":"https://assets.test/ref.png","ai_model":"meshy-7"}`},
		{"remesh", "/api/meshy/remesh", `{"model_url":"https://assets.test/model.glb","target_formats":["glb"]}`},
		{"retexture", "/api/meshy/retexture", `{"model_url":"https://assets.test/model.glb","text_style_prompt":"stone"}`},
		{"rigging", "/api/meshy/rigging", `{"model_url":"https://assets.test/model.glb"}`},
		{"animation", "/api/meshy/animations", `{"rig_task_id":"task-fixture","action_id":1}`},
		{"background-removal", "/api/meshy/background-removal", `{"files":[],"pipeline_params":{}}`},
		{"v1-task-create", "/api/v1/tasks", `{"capability":"text-to-image","offering_id":"openai/gpt-image-2/text-to-image","input":{"prompt":"test"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := serveLegacyRequest(handler, http.MethodPost, tc.path, strings.NewReader(tc.body))
			if response.Code != http.StatusForbidden {
				t.Fatalf("POST %s status = %d, body = %s", tc.path, response.Code, response.Body)
			}
			assertOwnerTaskCount(t, module, owner, before)
		})
	}

	// The background-removal endpoint also fails closed for its multipart wire form.
	formBody := &bytes.Buffer{}
	form := multipart.NewWriter(formBody)
	part, err := form.CreateFormFile("files", "sample.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("png-test")); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("pipeline_params", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/meshy/background-removal", formBody)
	request.Header.Set("X-User-ID", string(owner))
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("multipart background removal status = %d body = %s", response.Code, response.Body)
	}
	assertOwnerTaskCount(t, module, owner, before)
}

// Legacy reads and asset URLs remain projections of a task admitted through the
// prepared-input and human-grant test fixture, then deletion remains available.
func TestLegacyTaskReadDownloadAndDeleteUseApprovedTaskFixture(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	task := createSucceededImageTask(t, module, "owner-a")

	response := serveLegacyRequest(handler, http.MethodGet, "/api/meshy/text-to-image/"+string(task.ID), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("legacy read status = %d body = %s", response.Code, response.Body)
	}
	var projection struct {
		ID         generation.TaskID    `json:"id"`
		Status     generation.TaskState `json:"status"`
		OutputURLs []string             `json:"output_urls"`
	}
	if err := json.NewDecoder(response.Body).Decode(&projection); err != nil {
		t.Fatal(err)
	}
	if projection.ID != task.ID || projection.Status != generation.TaskSucceeded || len(projection.OutputURLs) != 1 {
		t.Fatalf("legacy task projection = %#v", projection)
	}
	asset := serveLegacyRequest(handler, http.MethodGet, projection.OutputURLs[0], nil)
	if asset.Code != http.StatusOK || asset.Body.Len() == 0 {
		t.Fatalf("legacy asset status = %d body bytes = %d", asset.Code, asset.Body.Len())
	}
	deleted := serveLegacyRequest(handler, http.MethodDelete, "/api/meshy/text-to-image/"+string(task.ID), nil)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status = %d body = %s", deleted.Code, deleted.Body)
	}
	missing := serveLegacyRequest(handler, http.MethodGet, "/api/meshy/text-to-image/"+string(task.ID), nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("deleted item status = %d body = %s", missing.Code, missing.Body)
	}
}
