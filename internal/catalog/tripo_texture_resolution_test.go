package catalog_test

import (
	"strings"
	"testing"
)

// Tripo documents texture_quality=extreme as "8K textures", but on 2026-10-19
// the dev-test stack received 4096×4096 maps in the native GLB and in the
// OBJ/USDZ exports of an extreme task (#264). Until an 8K delivery is observed,
// no Tripo option or price line may promise 8K to the person choosing it.
func TestTripoExtremeTextureDoesNotPromise8K(t *testing.T) {
	document := defaultCatalogDocument(t)
	offerings, _ := document["offerings"].([]any)
	checked := 0
	for _, entry := range offerings {
		offering, _ := entry.(map[string]any)
		id, _ := offering["id"].(string)
		if !strings.HasPrefix(id, "tripo/") {
			continue
		}
		parameters, _ := offering["parameters"].([]any)
		for _, rawParameter := range parameters {
			parameter, _ := rawParameter.(map[string]any)
			if parameter["name"] != "texture_quality" {
				continue
			}
			options, _ := parameter["options"].([]any)
			for _, rawOption := range options {
				option, _ := rawOption.(map[string]any)
				if option["value"] != "extreme" {
					continue
				}
				checked++
				if label, _ := option["label"].(string); strings.Contains(label, "8K") {
					t.Errorf("%s: texture_quality extreme label %q promises 8K", id, label)
				}
			}
		}
		pricing, _ := offering["native_pricing"].(map[string]any)
		components, _ := pricing["components"].([]any)
		for _, rawComponent := range components {
			component, _ := rawComponent.(map[string]any)
			if label, _ := component["label"].(string); strings.Contains(label, "8K") {
				t.Errorf("%s: price line %q promises 8K", id, label)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Tripo texture_quality=extreme option found; the guard checks nothing")
	}
}
