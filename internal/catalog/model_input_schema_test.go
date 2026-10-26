package catalog_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestUploadedModelProfileAndRouteContractsArePublic(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(c.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	var app struct {
		Runtime struct {
			InputAssets struct {
				ModelProfile struct {
					ID                string   `json:"id"`
					Revision          string   `json:"revision"`
					Extensions        []string `json:"extensions"`
					UploadMediaType   string   `json:"upload_media_type"`
					PreviewMediaTypes []string `json:"preview_media_types"`
					Limits            struct {
						MaxBytes       int64 `json:"max_bytes"`
						MaxWork        int64 `json:"max_work"`
						MaxImagePixels int64 `json:"max_image_pixels"`
					} `json:"limits"`
				} `json:"model_profile"`
			} `json:"input_assets"`
		} `json:"runtime"`
		Chat struct {
			Harness struct {
				SourceKinds []struct {
					ID    string `json:"id"`
					Label string `json:"label"`
				} `json:"source_kinds"`
			} `json:"harness"`
		} `json:"chat"`
	}
	if err := json.Unmarshal(raw, &app); err != nil {
		t.Fatal(err)
	}
	p := app.Runtime.InputAssets.ModelProfile
	if p.ID != "static-glb-v1" || p.Revision != "1" || p.Limits.MaxBytes != 10<<20 || p.Limits.MaxWork <= 0 || p.Limits.MaxImagePixels <= 0 || len(p.Extensions) != 1 || p.Extensions[0] != ".glb" || p.UploadMediaType == "" || len(p.PreviewMediaTypes) != 2 {
		t.Fatalf("missing bounded public GLB contract: %+v", p)
	}
	if len(app.Chat.Harness.SourceKinds) != 2 || app.Chat.Harness.SourceKinds[0].ID != "task_asset" || app.Chat.Harness.SourceKinds[1].ID != "input_asset" || app.Chat.Harness.SourceKinds[1].Label == "" {
		t.Fatalf("source kinds not declared: %+v", app.Chat.Harness.SourceKinds)
	}
	for _, capability := range []generation.Capability{generation.Remesh, generation.Retexture, generation.Rigging} {
		found := false
		for _, offering := range c.List(capability) {
			if offering.ProviderID != "meshy" {
				continue
			}
			encoded, _ := json.Marshal(offering.Parameters)
			var params []struct {
				Name          string `json:"name"`
				UploadedModel *struct {
					ProfileID       string `json:"profile_id"`
					RequireTextures bool   `json:"require_textures"`
					Requirements    string `json:"requirements"`
				} `json:"uploaded_model"`
			}
			if err := json.Unmarshal(encoded, &params); err != nil {
				t.Fatal(err)
			}
			for _, param := range params {
				if param.Name != "source_asset" {
					continue
				}
				found = true
				if param.UploadedModel == nil || param.UploadedModel.ProfileID != p.ID {
					t.Fatalf("%s missing uploaded-model policy", offering.ID)
				}
				if capability == generation.Rigging && (!param.UploadedModel.RequireTextures || param.UploadedModel.Requirements == "") {
					t.Fatal("rigging must retain explicit texture/humanoid/orientation conditions")
				}
			}
		}
		if !found {
			t.Fatalf("missing expected existing source-asset route: %s", capability)
		}
	}
	for _, offering := range c.List(generation.Animation) {
		for _, parameter := range offering.Parameters {
			if parameter.Type == "asset" {
				t.Fatalf("animation unexpectedly accepts upload without real rig Task: %s", offering.ID)
			}
		}
	}
}

func TestModelInputProfileValidationAndCopies(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	p := app.Runtime.InputAssets.ModelProfile
	if p == nil {
		t.Fatal("missing profile")
	}
	public := app.Public()
	public.Runtime.InputAssets.ModelProfile.Extensions[0] = ".changed"
	public.Runtime.InputAssets.ModelProfile.MediaTypes[0] = "changed/type"
	public.Runtime.InputAssets.ModelProfile.PreviewMediaTypes[0] = "changed/preview"
	public.Chat.Harness.SourceKinds[0].Label = "changed"
	if p.Extensions[0] != ".glb" || p.MediaTypes[0] == "changed/type" || p.PreviewMediaTypes[0] == "changed/preview" || app.Chat.Harness.SourceKinds[0].Label == "changed" {
		t.Fatal("public profile aliases private catalog")
	}
	p.Extensions[0] = ".changed"
	if c.Application().Runtime.InputAssets.ModelProfile.Extensions[0] != ".glb" {
		t.Fatal("Application profile aliases catalog")
	}
	cases := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"missing ID", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.ModelProfile.ID = "" }},
		{"missing revision", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.ModelProfile.Revision = "" }},
		{"unknown asset type", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.ModelProfile.AssetType = "unknown" }},
		{"empty extension", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.ModelProfile.Extensions = nil }},
		{"invalid extension", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.ModelProfile.Extensions = []string{".GLB"} }},
		{"duplicate extension", func(a *catalog.ApplicationSchema) {
			a.Runtime.InputAssets.ModelProfile.Extensions = []string{".glb", ".glb"}
		}},
		{"unsupported upload media", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.ModelProfile.UploadMediaType = "text/html" }},
		{"unknown preview media", func(a *catalog.ApplicationSchema) {
			a.Runtime.InputAssets.ModelProfile.PreviewMediaTypes = []string{"text/html"}
		}},
		{"duplicate preview media", func(a *catalog.ApplicationSchema) {
			a.Runtime.InputAssets.ModelProfile.PreviewMediaTypes = []string{"model/gltf-binary", "model/gltf-binary"}
		}},
		{"bad preview route", func(a *catalog.ApplicationSchema) { a.Runtime.HTTP.Routes.AssetModelPreview = "/preview/{taskID}" }},
		{"unbounded bytes", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.ModelProfile.Limits.MaxBytes = 0 }},
		{"above upload limit", func(a *catalog.ApplicationSchema) {
			a.Runtime.InputAssets.ModelProfile.Limits.MaxBytes = a.Runtime.InputAssets.UploadMaxBytes + 1
		}},
		{"unbounded work", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.ModelProfile.Limits.MaxWork = 0 }},
		{"unbounded depth", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.ModelProfile.Limits.MaxDepth = 0 }},
		{"unbounded vertices", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.ModelProfile.Limits.MaxVertices = 0 }},
		{"unbounded triangles", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.ModelProfile.Limits.MaxTriangles = 0 }},
		{"unbounded elements", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.ModelProfile.Limits.MaxElements = 0 }},
		{"unbounded nodes", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.ModelProfile.Limits.MaxNodes = 0 }},
		{"unbounded JSON", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.ModelProfile.Limits.MaxJSONBytes = 0 }},
		{"JSON exceeds file", func(a *catalog.ApplicationSchema) {
			a.Runtime.InputAssets.ModelProfile.Limits.MaxJSONBytes = a.Runtime.InputAssets.ModelProfile.Limits.MaxBytes + 1
		}},
		{"image totals inconsistent", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.ModelProfile.Limits.MaxTotalImagePixels = 1 }},
		{"unknown source kind", func(a *catalog.ApplicationSchema) { a.Chat.Harness.SourceKinds[1].ID = "arbitrary_url" }},
		{"duplicate source kind", func(a *catalog.ApplicationSchema) { a.Chat.Harness.SourceKinds[1] = a.Chat.Harness.SourceKinds[0] }},
		{"missing source copy", func(a *catalog.ApplicationSchema) { a.Chat.Harness.SourceKinds[1].Description = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := c.Application()
			tc.mutate(&a)
			if err := catalog.ValidateApplicationSchema(a); err == nil {
				t.Fatal("invalid uploaded-model declaration accepted")
			}
		})
	}
}

func TestModelPreviewCanBeWithdrawnWithoutChangingUploadOrDownload(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	app.Runtime.HTTP.Routes.AssetModelPreview = ""
	app.Runtime.InputAssets.ModelProfile.PreviewMediaTypes = nil
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatal(err)
	}
	if app.Runtime.HTTP.Routes.AssetItem == "" || app.Runtime.HTTP.Routes.Assets == "" || app.Runtime.InputAssets.ModelProfile.UploadMediaType == "" {
		t.Fatal("preview withdrawal changed upload/download")
	}
}
