package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

func configuredTaskAssetRoute(t *testing.T) routing.Two {
	t.Helper()
	route, err := routing.ParseTwo(
		"/configured/tasks/{taskID}/files/{assetID}",
		"taskID",
		"assetID",
	)
	if err != nil {
		t.Fatal(err)
	}
	return route
}

func TestLegacyResultProjectionUsesSchemaVariantsBeforeMIMEFallback(t *testing.T) {
	t.Parallel()
	task := generation.Task{
		ID: "task-projection",
		Assets: []generation.Asset{{
			ID:        "asset-fbx",
			Role:      "model",
			Variant:   "animation_fbx_url",
			MediaType: "application/octet-stream",
		}, {
			ID:        "asset-glb",
			Role:      "model",
			Variant:   "animation_glb_url",
			MediaType: "application/octet-stream",
		}, {
			ID:        "asset-idle",
			Role:      "model",
			Variant:   "basic_animations.idle",
			MediaType: "application/octet-stream",
		}},
	}
	schema := catalog.LegacyTaskAPISchema{
		ResultField: "configured_container",
		ResultAssets: []catalog.LegacyResultAssetSchema{{
			Name:       "configured_glb",
			Role:       "model",
			Variant:    "animation_glb_url",
			MediaTypes: []string{"application/octet-stream"},
		}, {
			Name:       "configured_fbx",
			Role:       "model",
			Variant:    "animation_fbx_url",
			MediaTypes: []string{"application/octet-stream"},
		}},
		ResultCollections: []catalog.LegacyResultCollectionSchema{{
			Name:          "configured_animations",
			Role:          "model",
			VariantPrefix: "basic_animations.",
		}},
	}
	response := map[string]any{}
	projectLegacyResultAssets(
		response,
		task,
		schema,
		configuredTaskAssetRoute(t),
	)
	result, ok := response["configured_container"].(map[string]any)
	if !ok {
		t.Fatalf("result = %#v", response)
	}
	if result["configured_glb"] !=
		"/configured/tasks/task-projection/files/asset-glb" ||
		result["configured_fbx"] !=
			"/configured/tasks/task-projection/files/asset-fbx" {
		t.Fatalf("projected = %#v", result)
	}
	animations, ok := result["configured_animations"].(map[string]string)
	if !ok ||
		animations["idle"] !=
			"/configured/tasks/task-projection/files/asset-idle" {
		t.Fatalf("collection = %#v", result["configured_animations"])
	}
}

