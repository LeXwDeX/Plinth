package generation

import "testing"

func TestAssetMatchesOutputUsesFrozenDeclarationSpecificityAndMedia(t *testing.T) {
	outputs := []AssetRequirement{
		{Role: "arbitrary-model-role", MediaTypes: []string{"application/octet-stream"}},
		{Role: "arbitrary-model-role", VariantPrefix: "export.", MediaTypes: []string{"model/*"}},
		{Role: "arbitrary-model-role", VariantPrefix: "export.preview.", MediaTypes: []string{"image/png"}},
		{Role: "arbitrary-model-role", Variant: "export.preview.glb", MediaTypes: []string{"model/gltf-binary"}},
	}
	for _, test := range []struct {
		name, role, variant, media string
		want                       bool
	}{
		{"exact", "arbitrary-model-role", "export.preview.glb", "model/gltf-binary", true},
		{"exact rejects prefix MIME", "arbitrary-model-role", "export.preview.glb", "image/png", false},
		{"longest prefix", "arbitrary-model-role", "export.preview.png", "image/png", true},
		{"longest prefix rejects broad MIME", "arbitrary-model-role", "export.preview.png", "model/gltf-binary", false},
		{"wildcard media", "arbitrary-model-role", "export.gltf", "model/gltf+json", true},
		{"legacy role variant", "arbitrary-model-role", "legacy.glb", "application/octet-stream", true},
		{"unknown role", "other", "export.preview.glb", "model/gltf-binary", false},
		{"empty media", "arbitrary-model-role", "export.gltf", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := AssetMatchesOutput(outputs, test.role, test.variant, test.media); got != test.want {
				t.Fatalf("match=%v want %v", got, test.want)
			}
		})
	}
	if AssetMatchesOutput(nil, "model", "glb", "model/gltf-binary") {
		t.Fatal("missing frozen outputs accepted")
	}
	if !AssetMatchesOutput([]AssetRequirement{{Role: "unrestricted"}}, "unrestricted", "anything", "model/gltf-binary") {
		t.Fatal("legacy unconstrained MIME rejected")
	}
}
