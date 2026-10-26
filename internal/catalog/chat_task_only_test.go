package catalog

import (
	"bytes"
	"encoding/json"
	"testing"
)

// Prepare the proposed declaration without changing the installed inventory.
func taskOnlyCatalogDocument(t *testing.T, mutate func(map[string]any, map[string]any)) []byte {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal(defaultCatalog, &d); err != nil {
		t.Fatal(err)
	}
	a := d["application"].(map[string]any)
	chat := a["chat"].(map[string]any)
	for _, v := range chat["tools"].([]any) {
		tool := v.(map[string]any)
		if tool["name"] != "create_motion_clip" {
			continue
		}
		tool["accept_reference_images"] = false
		tool["result_attachment_kind"] = "none"
		tool["override_mappings"] = map[string]any{"duration": "duration"}
		if mutate != nil {
			mutate(tool, chat)
		}
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	t.Fatal("motion declaration missing")
	return nil
}
func TestTaskOnlyToolContractAllowsExplicitNoReferencesAndNoAttachments(t *testing.T) {
	payload := taskOnlyCatalogDocument(t, nil)
	c, err := Load(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	a := c.Application()
	for _, tool := range a.Chat.Tools {
		if tool.Name == "create_motion_clip" {
			if tool.ResultAttachmentKind != "none" || len(tool.ResultAssetRoles) != 0 {
				t.Fatal("not task-only")
			}
			return
		}
	}
	t.Fatal("missing motion")
}
func TestTaskOnlyToolContractRejectsAmbiguousReferencesAndAttachments(t *testing.T) {
	cases := map[string]func(map[string]any, map[string]any){
		"none with roles":      func(tool, chat map[string]any) { tool["result_asset_roles"] = []any{"clip"} },
		"legacy without roles": func(tool, chat map[string]any) { delete(tool, "result_attachment_kind") },
		"image without roles":  func(tool, chat map[string]any) { tool["result_attachment_kind"] = "image" },
		"model without roles":  func(tool, chat map[string]any) { tool["result_attachment_kind"] = "model" },
		"default selection":    func(tool, chat map[string]any) { tool["default_to_selection"] = true },
		"reference indices mapping": func(tool, chat map[string]any) {
			tool["argument_mappings"].(map[string]any)[chat["context"].(map[string]any)["tool_reference_argument"].(string)] = "reference-indices"
		},
		"reference handles mapping": func(tool, chat map[string]any) {
			tool["argument_mappings"].(map[string]any)[chat["context"].(map[string]any)["reference_handles_argument"].(string)] = "reference-handles"
		},
		"reference alias mapping": func(tool, chat map[string]any) {
			tool["argument_mappings"].(map[string]any)["other_reference"] = "reference-indices"
		},
		"switch fallback with reference presence": func(tool, chat map[string]any) {
			tool["fallback_capability"] = "text-to-3d"
			tool["execution"].(map[string]any)["capabilities"] = []any{"text-to-motion", "text-to-3d"}
		},
		"missing image result field": func(tool, chat map[string]any) { delete(tool["result_fields"].(map[string]any), "image_urls") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(bytes.NewReader(taskOnlyCatalogDocument(t, mutate))); err == nil {
				t.Fatal("unsafe task-only contract accepted")
			}
		})
	}
}
func TestTaskOnlyReferencePolicyIsCloned(t *testing.T) {
	c, err := Load(bytes.NewReader(taskOnlyCatalogDocument(t, nil)))
	if err != nil {
		t.Fatal(err)
	}
	original := c.Application()
	var before map[string]any
	for _, tool := range original.Chat.Tools {
		if tool.Name == "create_motion_clip" {
			raw, _ := json.Marshal(tool)
			_ = json.Unmarshal(raw, &before)
		}
	}
	if before["accept_reference_images"] != false {
		t.Fatal("explicit reference policy not preserved")
	}
	for i := range original.Chat.Tools {
		tool := &original.Chat.Tools[i]
		if tool.Name == "create_motion_clip" {
			if tool.AcceptReferenceImages == nil {
				t.Fatal("missing explicit reference policy")
			}
			*tool.AcceptReferenceImages = true
		}
	}
	fresh := c.Application()
	for _, tool := range fresh.Chat.Tools {
		if tool.Name == "create_motion_clip" {
			raw, _ := json.Marshal(tool)
			var got map[string]any
			_ = json.Unmarshal(raw, &got)
			if got["accept_reference_images"] != false {
				t.Fatal("caller mutated Catalog reference policy")
			}
		}
	}
}

func TestTaskOnlyFlagKeepsHistoricalReferenceToolValidation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "omitted", true: "true"}[explicit], func(t *testing.T) {
			var d map[string]any
			if err := json.Unmarshal(taskOnlyCatalogDocument(t, nil), &d); err != nil {
				t.Fatal(err)
			}
			tool := d["application"].(map[string]any)["chat"].(map[string]any)["tools"].([]any)[0].(map[string]any)
			if explicit {
				tool["accept_reference_images"] = true
			} else {
				delete(tool, "accept_reference_images")
			}
			valid, _ := json.Marshal(d)
			if _, err := Load(bytes.NewReader(valid)); err != nil {
				t.Fatalf("historical reference contract changed: %v", err)
			}
			delete(tool, "prompt_with_reference_template")
			invalid, _ := json.Marshal(d)
			if _, err := Load(bytes.NewReader(invalid)); err == nil {
				t.Fatal("reference tool lost required reference prompt template")
			}
		})
	}
}