func TestLegacyProviderRecordsPreserveFailuresAndUseMaterializedAssetDownloads(
	t *testing.T,
) {
	t.Parallel()
	task := generation.Task{
		ID: "task-provider-records",
		Assets: []generation.Asset{{
			ID:      "asset-success",
			Role:    "image",
			Ordinal: 1,
		}},
		ProviderData: json.RawMessage(`[
			{
				"identifier":"subtask-success",
				"status":"SUCCEEDED",
				"downloadable":true,
				"source_name":"hero.png",
				"successful_ordinal":1
			},
			{
				"identifier":"subtask-failed",
				"status":"FAILED",
				"downloadable":false,
				"source_name":"broken.png",
				"error_message":"bad image",
				"invalid_reason":"unsupported alpha"
			}
		]`),
	}
	response := map[string]any{}
	projectLegacyOutputAssets(
		response,
		task,
		[]catalog.LegacyOutputProjectionSchema{{
			Name:         "configured_records",
			Role:         "image",
			Mode:         "records",
			RecordSource: "provider-data",
			RecordFields: []catalog.LegacyRecordFieldSchema{
				{Name: "configured_id", Source: "provider-record-identifier"},
				{Name: "configured_status", Source: "provider-record-status"},
				{Name: "configured_downloadable", Source: "provider-record-downloadable"},
				{Name: "configured_download", Source: "legacy-download-url-by-successful-ordinal"},
				{Name: "configured_name", Source: "provider-record-source-name"},
				{Name: "configured_error", Source: "provider-record-error-message"},
				{Name: "configured_reason", Source: "provider-record-invalid-reason"},
			},
		}},
		&catalog.LegacyAssetDownloadSchema{
			Path:           "/configured/download/{configuredToken}",
			TokenParameter: "configuredToken",
		},
		configuredTaskAssetRoute(t),
	)
	records, ok := response["configured_records"].([]map[string]any)
	if !ok || len(records) != 2 {
		t.Fatalf("records = %#v", response["configured_records"])
	}
	if records[0]["configured_id"] != "asset-success" ||
		records[0]["configured_status"] != "SUCCEEDED" ||
		records[0]["configured_name"] != "hero.png" ||
		records[0]["configured_downloadable"] != true {
		t.Fatalf("successful record = %#v", records[0])
	}
	token := legacyAssetToken(task.ID, task.Assets[0].ID)
	if records[0]["configured_download"] !=
		"/configured/download/"+token {
		t.Fatalf("successful download = %#v", records[0])
	}
	if records[1]["configured_status"] != "FAILED" ||
		records[1]["configured_downloadable"] != false {
		t.Fatalf("failed record = %#v", records[1])
	}
	for _, privateField := range []string{"configured_id", "configured_error", "configured_reason"} {
		if _, exists := records[1][privateField]; exists {
			t.Fatalf("failed record exposed %s: %#v", privateField, records[1])
		}
	}
	if _, exists := records[1]["configured_download"]; exists {
		t.Fatalf("failed record has download = %#v", records[1])
	}
}

func TestLegacyTopLevelOutputProjectionUsesOnlySchemaFieldsRolesAndModes(t *testing.T) {
	t.Parallel()
	task := generation.Task{
		ID: "task-output-projection",
		Assets: []generation.Asset{
			{ID: "asset-glb", Role: "model", Variant: "glb"},
			{ID: "asset-fbx", Role: "model", Variant: "fbx"},
			{ID: "asset-thumbnail", Role: "thumbnail", Variant: "primary"},
			{ID: "asset-base", Role: "texture", Variant: "1.base_color"},
			{ID: "asset-normal", Role: "texture", Variant: "1.normal"},
			{ID: "asset-base-2", Role: "texture", Variant: "2.base_color"},
		},
	}
	response := map[string]any{}
	projectLegacyOutputAssets(
		response,
		task,
		[]catalog.LegacyOutputProjectionSchema{
			{Name: "configured_all", Role: "*", Mode: "array"},
			{Name: "configured_models", Role: "model", Mode: "map"},
			{Name: "configured_thumbnail", Role: "thumbnail", Mode: "scalar"},
			{Name: "configured_textures", Role: "texture", Mode: "array-map"},
		},
		nil,
		configuredTaskAssetRoute(t),
	)
	all, ok := response["configured_all"].([]string)
	if !ok || len(all) != 6 {
		t.Fatalf("all = %#v", response["configured_all"])
	}
	models, ok := response["configured_models"].(map[string]string)
	if !ok ||
		models["glb"] !=
			"/configured/tasks/task-output-projection/files/asset-glb" ||
		models["fbx"] !=
			"/configured/tasks/task-output-projection/files/asset-fbx" {
		t.Fatalf("models = %#v", response["configured_models"])
	}
	if response["configured_thumbnail"] !=
		"/configured/tasks/task-output-projection/files/asset-thumbnail" {
		t.Fatalf("thumbnail = %#v", response["configured_thumbnail"])
	}
	textures, ok := response["configured_textures"].([]map[string]string)
	if !ok ||
		len(textures) != 2 ||
		textures[0]["base_color"] !=
			"/configured/tasks/task-output-projection/files/asset-base" ||
		textures[0]["normal"] !=
			"/configured/tasks/task-output-projection/files/asset-normal" ||
		textures[1]["base_color"] !=
			"/configured/tasks/task-output-projection/files/asset-base-2" {
		t.Fatalf("textures = %#v", response["configured_textures"])
	}
}
