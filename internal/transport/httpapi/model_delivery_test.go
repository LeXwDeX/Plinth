package httpapi_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func createSucceededMeshyModelTask(t *testing.T, module *generation.Module, owner generation.OwnerID) generation.Task {
	t.Helper()
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID: owner, Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d",
		Input: []byte(`{"prompt":"model delivery"}`), IdempotencyKey: "model-delivery-" + string(owner),
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if _, err := module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
			WorkerID: "model-delivery", LeaseFor: time.Minute, BatchSize: 10,
		}); err != nil {
			t.Fatal(err)
		}
		task, err := module.Get(context.Background(), owner, accepted.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State == generation.TaskSucceeded {
			return task
		}
		if task.State.Terminal() {
			t.Fatalf("task = %#v", task)
		}
		time.Sleep(12 * time.Millisecond)
	}
	t.Fatal("model task did not succeed in time")
	return generation.Task{}
}

func createSucceededTripoModelTask(t *testing.T, module *generation.Module, owner generation.OwnerID) generation.Task {
	t.Helper()
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID: owner, Capability: generation.TextTo3D, OfferingID: "tripo/tripo-v3.1-full/text-to-3d",
		Input: []byte(`{"prompt":"model delivery","authorize_extra_credits":true}`), IdempotencyKey: "model-delivery-tripo-" + string(owner),
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if _, err := module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
			WorkerID: "model-delivery", LeaseFor: time.Minute, BatchSize: 10,
		}); err != nil {
			t.Fatal(err)
		}
		task, err := module.Get(context.Background(), owner, accepted.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State == generation.TaskSucceeded {
			return task
		}
		if task.State.Terminal() {
			t.Fatalf("task = %#v", task)
		}
		time.Sleep(12 * time.Millisecond)
	}
	t.Fatal("tripo model task did not succeed in time")
	return generation.Task{}
}

func modelDeliveryRequest(handler http.Handler, method, target, owner string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("X-User-ID", owner)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestModelFormatsAndArchiveAreOwnerScoped(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	task := createSucceededMeshyModelTask(t, module, "owner-a")
	base := "/api/v1/tasks/" + string(task.ID)

	for _, target := range []string{base + "/formats", base + "/archive"} {
		foreign := modelDeliveryRequest(handler, http.MethodGet, target, "owner-b")
		if foreign.Code != http.StatusNotFound {
			t.Fatalf("foreign GET %s = %d %s", target, foreign.Code, foreign.Body)
		}
	}
	if foreign := modelDeliveryRequest(handler, http.MethodPost, base+"/formats/complete", "owner-b"); foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign complete = %d %s", foreign.Code, foreign.Body)
	}

	formats := modelDeliveryRequest(handler, http.MethodGet, base+"/formats", "owner-a")
	if formats.Code != http.StatusOK || formats.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("formats = %d %v", formats.Code, formats.Header())
	}
	var coverage struct {
		Applicable bool `json:"applicable"`
		Formats    []struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			AssetID string `json:"asset_id"`
			Reason  string `json:"reason"`
		} `json:"formats"`
		CompletionCredits int  `json:"completion_credits"`
		CanComplete       bool `json:"can_complete"`
		ArchiveAvailable  bool `json:"archive_available"`
	}
	if err := json.Unmarshal(formats.Body.Bytes(), &coverage); err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, row := range coverage.Formats {
		status[row.ID] = row.Status
	}
	if !coverage.Applicable || coverage.CanComplete || coverage.CompletionCredits != 0 || !coverage.ArchiveAvailable ||
		status["glb"] != "available" || status["usdz"] != "available" || status["gltf"] != "unavailable" {
		t.Fatalf("coverage = %s", formats.Body)
	}
	if _, exists := status["blend"]; exists {
		t.Fatalf("coverage still declares BLEND: %s", formats.Body)
	}
	if strings.Contains(formats.Body.String(), "https://") || strings.Contains(formats.Body.String(), "storage_key") {
		t.Fatalf("coverage leaks provider or storage data: %s", formats.Body)
	}

	archive := modelDeliveryRequest(handler, http.MethodGet, base+"/archive", "owner-a")
	if archive.Code != http.StatusOK || archive.Header().Get("Content-Type") != "application/zip" ||
		archive.Header().Get("Cache-Control") != "private, no-store" ||
		!strings.Contains(archive.Header().Get("Content-Disposition"), "attachment; filename=\"model-"+string(task.ID)+".zip\"") {
		t.Fatalf("archive = %d %v", archive.Code, archive.Header())
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Body.Bytes()), int64(archive.Body.Len()))
	if err != nil {
		t.Fatalf("response is not a ZIP: %v", err)
	}
	entries := map[string][]byte{}
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		entries[file.Name], _ = io.ReadAll(handle)
		handle.Close()
	}
	for _, name := range []string{"model.glb", "model.fbx", "model.obj", "model.stl", "model.usdz", "model.3mf", "README.txt"} {
		if len(entries[name]) == 0 {
			t.Fatalf("missing %q in ZIP: have %d entries", name, len(entries))
		}
	}
	// The bytes are exactly what the single-asset route serves.
	for _, asset := range task.Assets {
		if asset.Variant != "glb" {
			continue
		}
		single := modelDeliveryRequest(handler, http.MethodGet, base+"/assets/"+string(asset.ID), "owner-a")
		if !bytes.Equal(single.Body.Bytes(), entries["model.glb"]) {
			t.Fatal("ZIP glb differs from the delivered asset")
		}
	}
	readme := string(entries["README.txt"])
	if !strings.Contains(readme, string(task.ID)) || !strings.Contains(readme, "glTF") || strings.Contains(readme, "BLEND") {
		t.Fatalf("README = \n%s", readme)
	}
	if strings.Contains(string(archive.Body.Bytes()), "https://") {
		t.Fatal("ZIP carries an address")
	}
}

