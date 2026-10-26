package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestLegacyRiggingDirectURLWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	submitted := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/rigging",
		strings.NewReader(`{
			"model_url":"https://assets.test/character.glb",
			"height_meters":1.8
		}`),
	)
	if submitted.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", submitted.Code, submitted.Body.String())
	}
}

func TestLegacyRiggingTaskSourceWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	module, handler, inputs := newHTTPHarness(t)
	sourceAsset, err := inputs.UploadAsset(
		context.Background(),
		"owner-a",
		generation.InputAssetUpload{
			Body:             bytes.NewReader(modelinputtest.Triangle(modelinputtest.Options{Textured: true})),
			OriginalFilename: "source.glb",
			AssetType:        "model",
			MediaType:        "model/gltf-binary",
			MaxBytes:         1 << 20,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	remesh, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.Remesh,
		OfferingID: "meshy/processing/remesh",
		Input: json.RawMessage(
			`{"source_asset":"` + string(sourceAsset.ID) + `"}`,
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	reconcileLegacyTask(t, module, remesh.TaskID, "legacy-rigging-source")

	submitted := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/rigging",
		strings.NewReader(
			`{"input_task_id":"`+string(remesh.TaskID)+`"}`,
		),
	)
	if submitted.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", submitted.Code, submitted.Body.String())
	}
}

func TestLegacyRiggingConfiguredFieldWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			for index := range application.Capabilities {
				capability := &application.Capabilities[index]
				if capability.ID != generation.Rigging {
					continue
				}
				capability.LegacyAPI.Fields[0].Name = "schema_model_link"
				capability.LegacyAPI.ResultField = "schema_result"
				capability.LegacyAPI.ResultAssets[0].Name = "schema_glb"
			}
		},
	)
	submitted := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/rigging",
		strings.NewReader(
			`{"schema_model_link":"https://assets.test/schema.glb"}`,
		),
	)
	if submitted.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", submitted.Code, submitted.Body.String())
	}
}
