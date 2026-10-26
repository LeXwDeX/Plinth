package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestLegacyRemeshJSONWritesRequireApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	cases := []string{
		`{"model_url":"https://assets.test/source.glb","target_formats":["glb","fbx"]}`,
		`{"input_task_id":"task-fixture","target_formats":["glb"]}`}
	for _, body := range cases {
		response := serveLegacyRequest(handler, http.MethodPost, "/api/meshy/remesh", strings.NewReader(body))
		if response.Code != http.StatusForbidden {
			t.Fatalf("legacy POST status = %d body = %s", response.Code, response.Body)
		}
	}
}

func TestLegacyRemeshMultipartWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	submitted := serveLegacyMultipartTask(
		t,
		handler,
		"/api/meshy/remesh",
		"model_file",
		"character.glb",
		"params",
		`{"topology":"quad","target_formats":["glb","fbx"],"target_polycount":12000}`,
		"mock glb body",
	)
	if submitted.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", submitted.Code, submitted.Body.String())
	}
}

func TestLegacyRemeshConfiguredMultipartWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	const (
		missingText   = "configured missing model text"
		extensionText = "configured extension {extension}; choices={extensions}"
		paramsText    = "configured params text"
	)
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			application.Content.Messages["test.remesh.missing"] = missingText
			application.Content.Messages["test.remesh.extension"] = extensionText
			application.Content.Messages["test.remesh.params"] = paramsText
			// Every message key exists in every locale.
			english := application.Localization.Bundles["en"].Messages
			english["test.remesh.missing"] = missingText
			english["test.remesh.extension"] = extensionText
			english["test.remesh.params"] = paramsText
			for index := range application.Capabilities {
				capability := &application.Capabilities[index]
				if capability.ID != generation.Remesh {
					continue
				}
				capability.LegacyAPI.Multipart.FileField = "schema_model"
				capability.LegacyAPI.Multipart.ParamsField = "schema_params"
				capability.LegacyAPI.Multipart.MissingFileMessageKey = "test.remesh.missing"
				capability.LegacyAPI.Multipart.InvalidExtensionMessageKey = "test.remesh.extension"
				capability.LegacyAPI.Multipart.InvalidParamsMessageKey = "test.remesh.params"
			}
		},
	)

	valid := serveLegacyMultipartTask(
		t,
		handler,
		"/api/meshy/remesh",
		"schema_model",
		"schema.obj",
		"schema_params",
		`{"target_formats":["obj"]}`,
		"mock obj",
	)
	if valid.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", valid.Code, valid.Body.String())
	}
}

func TestLegacyRemeshConfiguredRulesWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	const configuredText = "configured remesh invalid input"
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
		"/api/meshy/remesh",
		strings.NewReader(`{}`),
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d body = %s", response.Code, response.Body)
	}
}

func TestLegacyRemeshUploadRulesAreProjectedFromOfferingSchema(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/providers?capability=remesh&projection=extended",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("provider status = %d, body = %s", response.Code, response.Body.String())
	}
	var discovered struct {
		Providers []struct {
			ID           string `json:"id"`
			InputSchemas map[generation.Capability][]struct {
				Name       string   `json:"name"`
				Extensions []string `json:"extensions"`
				MaxBytes   int64    `json:"maxBytes"`
			} `json:"inputSchemas"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&discovered); err != nil {
		t.Fatal(err)
	}
	// #185 adds Tripo Decimate (triangle) beside Meshy Remesh. It takes only
	// an exact native Tripo Task, so it projects no upload input.
	if len(discovered.Providers) != 2 || discovered.Providers[0].ID != "meshy-processing" ||
		discovered.Providers[1].ID != "tripo-decimate-triangle" {
		t.Fatalf("providers = %#v", discovered.Providers)
	}
	for _, decimate := range discovered.Providers[1:] {
		if len(decimate.InputSchemas[generation.Remesh]) != 0 {
			t.Fatalf("Tripo Decimate projected an upload input: %#v", decimate.InputSchemas)
		}
	}
	inputs := discovered.Providers[0].InputSchemas[generation.Remesh]
	if len(inputs) != 1 ||
		inputs[0].Name != "source_asset" ||
		len(inputs[0].Extensions) != 5 ||
		inputs[0].MaxBytes != 70*1024*1024 {
		t.Fatalf("remesh input schemas = %#v", inputs)
	}
}

func serveLegacyMultipartTask(
	t *testing.T,
	handler http.Handler,
	target string,
	fileField string,
	filename string,
	paramsField string,
	params string,
	content string,
) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if fileField != "" {
		part, err := writer.CreateFormFile(fileField, filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, content); err != nil {
			t.Fatal(err)
		}
	}
	if paramsField != "" {
		if err := writer.WriteField(paramsField, params); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, target, &body)
	request.Header.Set("X-User-ID", "owner-a")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertLegacyErrorText(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantStatus int,
	wantText string,
) {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.Code != wantStatus || body.Error != wantText {
		t.Fatalf("status = %d, error = %q, body = %s", response.Code, body.Error, response.Body.String())
	}
}