func TestModelArchiveSupportsByteRanges(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	task := createSucceededMeshyModelTask(t, module, "owner-a")
	target := "/api/v1/tasks/" + string(task.ID) + "/archive"

	full := modelDeliveryRequest(handler, http.MethodGet, target, "owner-a")
	if full.Code != http.StatusOK {
		t.Fatalf("archive = %d %s", full.Code, full.Body)
	}
	if got := full.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q", got)
	}
	// The ordinary response streams Deflate entries, so its length is not
	// known up front; progress comes from the client side instead.
	if got := full.Header().Get("Content-Length"); got != "" {
		t.Fatalf("streaming archive Content-Length = %q, want unknown", got)
	}
	body := full.Body.Bytes()
	if len(body) < 128 {
		t.Fatalf("archive is too small for a range test: %d bytes", len(body))
	}

	for _, tc := range []struct {
		header string
		first  int
		last   int
	}{
		{header: "bytes=0-63", first: 0, last: 63},
		{header: "bytes=-16", first: len(body) - 16, last: len(body) - 1},
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("X-User-ID", "owner-a")
		request.Header.Set("Range", tc.header)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusPartialContent {
			t.Fatalf("%s = %d %s", tc.header, response.Code, response.Body)
		}
		want := fmt.Sprintf("bytes %d-%d/%d", tc.first, tc.last, len(body))
		if got := response.Header().Get("Content-Range"); got != want {
			t.Fatalf("%s Content-Range = %q, want %q", tc.header, got, want)
		}
		if got := response.Header().Get("Content-Length"); got != fmt.Sprint(tc.last-tc.first+1) {
			t.Fatalf("%s Content-Length = %q", tc.header, got)
		}
		if !bytes.Equal(response.Body.Bytes(), body[tc.first:tc.last+1]) {
			t.Fatalf("%s bytes differ from the full archive", tc.header)
		}
	}
}

// recordingResponseWriter keeps the call order so the test proves the status
// and headers are flushed once Prepare has finished, before the first entry is
// written, instead of measuring wall-clock time.
type recordingResponseWriter struct {
	*httptest.ResponseRecorder
	order []string
}

func (writer *recordingResponseWriter) WriteHeader(status int) {
	writer.order = append(writer.order, fmt.Sprintf("status:%d", status))
	writer.ResponseRecorder.WriteHeader(status)
}

