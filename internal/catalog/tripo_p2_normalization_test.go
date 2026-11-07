package catalog

// The P2 offerings are enabled with the officially listed native price. These
// tests call the same normalization the kernel uses so the declared low-poly
// contract and the extra-credit consent are exercised through Normalize.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func loadP2Offering(t *testing.T, id string) schema.Offering {
	t.Helper()
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := c.Get(schema.OfferingID(id))
	if !ok {
		t.Fatalf("%s missing", id)
	}
	return offering
}

func normalizeP2(t *testing.T, offering schema.Offering, changes map[string]any) (map[string]any, error) {
	t.Helper()
	input := map[string]any{"authorize_extra_credits": true}
	switch offering.Capability {
	case schema.TextTo3D:
		input["prompt"] = "a game-ready wooden crate"
	case schema.ImageTo3D:
		if strings.Contains(string(offering.ID), "multiview") {
			input["reference_assets"] = []any{"asset-one", "asset-two"}
			input["reference_views"] = []any{"front", "left"}
		} else {
			input["reference_assets"] = []any{"asset-one"}
		}
	}
	for key, value := range changes {
		input[key] = value
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := normalizeInput(offering, encoded)
	if err != nil {
		return nil, err
	}
	var values map[string]any
	if err := json.Unmarshal(frozen, &values); err != nil {
		t.Fatal(err)
	}
	return values, nil
}

func TestTripoP2NormalizeEnforcesFaceLimitAndFastTextureRules(t *testing.T) {
	offering := loadP2Offering(t, "tripo/tripo-p2/image-to-3d")

	values, err := normalizeP2(t, offering, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := values["face_limit"]; present {
		t.Fatalf("adaptive face_limit was injected: %+v", values["face_limit"])
	}
	if values["texture_quality"] != "standard" || values["texture_version"] != "v3.5-20260815" ||
		values["texture"] != true || values["pbr"] != true || values["export_uv"] != true || values["delight"] != true {
		t.Fatalf("default P2 input missing: %+v", values)
	}
	if values["authorize_extra_credits"] != true {
		t.Fatalf("the injected extra-credit consent was lost: %+v", values["authorize_extra_credits"])
	}

	if values, err = normalizeP2(t, offering, map[string]any{"face_limit": 50000}); err != nil {
		t.Fatalf("triangle face_limit 50000 refused: %v", err)
	} else if values["face_limit"] != float64(50000) {
		t.Fatalf("face_limit not normalized: %+v", values["face_limit"])
	}
	if values, err = normalizeP2(t, offering, map[string]any{"quad": true, "face_limit": 25000}); err != nil {
		t.Fatalf("quad face_limit 25000 refused: %v", err)
	} else if values["quad"] != true {
		t.Fatalf("quad not normalized: %+v", values)
	}
	// The refusal is the Schema's text naming the legal range (#139).
	if _, err = normalizeP2(t, offering, map[string]any{"quad": true, "face_limit": 30000}); err == nil || !strings.Contains(err.Error(), "48–25000") {
		t.Fatalf("quad face_limit above 25000 accepted: %v", err)
	}
	if _, err = normalizeP2(t, offering, map[string]any{"face_limit": 50001}); err == nil || !strings.Contains(err.Error(), "不能大于 50000") {
		t.Fatalf("face_limit above the static bound accepted: %v", err)
	}
	if _, err = normalizeP2(t, offering, map[string]any{"face_limit": 48.5}); err == nil || !strings.Contains(err.Error(), "必须是整数") {
		t.Fatalf("fractional face_limit accepted: %v", err)
	}
	if values, err = normalizeP2(t, offering, map[string]any{"texture_quality": "fast"}); err != nil {
		t.Fatalf("fast texture with the default v3.5 version refused: %v", err)
	} else if values["texture_version"] != "v3.5-20260815" {
		t.Fatalf("fast texture lost the v3.5 version: %+v", values["texture_version"])
	}
	if _, err = normalizeP2(t, offering, map[string]any{"texture_quality": "fast", "texture_version": "v3.0-20250812"}); err == nil || !strings.Contains(err.Error(), "仅支持纹理模型版本") {
		t.Fatalf("fast texture with v3.0 accepted: %v", err)
	}
	if values, err = normalizeP2(t, offering, map[string]any{"texture": false}); err != nil {
		t.Fatal(err)
	} else if values["pbr"] != false || values["delight"] != false {
		t.Fatalf("PBR/delight must follow texture off: %+v", values)
	}
}

func TestTripoP2MultiviewNormalizesTheSharedMultiviewWire(t *testing.T) {
	offering := loadP2Offering(t, "tripo/tripo-p2-multiview/image-to-3d")
	var options struct {
		ReferenceWire string `json:"reference_wire"`
	}
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	if options.ReferenceWire != "multiview_inputs" || offering.ReferenceSelectionPolicy != "preserve" {
		t.Fatalf("multiview wire/reference policy missing: %+v policy=%q", options, offering.ReferenceSelectionPolicy)
	}
	values, err := normalizeP2(t, offering, map[string]any{"face_limit": 20000})
	if err != nil {
		t.Fatalf("two-view P2 input refused: %v", err)
	}
	if values["face_limit"] != float64(20000) {
		t.Fatalf("multiview face_limit not normalized: %+v", values)
	}
}

func TestTripoP2TextNormalizesTheLowPolyContract(t *testing.T) {
	offering := loadP2Offering(t, "tripo/tripo-p2/text-to-3d")
	values, err := normalizeP2(t, offering, map[string]any{"quad": true, "face_limit": 25000})
	if err != nil {
		t.Fatalf("text P2 input refused: %v", err)
	}
	if values["quad"] != true || values["face_limit"] != float64(25000) {
		t.Fatalf("text P2 quad/face_limit missing: %+v", values)
	}
	if _, err := normalizeP2(t, offering, map[string]any{"quad": true, "face_limit": 25001}); err == nil {
		t.Fatal("text P2 accepted a quad face_limit above 25000")
	}
}

func TestTripoP2NormalizeRequiresExtraCreditConsent(t *testing.T) {
	offering := loadP2Offering(t, "tripo/tripo-p2/text-to-3d")
	input := map[string]any{"prompt": "a game-ready wooden crate"}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeInput(offering, encoded); err == nil || !strings.Contains(err.Error(), "额外积分") {
		t.Fatalf("normalize accepted extra credits without consent: %v", err)
	}
	input["authorize_extra_credits"] = true
	encoded, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeInput(offering, encoded); err != nil {
		t.Fatalf("normalize refused explicit consent: %v", err)
	}
	input["authorize_extra_credits"] = false
	encoded, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeInput(offering, encoded); err == nil {
		t.Fatal("normalize accepted an explicit refusal")
	}
	// The base price has no extra component, so an untextured model needs no consent.
	untextured := map[string]any{"prompt": "a game-ready wooden crate", "texture": false}
	encoded, err = json.Marshal(untextured)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeInput(offering, encoded); err != nil {
		t.Fatalf("normalize refused an untextured model without consent: %v", err)
	}
}
