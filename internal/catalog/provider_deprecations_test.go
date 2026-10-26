package catalog

import (
	"strings"
	"testing"
)

// #329: application.provider_deprecations lists what Meshy has deprecated or
// retired (models, wire fields, values). Catalog loading rejects any Offering
// of that provider that would send one, except the disabled history records
// named in history_offerings, which can never be enabled again.

func offeringByID(t *testing.T, document map[string]any, id string) map[string]any {
	t.Helper()
	for _, raw := range document["offerings"].([]any) {
		offering := raw.(map[string]any)
		if offering["id"] == id {
			return offering
		}
	}
	t.Fatalf("offering %s missing", id)
	return nil
}

func offeringParameters(offering map[string]any) []any {
	return offering["parameters"].([]any)
}

func providerOptions(offering map[string]any) map[string]any {
	return offering["provider_options"].(map[string]any)
}

func addExemption(document map[string]any, exemption map[string]any) {
	entry := document["application"].(map[string]any)["provider_deprecations"].([]any)[0].(map[string]any)
	existing, _ := entry["exemptions"].([]any)
	entry["exemptions"] = append(existing, exemption)
}

func TestProviderDeprecationsRejectDeprecatedMeshyWire(t *testing.T) {
	if err := mutateDefaultCatalog(t, func(map[string]any) {}); err != nil {
		t.Fatalf("default catalog rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, document map[string]any)
	}{
		{"meshy-7 history offering enabled", func(t *testing.T, document map[string]any) {
			offeringByID(t, document, "meshy/meshy-7/text-to-3d")["enabled"] = true
			discovery := document["application"].(map[string]any)["provider_discovery"].(map[string]any)
			discovery["legacy_models"] = append(discovery["legacy_models"].([]any), map[string]any{"model_id": "meshy-7", "provider_key": "meshy-3d"})
		}},
		{"meshy-7 sent on text to 3d", func(t *testing.T, document map[string]any) {
			providerOptions(offeringByID(t, document, "meshy/meshy-7.1/text-to-3d"))["model_parameter_value"] = "meshy-7"
		}},
		{"meshy-7 sent on multi-image", func(t *testing.T, document map[string]any) {
			providerOptions(offeringByID(t, document, "meshy/meshy-7.1-multiview/image-to-3d"))["model_parameter_value"] = "meshy-7"
		}},
		{"unpinned latest model", func(t *testing.T, document map[string]any) {
			providerOptions(offeringByID(t, document, "meshy/meshy-7.1-multiview/image-to-3d"))["model_parameter_value"] = "latest"
		}},
		{"retired meshy-5 on retexture", func(t *testing.T, document map[string]any) {
			providerOptions(offeringByID(t, document, "meshy/processing/retexture"))["model_parameter_value"] = "meshy-5"
		}},
		{"ultra_mode parameter", func(t *testing.T, document map[string]any) {
			offering := offeringByID(t, document, "meshy/meshy-7.1/image-to-3d")
			offering["parameters"] = append(offeringParameters(offering), map[string]any{"name": "ultra_mode", "label": "超高精度", "type": "boolean"})
		}},
		{"symmetry_mode under another wire name", func(t *testing.T, document map[string]any) {
			offering := offeringByID(t, document, "meshy/meshy-t2.5/image-to-3d")
			offering["parameters"] = append(offeringParameters(offering), map[string]any{"name": "symmetry", "wire_name": "symmetry_mode", "label": "对称", "type": "boolean"})
		}},
		{"hd_texture fixed in a request template", func(t *testing.T, document map[string]any) {
			providerOptions(offeringByID(t, document, "meshy/processing/retexture"))["request_template"] = map[string]any{"hd_texture": true}
		}},
		{"is_a_t_pose set on refine", func(t *testing.T, document map[string]any) {
			declaration := providerOptions(offeringByID(t, document, "meshy/meshy-7.1/text-to-3d"))["refine_after_preview"].(map[string]any)
			declaration["set"].(map[string]any)["is_a_t_pose"] = true
		}},
		{"lowpoly model type", func(t *testing.T, document map[string]any) {
			for _, raw := range offeringParameters(offeringByID(t, document, "meshy/meshy-t2.5/text-to-3d")) {
				parameter := raw.(map[string]any)
				if parameter["name"] == "model_type" {
					parameter["default"] = "lowpoly"
					parameter["options"] = []any{map[string]any{"value": "lowpoly", "label": "低模"}}
				}
			}
		}},
		{"remesh resize field", func(t *testing.T, document map[string]any) {
			offering := offeringByID(t, document, "meshy/processing/remesh")
			offering["parameters"] = append(offeringParameters(offering), map[string]any{"name": "resize_height", "label": "调整高度", "type": "number"})
		}},
		{"negative_prompt on retexture", func(t *testing.T, document map[string]any) {
			offering := offeringByID(t, document, "meshy/processing/retexture")
			offering["parameters"] = append(offeringParameters(offering), map[string]any{"name": "negative_prompt", "label": "负向提示词", "type": "text"})
		}},
		{"image_urls on single image to 3d", func(t *testing.T, document map[string]any) {
			for _, raw := range offeringParameters(offeringByID(t, document, "meshy/meshy-7.1/image-to-3d")) {
				if parameter := raw.(map[string]any); parameter["name"] == "reference_assets" {
					parameter["wire_name"] = "image_urls"
				}
			}
		}},
		// #330 left no exemption; an exemption added later must name a listed
		// field, a Meshy Offering, its tracking Issue and a reason.
		{"exemption for a field that is not deprecated", func(t *testing.T, document map[string]any) {
			addExemption(document, map[string]any{"offering_id": "meshy/processing/remesh", "field": "topology", "issue": "#1", "reason": "test"})
		}},
		{"exemption for another provider's offering", func(t *testing.T, document map[string]any) {
			addExemption(document, map[string]any{"offering_id": "tripo/tripo-v3.1/format-conversion", "field": "convert_format_only", "issue": "#1", "reason": "test"})
		}},
		{"exemption without issue", func(t *testing.T, document map[string]any) {
			addExemption(document, map[string]any{"offering_id": "meshy/processing/remesh", "field": "convert_format_only", "issue": "", "reason": "test"})
		}},
		{"history offering is unknown", func(t *testing.T, document map[string]any) {
			deprecations := document["application"].(map[string]any)["provider_deprecations"].([]any)
			entry := deprecations[0].(map[string]any)
			entry["history_offerings"] = append(entry["history_offerings"].([]any), "meshy/missing/text-to-3d")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, func(document map[string]any) { tc.change(t, document) })
			if err == nil || !strings.Contains(err.Error(), "provider_deprecations") {
				t.Fatalf("deprecated wire accepted or wrong error: %v", err)
			}
		})
	}
	// Endpoint-scoped entries do not reach other endpoints: Retexture's newest
	// model is meshy-7, and auto_size is deprecated only on Remesh.
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, document map[string]any)
	}{
		{"meshy-7 on retexture", func(*testing.T, map[string]any) {}},
		{"auto_size on image to 3d", func(t *testing.T, document map[string]any) {
			for _, raw := range offeringParameters(offeringByID(t, document, "meshy/meshy-7.1/image-to-3d")) {
				if raw.(map[string]any)["name"] == "auto_size" {
					return
				}
			}
			t.Fatal("meshy-7.1 image to 3d no longer declares auto_size")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := mutateDefaultCatalog(t, func(document map[string]any) { tc.change(t, document) }); err != nil {
				t.Fatalf("documented wire rejected: %v", err)
			}
		})
	}
}

