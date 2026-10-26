package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestRecoveryCatalogRejectsNetworkCostAndAmbiguousSlotContracts(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any, map[string]any)
	}{
		{"missing presentation", func(o, c map[string]any) { delete(o["parameters"].([]any)[0].(map[string]any), "presentation") }},
		{"null presentation", func(o, c map[string]any) { o["parameters"].([]any)[0].(map[string]any)["presentation"] = nil }},
		{"cost", func(o, c map[string]any) { o["estimated_credits"] = 1 }},
		{"metered", func(o, c map[string]any) { o["credits_metered"] = true }},
		{"non-idempotent", func(o, c map[string]any) { o["idempotent_submission"] = false }},
		{"network", func(o, c map[string]any) { c["base_url_setting"] = "tripo.base_url" }},
		{"remote protocol", func(o, c map[string]any) { c["protocol"] = "tripo" }},
		{"unknown receipt option", func(o, c map[string]any) { o["provider_options"].(map[string]any)["url"] = "https://example.test" }},
		{"duplicate source slot", func(o, c map[string]any) {
			slots := o["provider_options"].(map[string]any)["sources"].([]any)[0].(map[string]any)["slots"].([]any)
			slots[1] = slots[0]
		}},
		{"missing source slot", func(o, c map[string]any) {
			source := o["provider_options"].(map[string]any)["sources"].([]any)[0].(map[string]any)
			source["slots"] = source["slots"].([]any)[:1]
		}},
		{"dynamic output slot", func(o, c map[string]any) { o["outputs"].([]any)[0].(map[string]any)["variant_prefix"] = "converted." }},
		{"visible receipt input", func(o, c map[string]any) {
			o["parameters"].([]any)[0].(map[string]any)["presentation"].(map[string]any)["surface"] = "composer"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := os.ReadFile("default_catalog.json")
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err = json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			var offering, connection map[string]any
			for _, v := range doc["offerings"].([]any) {
				o := v.(map[string]any)
				if o["id"] == "local/existing-assets/recover" {
					offering = o
				}
			}
			runtime := doc["application"].(map[string]any)["runtime"].(map[string]any)
			for _, v := range runtime["provider_connections"].([]any) {
				c := v.(map[string]any)
				if c["id"] == "local-recovery" {
					connection = c
				}
			}
			if offering == nil || connection == nil {
				t.Fatal("missing recovery declaration")
			}
			tt.change(offering, connection)
			raw, _ = json.Marshal(doc)
			if _, err = catalog.Load(bytes.NewReader(raw)); err == nil {
				t.Fatal("unsafe recovery contract accepted")
			}
		})
	}
}
