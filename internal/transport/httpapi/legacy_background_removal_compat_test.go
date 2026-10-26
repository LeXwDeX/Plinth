package httpapi_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestLegacyBackgroundRemovalWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	writeBackgroundRemovalFile(t, form, "first.png", "image/png", "first")
	writeBackgroundRemovalFile(t, form, "second.jpg", "image/jpeg", "second")
	if err := form.WriteField("pipeline_params", `{"expand":360}`); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/meshy/background-removal",
		body,
	)
	request.Header.Set("X-User-ID", "owner-a")
	request.Header.Set("Content-Type", form.FormDataContentType())
	submitted := httptest.NewRecorder()
	handler.ServeHTTP(submitted, request)
	if submitted.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", submitted.Code, submitted.Body.String())
	}
}

func TestLegacyBackgroundRemovalMalformedWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	if err := form.WriteField("pipeline_params", "{"); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/meshy/background-removal",
		body,
	)
	request.Header.Set("X-User-ID", "owner-a")
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
}

func TestLegacyBackgroundRemovalConfiguredWriteRequiresApprovedExecution(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			for index := range application.Capabilities {
				capability := &application.Capabilities[index]
				if capability.ID != generation.BackgroundRemove {
					continue
				}
				legacy := capability.LegacyAPI
				legacy.Path = "/api/schema/background"
				legacy.Multipart.FileField = "schema_files"
				legacy.Multipart.ParamsField = "schema_pipeline"
				legacy.AssetDownload.Path =
					"/api/schema/background/download/{schemaToken}"
				legacy.AssetDownload.TokenParameter = "schemaToken"
				legacy.AssetDownload.FilenameTemplate =
					"schema-{token}.png"
				legacy.AssetDownload.CacheFilenameTemplate =
					"{schemaToken}.png"
				for projectionIndex := range legacy.OutputProjections {
					projection := &legacy.OutputProjections[projectionIndex]
					if projection.Mode != "records" {
						continue
					}
					projection.Name = "schema_items"
					for fieldIndex := range projection.RecordFields {
						field := &projection.RecordFields[fieldIndex]
						field.Name = "schema_" + field.Name
					}
				}
			}
		},
	)
	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	writeBackgroundRemovalFileField(
		t,
		form,
		"schema_files",
		"schema.png",
		"image/png",
		"schema-input",
	)
	if err := form.WriteField(
		"schema_pipeline",
		`{"expand":420}`,
	); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/schema/background",
		body,
	)
	request.Header.Set("X-User-ID", "owner-a")
	request.Header.Set("Content-Type", form.FormDataContentType())
	submitted := httptest.NewRecorder()
	handler.ServeHTTP(submitted, request)
	if submitted.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", submitted.Code, submitted.Body.String())
	}
}

func writeBackgroundRemovalFile(
	t *testing.T,
	form *multipart.Writer,
	filename string,
	mediaType string,
	content string,
) {
	t.Helper()
	writeBackgroundRemovalFileField(
		t,
		form,
		"files",
		filename,
		mediaType,
		content,
	)
}

func writeBackgroundRemovalFileField(
	t *testing.T,
	form *multipart.Writer,
	field string,
	filename string,
	mediaType string,
	content string,
) {
	t.Helper()
	header := make(textproto.MIMEHeader)
	header.Set(
		"Content-Disposition",
		fmt.Sprintf(
			`form-data; name="%s"; filename="%s"`,
			field,
			filename,
		),
	)
	header.Set("Content-Type", mediaType)
	part, err := form.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
}