func (writer *recordingResponseWriter) Flush() {
	writer.order = append(writer.order, "flush")
	writer.ResponseRecorder.Flush()
}

func (writer *recordingResponseWriter) Write(data []byte) (int, error) {
	if len(writer.order) == 0 || writer.order[len(writer.order)-1] != "body" {
		writer.order = append(writer.order, "body")
	}
	return writer.ResponseRecorder.Write(data)
}

func TestModelArchiveFlushesHeadersBeforeTheFirstEntry(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	task := createSucceededMeshyModelTask(t, module, "owner-a")
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+string(task.ID)+"/archive", nil)
	request.Header.Set("X-User-ID", "owner-a")
	writer := &recordingResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(writer, request)
	if writer.Code != http.StatusOK {
		t.Fatalf("archive = %d %s", writer.Code, writer.Body)
	}
	if len(writer.order) < 3 || writer.order[0] != "status:200" || writer.order[1] != "flush" || writer.order[2] != "body" {
		t.Fatalf("archive call order = %v; headers must flush before the first entry", writer.order)
	}
}

func TestModelArchiveDownloadIsAudited(t *testing.T) {
	t.Parallel()
	module, handler, _, _ := newHTTPHarnessWithServices(t, []string{"admin"}, nil, nil, nil, nil)
	task := createSucceededMeshyModelTask(t, module, "owner-a")
	response := modelDeliveryRequest(handler, http.MethodGet, "/api/v1/tasks/"+string(task.ID)+"/archive", "owner-a")
	if response.Code != http.StatusOK {
		t.Fatalf("archive = %d %s", response.Code, response.Body)
	}
	audit := administrationRequest(handler, http.MethodGet, "/api/admin/audit-logs?action=download_model_archive", "", nil)
	if audit.Code != http.StatusOK {
		t.Fatalf("audit = %d %s", audit.Code, audit.Body)
	}
	body := decodeAdministrationBody(t, audit)
	if body["total"] != float64(1) {
		t.Fatalf("audit body = %#v", body)
	}
	event := body["data"].([]any)[0].(map[string]any)
	if event["action"] != "download_model_archive" || event["userId"] != "owner-a" ||
		event["resourceType"] != "model_archive" || event["resourceId"] != string(task.ID) {
		t.Fatalf("audit event = %#v", event)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(event["detail"].(string)), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["taskId"] != string(task.ID) || detail["entries"] != float64(7) ||
		detail["missing"] != "gltf" || detail["bytes"].(float64) <= 0 {
		t.Fatalf("audit detail = %#v", detail)
	}
}

func TestModelArchiveRespectsTheSchemaSizeBound(t *testing.T) {
	t.Parallel()
	module, handler, _, _ := newHTTPHarnessWithServices(t, nil, func(application *catalog.ApplicationSchema) {
		application.ModelDelivery.Archive.MaxBytes = 16
	}, nil, nil, nil)
	task := createSucceededMeshyModelTask(t, module, "owner-a")
	formats := modelDeliveryRequest(handler, http.MethodGet, "/api/v1/tasks/"+string(task.ID)+"/formats", "owner-a")
	if formats.Code != http.StatusOK || !strings.Contains(formats.Body.String(), `"archive_available":false`) {
		t.Fatalf("oversized coverage = %d %s", formats.Code, formats.Body)
	}
	response := modelDeliveryRequest(handler, http.MethodGet, "/api/v1/tasks/"+string(task.ID)+"/archive", "owner-a")
	if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), "超过一键打包的上限") {
		t.Fatalf("oversized archive = %d %s", response.Code, response.Body)
	}
	if response.Header().Get("Content-Type") == "application/zip" {
		t.Fatal("an oversized request must not start a ZIP")
	}
}

