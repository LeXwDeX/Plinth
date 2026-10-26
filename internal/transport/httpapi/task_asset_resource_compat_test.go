package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// runRiggingUntilSucceeded 用 mock 适配器跑通一个 rigging 任务并回到终态。
func runRiggingUntilSucceeded(
	t *testing.T,
	module *generation.Module,
	inputs *inputassets.Service,
) generation.TaskID {
	t.Helper()
	source, err := inputs.UploadAsset(context.Background(), "owner-a", generation.InputAssetUpload{
		Body:             bytes.NewReader(modelinputtest.Triangle(modelinputtest.Options{Textured: true})),
		OriginalFilename: "resource-contract.glb", AssetType: "model",
		MediaType: "model/gltf-binary", MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.Rigging,
		OfferingID: "meshy/processing/rigging",
		Input:      []byte(`{"source_asset":"` + string(source.ID) + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	reconcileLegacyTask(t, module, accepted.TaskID, "resource-compat-rigging")
	return accepted.TaskID
}

// TestTaskAssetResponseExposesResourceContractAdditively 覆盖 §3.4/§8.1：
// assetResponse 只加不改——旧键全部保留，新语义任务新增
// variant/required/delivery，且不泄漏私有持久化字段。
func TestTaskAssetResponseExposesResourceContractAdditively(t *testing.T) {
	t.Parallel()
	module, handler, inputs := newHTTPHarness(t)
	taskID := runRiggingUntilSucceeded(t, module, inputs)
	response := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/v1/tasks/"+string(taskID),
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"task detail status = %d body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	body := response.Body.String()
	for _, legacyKey := range []string{
		`"id"`, `"role"`, `"ordinal"`, `"media_type"`,
		`"size"`, `"sha256"`, `"url"`,
	} {
		if !strings.Contains(body, legacyKey) {
			t.Fatalf("legacy key %s missing: %s", legacyKey, body)
		}
	}
	for _, private := range []string{
		"storage_key", "owner-a", "attempt", "opaque",
	} {
		if strings.Contains(body, private) {
			t.Fatalf("private field %q leaked: %s", private, body)
		}
	}
	var task struct {
		State  string `json:"state"`
		Assets []struct {
			ID       string `json:"id"`
			Role     string `json:"role"`
			Variant  string `json:"variant"`
			Required bool   `json:"required"`
			Delivery string `json:"delivery"`
			Format   string `json:"format"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if task.State != string(generation.TaskSucceeded) || len(task.Assets) != 2 {
		t.Fatalf("task = %#v", task)
	}
	variants := map[string]bool{}
	// #327: each model file also carries its public format name, so the 加工 panel never shows the
	// Provider's result field (meshy · rigged_character_glb_url).
	formats := map[string]string{"rigged_character_glb_url": "GLB", "rigged_character_fbx_url": "FBX"}
	for _, asset := range task.Assets {
		if !asset.Required || asset.Delivery != "required" {
			t.Fatalf("asset resource contract = %#v", asset)
		}
		if asset.Format != formats[asset.Variant] {
			t.Fatalf("asset %s format = %q, want %q", asset.Variant, asset.Format, formats[asset.Variant])
		}
		variants[asset.Variant] = true
	}
	if !variants["rigged_character_fbx_url"] || !variants["rigged_character_glb_url"] {
		t.Fatalf("rigged variants = %#v", variants)
	}
}

// Meshy 7.1 tasks expose each required model format in the public task projection.
func TestTaskAssetResponseExposesMeshy71FormatVariants(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d",
		Input:      []byte(`{"prompt":"compat castle"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	reconcileLegacyTask(t, module, accepted.TaskID, "resource-compat-legacy")
	response := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/v1/tasks/"+string(accepted.TaskID),
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"status = %d body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	var task struct {
		Assets []struct {
			Required bool   `json:"required"`
			Variant  string `json:"variant"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, asset := range task.Assets {
		if !asset.Required {
			t.Fatalf("Meshy 7.1 asset must be required: %#v", asset)
		}
		got[asset.Variant] = true
	}
	for _, variant := range []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"} {
		if !got[variant] {
			t.Fatalf("Meshy 7.1 asset variant %q missing: %#v", variant, task.Assets)
		}
	}
}

// TestOfferingsExposeResourceContractMetadata 覆盖 §8.1 R2 行的正面断言：
// /api/v1/offerings 构造性公开 outputs[] 资源元数据（含 W1 新字段）。
func TestOfferingsExposeResourceContractMetadata(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(handler, http.MethodGet, "/api/v1/offerings", nil)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"status = %d body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	var body struct {
		Offerings []struct {
			ID      string `json:"id"`
			Outputs []struct {
				Role          string   `json:"role"`
				Variant       string   `json:"variant"`
				VariantPrefix string   `json:"variant_prefix"`
				Delivery      string   `json:"delivery"`
				Group         string   `json:"group"`
				Minimum       *int     `json:"minimum"`
				MediaTypes    []string `json:"media_types"`
				MaxBytes      int64    `json:"max_bytes"`
			} `json:"outputs"`
		} `json:"offerings"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{
		"rigged_character_fbx_url": false,
		"rigged_character_glb_url": false,
		"animation_glb_url":        false,
		"animation_fbx_url":        false,
		"basic_animations.":        false,
	}
	for _, offering := range body.Offerings {
		if offering.ID != "meshy/processing/rigging" &&
			offering.ID != "meshy/processing/animation" {
			continue
		}
		for _, output := range offering.Outputs {
			if (output.VariantPrefix == "" && output.Variant == "") || output.Minimum == nil {
				t.Fatalf(
					"offering %s output missing resource metadata: %#v",
					offering.ID,
					output,
				)
			}
			if output.Variant == "rigged_character_fbx_url" ||
				output.Variant == "rigged_character_glb_url" ||
				output.Variant == "animation_glb_url" {
				if output.Delivery != "required" || *output.Minimum != 1 {
					t.Fatalf(
						"offering %s primary = %#v",
						offering.ID,
						output,
					)
				}
			}
			if output.VariantPrefix == "basic_animations." {
				if output.Delivery != "required" {
					t.Fatalf(
						"offering %s background = %#v",
						offering.ID,
						output,
					)
				}
			}
			key := output.Variant
			if output.VariantPrefix != "" {
				key = output.VariantPrefix
			}
			if _, known := found[key]; known {
				found[key] = true
			}
		}
	}
	for prefix, seen := range found {
		if !seen {
			t.Fatalf("prefix %q not exposed in offerings", prefix)
		}
	}
}
