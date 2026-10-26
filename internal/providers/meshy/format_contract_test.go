package meshy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestRequiredModelFormatsRejectsSubsetAndDuplicateWithoutBreakingLegacy(t *testing.T) {
	one := 1
	offering := generation.Offering{Parameters: []generation.Parameter{{Name: "target_formats", Type: "string-array"}}, Outputs: []generation.AssetRequirement{
		{Role: "model", Variant: "glb", Minimum: &one},
		{Role: "model", Variant: "fbx", Minimum: &one},
	}}
	cases := []struct {
		name      string
		input     map[string]any
		wantError bool
	}{
		{"all", map[string]any{"target_formats": []any{"glb", "fbx"}}, false},
		{"subset", map[string]any{"target_formats": []any{"glb"}}, true},
		{"duplicate", map[string]any{"target_formats": []any{"glb", "glb"}}, true},
		{"unlisted", map[string]any{"target_formats": []any{"glb", "obj"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateRequiredModelFormats(offering, tc.input); (got != nil) != tc.wantError {
				t.Fatalf("error=%v, want error=%v", got, tc.wantError)
			}
		})
	}
	old := generation.Offering{Outputs: []generation.AssetRequirement{{Role: "model", Minimum: &one}}}
	if err := validateRequiredModelFormats(old, map[string]any{"target_formats": []any{"glb"}}); err != nil {
		t.Fatalf("historical generic output rejected: %v", err)
	}
}

func TestTextureProjectionKeepsCompanionFilesInRequiredScope(t *testing.T) {
	projection := resultAssetProjection{Path: "texture_urls", Role: "texture", Mode: "array-map", Variant: "texture."}
	payload := map[string]json.RawMessage{
		"texture_urls": json.RawMessage(`[{"base_color":"https://assets.meshy.ai/texture.png"}]`),
	}
	claims, err := projectResultAssets("attempt", payload, projection)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].Role != "texture" || claims[0].Variant != "texture.1.base_color" {
		t.Fatalf("claims = %#v", claims)
	}
}

func TestResultKeyCannotExposeUpstreamLocatorAsPublicVariant(t *testing.T) {
	claims, err := flattenResultClaims("attempt", "model", "", map[string]json.RawMessage{
		"https://private.example/task-secret": json.RawMessage(`"https://assets.meshy.ai/model.glb"`),
	})
	if err == nil || len(claims) != 0 {
		t.Fatalf("claims=%#v error=%v", claims, err)
	}
	var providerError *generation.ProviderError
	if !errors.As(err, &providerError) || providerError.Failure == nil ||
		strings.Contains(providerError.Failure.SafeDetail, "private.example") {
		t.Fatalf("public failure = %#v", err)
	}
}

func TestOptionalFormatsMayBeRequestedButRequiredOnesCannotBeDropped(t *testing.T) {
	one, zero := 1, 0
	offering := generation.Offering{
		Parameters: []generation.Parameter{{Name: "target_formats", Type: "string-array"}},
		Outputs: []generation.AssetRequirement{
			{Role: "model", Variant: "glb", Minimum: &one},
			{Role: "model", Variant: "fbx", Minimum: &one},
			{Role: "model", Variant: "usdz", Delivery: "background", Minimum: &zero},
		},
	}
	for name, tc := range map[string]struct {
		formats []any
		wantErr bool
	}{
		"all with optional":  {[]any{"glb", "fbx", "usdz"}, false},
		"required only":      {[]any{"glb", "fbx"}, false},
		"missing required":   {[]any{"glb", "usdz"}, true},
		"undeclared":         {[]any{"glb", "fbx", "stl"}, true},
		"duplicate optional": {[]any{"glb", "fbx", "usdz", "usdz"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateRequiredModelFormats(offering, map[string]any{"target_formats": tc.formats})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v, want error=%v", err, tc.wantErr)
			}
		})
	}
}
