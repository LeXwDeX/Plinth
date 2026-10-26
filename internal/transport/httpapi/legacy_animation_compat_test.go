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

func TestLegacyAnimationWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	module, handler, inputs := newHTTPHarness(t)
	sourceAsset, err := inputs.UploadAsset(
		context.Background(),
		"owner-a",
		generation.InputAssetUpload{
			Body:             bytes.NewReader(modelinputtest.Triangle(modelinputtest.Options{Textured: true})),
			OriginalFilename: "character.glb",
			AssetType:        "model",
			MediaType:        "model/gltf-binary",
			MaxBytes:         1 << 20,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rigging, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.Rigging,
		OfferingID: "meshy/processing/rigging",
		Input: json.RawMessage(
			`{"source_asset":"` + string(sourceAsset.ID) + `"}`,
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	reconcileLegacyTask(t, module, rigging.TaskID, "legacy-animation-rigging")

	submitted := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/animations",
		strings.NewReader(`{
			"rig_task_id":"`+string(rigging.TaskID)+`",
			"action_id":5
		}`),
	)
	if submitted.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", submitted.Code, submitted.Body.String())
	}
}

func TestLegacyAnimationSchemaAliasWriteRequiresApprovedExecution(t *testing.T) {
	t.Parallel()
	module, handler, inputs := newHTTPHarnessWithApplication(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			for index := range application.Capabilities {
				capability := &application.Capabilities[index]
				if capability.ID != generation.Animation {
					continue
				}
				capability.LegacyAPI.TaskAliases[0].Name = "legacy_rig"
				capability.LegacyAPI.ResultField = "artifact_result"
				capability.LegacyAPI.ResultAssets[0].Name = "preview_asset"
			}
		},
	)
	sourceAsset, err := inputs.UploadAsset(
		context.Background(),
		"owner-a",
		generation.InputAssetUpload{
			Body:             bytes.NewReader(modelinputtest.Triangle(modelinputtest.Options{Textured: true})),
			OriginalFilename: "schema-character.glb",
			AssetType:        "model",
			MediaType:        "model/gltf-binary",
			MaxBytes:         1 << 20,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rigging, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.Rigging,
		OfferingID: "meshy/processing/rigging",
		Input: json.RawMessage(
			`{"source_asset":"` + string(sourceAsset.ID) + `"}`,
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	reconcileLegacyTask(t, module, rigging.TaskID, "schema-animation-rigging")
	response := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/animations",
		strings.NewReader(
			`{"legacy_rig":"`+string(rigging.TaskID)+`","action_id":1}`,
		),
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("legacy write status = %d, want 403; body = %s", response.Code, response.Body.String())
	}
}