func TestCompleteModelFormatsStartsOneIdempotentPlan(t *testing.T) {
	t.Parallel()
	module, handler, _, _ := newHTTPHarnessWithCatalogOverride(t, nil, nil, nil, nil, nil, catalogtest.LoadDefaultWithTripoConversion(t))
	task := createSucceededTripoModelTask(t, module, "owner-a")
	url := "/api/v1/tasks/" + string(task.ID) + "/formats/complete"
	first := modelDeliveryRequest(handler, http.MethodPost, url, "owner-a")
	if first.Code != http.StatusAccepted {
		t.Fatalf("complete = %d %s", first.Code, first.Body)
	}
	var coverage struct {
		CanComplete bool `json:"can_complete"`
		Steps       []struct {
			OfferingID string   `json:"offering_id"`
			State      string   `json:"state"`
			PlanID     string   `json:"plan_id"`
			Formats    []string `json:"formats"`
		} `json:"steps"`
		Formats []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"formats"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &coverage); err != nil {
		t.Fatal(err)
	}
	if coverage.CanComplete || len(coverage.Steps) != 1 || coverage.Steps[0].OfferingID != "tripo/tripo-v3.1/format-conversion" ||
		coverage.Steps[0].State != "PENDING" || coverage.Steps[0].PlanID == "" || coverage.Steps[0].Formats[0] != "gltf" {
		t.Fatalf("coverage = %s", first.Body)
	}
	for _, row := range coverage.Formats {
		if row.ID == "gltf" && row.Status != "converting" {
			t.Fatalf("gltf = %q", row.Status)
		}
	}
	again := modelDeliveryRequest(handler, http.MethodPost, url, "owner-a")
	if again.Code != http.StatusConflict {
		t.Fatalf("repeat complete = %d %s", again.Code, again.Body)
	}
}

func TestModelFormatsRefuseTasksThatAreNotModels(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	image := createSucceededImageTask(t, module, "owner-a")
	base := "/api/v1/tasks/" + string(image.ID)
	formats := modelDeliveryRequest(handler, http.MethodGet, base+"/formats", "owner-a")
	if formats.Code != http.StatusOK || !strings.Contains(formats.Body.String(), `"applicable":false`) {
		t.Fatalf("image formats = %d %s", formats.Code, formats.Body)
	}
	if complete := modelDeliveryRequest(handler, http.MethodPost, base+"/formats/complete", "owner-a"); complete.Code != http.StatusConflict {
		t.Fatalf("image complete = %d %s", complete.Code, complete.Body)
	}
	if archive := modelDeliveryRequest(handler, http.MethodGet, base+"/archive", "owner-a"); archive.Code != http.StatusConflict {
		t.Fatalf("image archive = %d %s", archive.Code, archive.Body)
	}
}

// #323: a format row starts only the step that delivers that format. The body
// is optional; without it the request completes every obtainable format.
func TestCompleteModelFormatsStartsTheStepOfOneRequestedFormat(t *testing.T) {
	t.Parallel()
	module, handler, _, _ := newHTTPHarnessWithCatalogOverride(t, nil, nil, nil, nil, nil, catalogtest.LoadDefaultWithTripoConversion(t))
	task := createSucceededTripoModelTask(t, module, "owner-a")
	url := "/api/v1/tasks/" + string(task.ID) + "/formats/complete"
	post := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		request.Header.Set("X-User-ID", "owner-a")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for _, malformed := range []string{`{"formats":"gltf"}`, `{"format":"gltf"}`, `{"formats":["gltf"]} {}`} {
		if response := post(malformed); response.Code != http.StatusBadRequest {
			t.Fatalf("malformed %s = %d %s", malformed, response.Code, response.Body)
		}
	}
	// A format no step delivers (here: already saved) starts nothing.
	if response := post(`{"formats":["glb"]}`); response.Code != http.StatusConflict {
		t.Fatalf("available format = %d %s", response.Code, response.Body)
	}
	first := post(`{"formats":["gltf"]}`)
	if first.Code != http.StatusAccepted || !strings.Contains(first.Body.String(), `"id":"gltf"`) {
		t.Fatalf("row complete = %d %s", first.Code, first.Body)
	}
	var coverage struct {
		Formats []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"formats"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &coverage); err != nil {
		t.Fatal(err)
	}
	for _, row := range coverage.Formats {
		if row.ID == "gltf" && row.Status != "converting" {
			t.Fatalf("gltf = %q", row.Status)
		}
	}
	if again := post(`{"formats":["gltf"]}`); again.Code != http.StatusConflict {
		t.Fatalf("repeat row complete = %d %s", again.Code, again.Body)
	}
}