func TestRetiredGenerationModelsNameAnOfferedSuccessor(t *testing.T) {
	retired := func(document map[string]any) []any {
		frontend := document["application"].(map[string]any)["runtime"].(map[string]any)["frontend"].(map[string]any)
		return frontend["retired_generation_models"].([]any)
	}
	if err := mutateDefaultCatalog(t, func(document map[string]any) {
		entries := retired(document)
		if len(entries) != 1 {
			t.Fatalf("retired models = %v", entries)
		}
		entry := entries[0].(map[string]any)
		if entry["provider_id"] != "meshy" || entry["model_id"] != "meshy-7" || entry["successor_model_id"] != "meshy-7.1" {
			t.Fatalf("retired model = %v", entry)
		}
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, document map[string]any)
	}{
		{"successor not offered", func(t *testing.T, document map[string]any) {
			retired(document)[0].(map[string]any)["successor_model_id"] = "meshy-9"
		}},
		{"retired model still offered", func(t *testing.T, document map[string]any) {
			retired(document)[0].(map[string]any)["model_id"] = "meshy-t2.5"
			retired(document)[0].(map[string]any)["successor_model_id"] = "meshy-7.1"
		}},
		{"retired model name differs", func(t *testing.T, document map[string]any) {
			retired(document)[0].(map[string]any)["model_name"] = "Meshy Seven"
		}},
		{"retired model unknown", func(t *testing.T, document map[string]any) {
			retired(document)[0].(map[string]any)["model_id"] = "meshy-3"
		}},
		{"successor misses a capability", func(t *testing.T, document map[string]any) {
			// The multi-view model has no text to 3D Offering.
			retired(document)[0].(map[string]any)["successor_model_id"] = "meshy-7.1-multiview"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, func(document map[string]any) { tc.change(t, document) })
			if err == nil || !strings.Contains(err.Error(), "retired_generation_models") {
				t.Fatalf("invalid retired model accepted or wrong error: %v", err)
			}
		})
	}
}

// #329 follow-up C: the retired-model texts the server writes name the
// retired model and its successor.
func TestRetiredModelMessagesAreDeclared(t *testing.T) {
	for _, tc := range []struct {
		name string
		path []string
	}{
		{"direct refusal", []string{"runtime", "generation_messages", "retired_model"}},
		{"agent note", []string{"chat", "presentation", "messages", "retired_model_fallback"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, func(document map[string]any) {
				node := document["application"].(map[string]any)
				for _, key := range tc.path[:len(tc.path)-1] {
					node = node[key].(map[string]any)
				}
				node[tc.path[len(tc.path)-1]] = "已停用"
			})
			if err == nil || !strings.Contains(err.Error(), tc.path[len(tc.path)-1]) {
				t.Fatalf("template without {from}/{to} accepted or wrong error: %v", err)
			}
		})
	}
}
