package hyper3d_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
)

// Gen-2.5-Extreme-High returns an extra untextured Draco mesh next to the
// textured PBR pack. The Schema-declared file list keeps the required model
// slot on the PBR GLB instead of whichever GLB the list happens to name first.
func declaredFilesOffering() generation.Offering {
	step := offering(generation.TextTo3D)
	step.ProviderOptions = json.RawMessage(`{"endpoint":"rodin","observe_endpoint":"status","download_endpoint":"download",` +
		`"request_template":{"tier":"Gen-2.5-Extreme-High","geometry_file_format":"glb"},` +
		`"download_files":[{"name_suffix":"_pbr.glb"},{"name":"preview.webp"}]}`)
	minimum := 0
	step.Outputs = append(step.Outputs, generation.AssetRequirement{Role: "thumbnail", Variant: "primary", Minimum: &minimum, Delivery: generation.AssetDeliveryBackground})
	step.AssetHosts = []string{"127.0.0.1"}
	return step
}

func observeWithList(t *testing.T, step generation.Offering, list []map[string]string) generation.Observation {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/status":
			_, _ = io.WriteString(w, `{"jobs":[{"status":"Done"},{"status":"Done"}]}`)
		case "/api/v2/download":
			items := make([]map[string]string, 0, len(list))
			for _, item := range list {
				items = append(items, map[string]string{"name": item["name"], "url": server.URL + item["path"]})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"list": items})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	port := adapter(t, server.URL+"/api/v2", server.Client(), nil)
	result, err := port.Observe(context.Background(), generation.ObserveRequest{
		TaskID: "task", AttemptID: "attempt", Offering: step,
		Job:    generation.UpstreamJob{ProviderID: "hyper3d", RemoteID: "task-uuid"},
		Opaque: json.RawMessage(`{"task_uuid":"task-uuid","subscription_key":"key"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRodinDeclaredDownloadFilesKeepRequiredModelOnPBRPack(t *testing.T) {
	step := declaredFilesOffering()
	if err := hyper3d.ValidateOffering(step); err != nil {
		t.Fatal(err)
	}
	result := observeWithList(t, step, []map[string]string{
		{"name": "model_export_raw.glb", "path": "/u/model_refine/model_export_raw.glb"},
		{"name": "base_high_pbr.glb", "path": "/u/pack/glb/base_high_pbr.glb"},
		{"name": "preview.webp", "path": "/u/model_generate/v1/preview.webp"},
		{"name": "extra_pbr.glb", "path": "/u/pack/glb/extra_pbr.glb"},
	})
	if result.Phase != generation.UpstreamCompleted || len(result.Artifacts) != 2 {
		t.Fatalf("result = %#v", result)
	}
	model, thumbnail := result.Artifacts[0], result.Artifacts[1]
	if model.Role != "model" || model.Variant != "glb" || model.MediaType != "model/gltf-binary" ||
		!strings.HasSuffix(model.Locator, "/pack/glb/base_high_pbr.glb") {
		t.Fatalf("model claim = %#v", model)
	}
	if thumbnail.Role != "thumbnail" || thumbnail.Variant != "primary" || thumbnail.MediaType != "image/webp" {
		t.Fatalf("thumbnail claim = %#v", thumbnail)
	}
}

func TestRodinDeclaredDownloadFilesWaitForTexturedModel(t *testing.T) {
	result := observeWithList(t, declaredFilesOffering(), []map[string]string{
		{"name": "model_export_raw.glb", "path": "/u/model_refine/model_export_raw.glb"},
		{"name": "preview.webp", "path": "/u/model_generate/v1/preview.webp"},
	})
	if result.Phase != generation.UpstreamRunning || len(result.Artifacts) != 0 {
		t.Fatalf("an undeclared GLB filled the required model slot: %#v", result)
	}
}

func TestRodinDownloadFilesDeclarationIsValidated(t *testing.T) {
	for name, files := range map[string]string{
		"empty rule":       `[{"name_suffix":"_pbr.glb"},{}]`,
		"both selectors":   `[{"name":"a_pbr.glb","name_suffix":"_pbr.glb"}]`,
		"path separator":   `[{"name_suffix":"/pbr.glb"}]`,
		"unknown kind":     `[{"name_suffix":"_pbr.glb"},{"name":"notes.txt"}]`,
		"no model":         `[{"name":"preview.webp"}]`,
		"undeclared model": `[{"name_suffix":"_pbr.obj"}]`,
		"unknown field":    `[{"name_suffix":"_pbr.glb","role":"model"}]`,
		"not a list":       `{"name_suffix":"_pbr.glb"}`,
	} {
		t.Run(name, func(t *testing.T) {
			step := declaredFilesOffering()
			step.ProviderOptions = json.RawMessage(`{"endpoint":"rodin","observe_endpoint":"status","download_endpoint":"download",` +
				`"request_template":{"tier":"Gen-2.5-Extreme-High","geometry_file_format":"glb"},"download_files":` + files + `}`)
			if err := hyper3d.ValidateOffering(step); err == nil {
				t.Fatal("invalid download_files accepted")
			}
		})
	}
}

// A renamed pack file must not keep a paid Task polling until the job timeout:
// after a bounded number of complete-but-unmatched lists the Attempt fails.
func TestRodinDeclaredDownloadFilesFailWhenModelNeverAppears(t *testing.T) {
	step := declaredFilesOffering()
	opaque := json.RawMessage(`{"task_uuid":"task-uuid","subscription_key":"key"}`)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/status":
			_, _ = io.WriteString(w, `{"jobs":[{"status":"Done"}]}`)
		case "/api/v2/download":
			_ = json.NewEncoder(w).Encode(map[string]any{"list": []map[string]string{
				{"name": "renamed_pack.glb", "url": server.URL + "/u/renamed_pack.glb"},
			}})
		}
	}))
	t.Cleanup(server.Close)
	port := adapter(t, server.URL+"/api/v2", server.Client(), nil)
	for poll := 1; poll <= 20; poll++ {
		result, err := port.Observe(context.Background(), generation.ObserveRequest{
			TaskID: "task", AttemptID: "attempt", Offering: step,
			Job: generation.UpstreamJob{ProviderID: "hyper3d", RemoteID: "task-uuid"}, Opaque: opaque,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Phase == generation.UpstreamFailed {
			if poll < 3 || result.Failure == nil || result.Failure.Class != generation.FailureProtocol || result.Failure.Retryable {
				t.Fatalf("poll %d failure = %#v", poll, result.Failure)
			}
			return
		}
		if result.Phase != generation.UpstreamRunning {
			t.Fatalf("poll %d phase = %s", poll, result.Phase)
		}
		opaque = result.Opaque
	}
	t.Fatal("unmatched download list polled without bound")
}

func TestRodinCatalogOfferingSelectsRealDownloadNames(t *testing.T) {
	step := nativeCandidate(t, "hyper3d/rodin-gen-2.5/text-to-3d")
	step.AssetHosts = []string{"127.0.0.1"}
	result := observeWithList(t, step, []map[string]string{
		{"name": "model_export_raw.glb", "path": "/u/model_refine/model_export_raw.glb"},
		{"name": "base_high_pbr.glb", "path": "/u/pack/glb/base_high_pbr.glb"},
		{"name": "preview.webp", "path": "/u/model_generate/v1/preview.webp"},
	})
	if result.Phase != generation.UpstreamCompleted || len(result.Artifacts) != 2 ||
		!strings.HasSuffix(result.Artifacts[0].Locator, "/base_high_pbr.glb") || result.Artifacts[1].Role != "thumbnail" {
		t.Fatalf("real Rodin file list selection = %#v", result)
	}
}

func TestRodinDownloadFilesRequireASuccessBlockingModelSlot(t *testing.T) {
	step := declaredFilesOffering()
	step.Outputs[0].Delivery = generation.AssetDeliveryBackground
	if err := hyper3d.ValidateOffering(step); err == nil {
		t.Fatal("a background GLB slot satisfied the required-model selection")
	}
}

// TestProcessingCatalogOfferingsSelectOnlyQualifiedPBRPack replays the file
// names seen on 2026-10-21 (Bang: base_high_pbr.glb; Texture:
// base_basic_pbr.glb) next to decoys a different material setting could add.
func TestProcessingCatalogOfferingsSelectOnlyQualifiedPBRPack(t *testing.T) {
	for id, name := range map[generation.OfferingID]string{
		"hyper3d/bang/rodin-task":     "base_high_pbr.glb",
		"hyper3d/bang/local-model":    "base_high_pbr.glb",
		"hyper3d/texture/local-model": "base_basic_pbr.glb",
	} {
		t.Run(string(id), func(t *testing.T) {
			step := nativeCandidate(t, id)
			step.AssetHosts = []string{"127.0.0.1"}
			result := observeWithList(t, step, []map[string]string{
				{"name": "base_high_shaded.glb", "path": "/u/pack/glb/base_high_shaded.glb"},
				{"name": name, "path": "/u/pack/glb/" + name},
				{"name": "preview.webp", "path": "/u/preview.webp"},
			})
			if result.Phase != generation.UpstreamCompleted || len(result.Artifacts) != 1 ||
				result.Artifacts[0].Role != "model" || result.Artifacts[0].Variant != "glb" ||
				!strings.HasSuffix(result.Artifacts[0].Locator, "/"+name) {
				t.Fatalf("selection = %#v", result)
			}
		})
	}
}
