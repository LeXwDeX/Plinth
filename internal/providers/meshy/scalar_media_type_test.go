package meshy

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func scalarMediaOffering(t *testing.T, role, variant, media string, edit func(map[string]any)) generation.Offering {
	t.Helper()
	projection := map[string]any{"path": "result_url", "role": role, "variant": variant, "mode": "scalar", "media_type": media}
	if edit != nil {
		edit(projection)
	}
	options, err := json.Marshal(map[string]any{"endpoint": "text-to-motion", "api_version": "v1", "result_assets": []any{projection}})
	if err != nil {
		t.Fatal(err)
	}
	one := 1
	return generation.Offering{ProviderOptions: options, Outputs: []generation.AssetRequirement{{Role: role, Variant: variant, Minimum: &one, MediaTypes: []string{media}, MaxBytes: 1 << 20}}}
}

func TestScalarResultMediaTypeComesFromFrozenDeclaration(t *testing.T) {
	for _, tc := range []struct{ role, variant, media string }{{"clip", "fbx", "model/fbx"}, {"print-archive", "3mf", "model/3mf"}} {
		t.Run(tc.variant, func(t *testing.T) {
			offering := scalarMediaOffering(t, tc.role, tc.variant, tc.media, nil)
			if err := ValidateOffering(offering); err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"." + tc.variant, "?signature=hidden"} {
				encoded, _ := json.Marshal("https://assets.example/result" + suffix)
				claims, err := (taskResponse{Raw: map[string]json.RawMessage{"result_url": encoded}}).claims("attempt", offering)
				if err != nil || len(claims) != 1 || claims[0].MediaType != tc.media || claims[0].Role != tc.role || claims[0].Variant != tc.variant {
					t.Fatalf("declared output lost: claims=%+v err=%v", claims, err)
				}
			}
		})
	}
}

func TestScalarResultMediaTypeRejectsInvalidOrUnboundDeclarations(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"wildcard":       func(p map[string]any) { p["media_type"] = "model/*" },
		"parameters":     func(p map[string]any) { p["media_type"] = "model/fbx; charset=utf-8" },
		"whitespace":     func(p map[string]any) { p["media_type"] = " model/fbx " },
		"not MIME":       func(p map[string]any) { p["media_type"] = "fbx" },
		"other type":     func(p map[string]any) { p["media_type"] = "text/html" },
		"wrong role":     func(p map[string]any) { p["role"] = "model" },
		"wrong variant":  func(p map[string]any) { p["variant"] = "other" },
		"map projection": func(p map[string]any) { p["mode"] = "map" },
	} {
		t.Run(name, func(t *testing.T) {
			offering := scalarMediaOffering(t, "clip", "fbx", "model/fbx", edit)
			if err := ValidateOffering(offering); err == nil {
				t.Fatal("invalid declared media type accepted")
			}
			if _, err := (taskResponse{Raw: map[string]json.RawMessage{"result_url": json.RawMessage(`"https://assets.example/result.fbx"`)}}).claims("attempt", offering); err == nil {
				t.Fatal("restored invalid declaration accepted")
			}
		})
	}
	offering := scalarMediaOffering(t, "clip", "fbx", "model/fbx", nil)
	offering.Outputs = append(offering.Outputs, offering.Outputs[0])
	if err := ValidateOffering(offering); err == nil {
		t.Fatal("ambiguous output declaration accepted")
	}
}

func TestScalarResultWithoutMediaDeclarationRetainsLegacyInference(t *testing.T) {
	offering := scalarMediaOffering(t, "model", "fbx", "application/octet-stream", func(p map[string]any) { delete(p, "media_type") })
	claims, err := (taskResponse{Raw: map[string]json.RawMessage{"result_url": json.RawMessage(`"https://assets.example/result.fbx"`)}}).claims("attempt", offering)
	if err != nil || len(claims) != 1 || claims[0].MediaType != "application/octet-stream" {
		t.Fatalf("legacy result changed: %+v %v", claims, err)
	}
}
