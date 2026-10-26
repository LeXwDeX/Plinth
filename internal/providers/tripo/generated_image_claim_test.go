package tripo

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// Upstream files are all cached locally: Tripo's generated_image (the 2D image
// it drew before modeling) is kept as an extra thumbnail beside rendered_image.
func TestCatalogAliasesKeepTripoGeneratedImage(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, exists := taskCatalog.Get("tripo/tripo-v3.1-full/text-to-3d")
	if !exists {
		t.Fatal("missing Tripo text-to-3d offering")
	}
	var options providerOptions
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	got := claims(task{Output: map[string]any{
		"pbr_model":       "https://tripo-data.example/model.glb",
		"rendered_image":  "https://tripo-data.example/rendered.webp",
		"generated_image": "https://tripo-data.example/generated.webp",
	}}, "attempt-1", options.ResponseAliases)
	found := false
	for _, claim := range got {
		if claim.Role == "thumbnail" && claim.Variant == "generated_image" &&
			claim.Locator == "https://tripo-data.example/generated.webp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("generated_image not claimed: %#v", got)
	}
}
