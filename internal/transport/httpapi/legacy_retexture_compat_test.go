package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestLegacyRetextureJSONWritesRequireApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	cases := []string{
		`{"model_url":"https://assets.test/source.glb","text_style_prompt":"painted stone"}`,
		`{"input_task_id":"task-fixture","image_style_url":"https://assets.test/style.png"}`}
	for _, body := range cases {
		response := serveLegacyRequest(handler, http.MethodPost, "/api/meshy/retexture", strings.NewReader(body))
		if response.Code != http.StatusForbidden {
			t.Fatalf("legacy POST status = %d body = %s", response.Code, response.Body)
		}
	}
}

func TestLegacyRetextureMultipartWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	submitted := serveLegacyMultipartTask(
		t,
		handler,
		"/api/meshy/retexture",
		"model_file",
		"character.glb",
		"params",
		`{"text_style_prompt":"painted stone","enable_pbr":true,"resolution":"4096"}`,
		"mock retexture glb",
	)
	if submitted.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", submitted.Code, submitted.Body.String())
	}
}

func TestLegacyRetextureMalformedWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	const configuredText = "configured retexture invalid input"
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			application.Content.Messages["error.invalid_input"] = configuredText
		},
	)
	response := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/retexture",
		strings.NewReader(`{}`),
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d body = %s", response.Code, response.Body)
	}
}
