package legacygeneration

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestProjectorUsesCapabilityOutputSchemaForLegacyProviderData(
	t *testing.T,
) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	projector, err := NewProjector(ProjectorConfig{
		Capabilities: application.Capabilities,
		Aliases:      application.TaskStream.Aliases,
		ExcludedRoles: application.Administration.Generations.Assets.
			DownloadExcludedRoles,
	})
	if err != nil {
		t.Fatal(err)
	}

	assets := projector.ProjectLegacyGenerationAssets(
		"text-to-3d",
		json.RawMessage(`{
			"model_urls":{
				"obj":"https://cdn.meshy.ai/task/model.obj",
				"glb":"https://cdn.meshy.ai/task/model.glb"
			},
			"texture_urls":[{
				"normal":"https://cdn.meshy.ai/task/normal.png",
				"base_color":"https://cdn.meshy.ai/task/base.png"
			}],
			"thumbnail_url":"https://cdn.meshy.ai/task/thumb.png"
		}`),
		nil,
	)
	got := compactAssets(assets)
	want := []compactAsset{
		{
			role: "model", variant: "glb",
			source:   "https://cdn.meshy.ai/task/model.glb",
			filename: "model.glb",
		},
		{
			role: "model", variant: "obj",
			source:   "https://cdn.meshy.ai/task/model.obj",
			filename: "model.obj",
		},
		{
			role: "texture", variant: "0.base_color",
			source:   "https://cdn.meshy.ai/task/base.png",
			filename: "base.png",
		},
		{
			role: "texture", variant: "0.normal",
			source:   "https://cdn.meshy.ai/task/normal.png",
			filename: "normal.png",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("assets = %#v, want %#v", got, want)
	}
}

func TestProjectorUsesSchemaResultFieldsAndAliases(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	projector, err := NewProjector(ProjectorConfig{
		Capabilities: application.Capabilities,
		Aliases:      application.TaskStream.Aliases,
		ExcludedRoles: application.Administration.Generations.Assets.
			DownloadExcludedRoles,
	})
	if err != nil {
		t.Fatal(err)
	}

	assets := projector.ProjectLegacyGenerationAssets(
		"animations",
		json.RawMessage(`{
			"result":{
				"animation_glb_url":"https://assets.meshy.ai/walk.glb",
				"animation_fbx_url":"https://assets.meshy.ai/walk.fbx",
				"basic_animations":{
					"run":"https://assets.meshy.ai/run.glb",
					"idle":"https://assets.meshy.ai/idle.glb"
				}
			}
		}`),
		nil,
	)
	got := compactAssets(assets)
	want := []compactAsset{
		{
			role: "model", variant: "animation_glb_url",
			source:   "https://assets.meshy.ai/walk.glb",
			filename: "walk.glb",
		},
		{
			role: "model", variant: "animation_fbx_url",
			source:   "https://assets.meshy.ai/walk.fbx",
			filename: "walk.fbx",
		},
		{
			role: "model", variant: "basic_animations.idle",
			source:   "https://assets.meshy.ai/idle.glb",
			filename: "idle.glb",
		},
		{
			role: "model", variant: "basic_animations.run",
			source:   "https://assets.meshy.ai/run.glb",
			filename: "run.glb",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("assets = %#v, want %#v", got, want)
	}
}

func TestProjectorChangesWithConfiguredProjectionNamesAndFallsBack(
	t *testing.T,
) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	capability := application.Capabilities[2]
	capability.LegacyAPI.OutputProjections[1].Name = "configured_images"
	projector, err := NewProjector(ProjectorConfig{
		Capabilities:  []catalog.CapabilityPresentation{capability},
		ExcludedRoles: []string{"thumbnail", "*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	configured := projector.ProjectLegacyGenerationAssets(
		string(capability.ID),
		json.RawMessage(`{
			"configured_images":["/api/gpt-images/configured.webp"]
		}`),
		nil,
	)
	if got := compactAssets(configured); !reflect.DeepEqual(
		got,
		[]compactAsset{{
			role:     "image",
			variant:  "0",
			source:   "/api/gpt-images/configured.webp",
			filename: "configured.webp",
		}},
	) {
		t.Fatalf("configured assets = %#v", got)
	}

	fallback := projector.ProjectLegacyGenerationAssets(
		string(capability.ID),
		json.RawMessage(`{}`),
		[]string{"/api/gpt-images/fallback.webp"},
	)
	if got := compactAssets(fallback); !reflect.DeepEqual(
		got,
		[]compactAsset{{
			role:     "image",
			variant:  "0",
			source:   "/api/gpt-images/fallback.webp",
			filename: "fallback.webp",
		}},
	) {
		t.Fatalf("fallback assets = %#v", got)
	}
}

func TestProjectorNormalizesProviderRecordDownloadToLegacyCacheRoute(
	t *testing.T,
) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	projector, err := NewProjector(ProjectorConfig{
		Capabilities: application.Capabilities,
		Aliases:      application.TaskStream.Aliases,
		ExcludedRoles: application.Administration.Generations.Assets.
			DownloadExcludedRoles,
	})
	if err != nil {
		t.Fatal(err)
	}

	assets := projector.ProjectLegacyGenerationAssets(
		"background-removal",
		json.RawMessage(`{
			"tasks":[
				{
					"task_id":"task_cached",
					"status":"succeeded",
					"downloadable":true,
					"download_path":"/api/v1/tasks/task_cached/download"
				},
				{
					"task_id":"task_failed",
					"status":"failed",
					"downloadable":false,
					"download_path":"/api/v1/tasks/task_failed/download"
				}
			]
		}`),
		nil,
	)
	got := compactAssets(assets)
	want := []compactAsset{{
		role:     "image",
		variant:  "0",
		source:   "/api/meshy/background-removal/download/task_cached",
		filename: "task_cached",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("background-removal assets = %#v, want %#v", got, want)
	}
}

type compactAsset struct {
	role     string
	variant  string
	source   string
	filename string
}

func compactAssets(
	assets []administration.GenerationAsset,
) []compactAsset {
	result := make([]compactAsset, 0, len(assets))
	for _, asset := range assets {
		result = append(result, compactAsset{
			role:     asset.Role,
			variant:  asset.Variant,
			source:   asset.SourceURL,
			filename: asset.PreferredFilename,
		})
	}
	return result
}
