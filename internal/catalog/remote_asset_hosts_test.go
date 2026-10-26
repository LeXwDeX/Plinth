package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// URL mappings must be executable before accepting paid work. Exercise the
// actual Catalog declarations, including private conversion result aliases.
func TestRemoteAssetMappingsRequireAssetHosts(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var original map[string]any
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	for index, item := range original["offerings"].([]any) {
		offering := item.(map[string]any)
		options, _ := offering["provider_options"].(map[string]any)
		assets, _ := options["result_assets"].([]any)
		aliases, _ := options["response_aliases"].([]any)
		response, _ := options["response"].(map[string]any)
		urlField, _ := response["url_field"].(string)
		if len(assets) == 0 && len(aliases) == 0 && urlField == "" {
			continue
		}
		for _, hosts := range []struct {
			name  string
			value []string
		}{
			{"missing", nil},
			{"empty", []string{}},
			{"blank", []string{" ", ".", ".."}},
		} {
			t.Run(offering["id"].(string)+"/"+hosts.name, func(t *testing.T) {
				var document map[string]any
				if err := json.Unmarshal(raw, &document); err != nil {
					t.Fatal(err)
				}
				candidate := document["offerings"].([]any)[index].(map[string]any)
				candidate["asset_hosts"] = hosts.value
				encoded, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := catalog.Load(bytes.NewReader(encoded)); err == nil || !strings.Contains(err.Error(), "asset_hosts") {
					t.Fatalf("remote URL mapping without usable asset_hosts must fail during Catalog load, got %v", err)
				}
			})
		}
	}
}

func TestRemoteAssetHostsRemainSchemaDeclared(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	// A new endpoint's domain needs no Provider-name rule or compiled host table.
	hosts := []string{" ", ".ARTIFACTS.New-Provider.Example."}
	for _, item := range document["offerings"].([]any) {
		offering := item.(map[string]any)
		if existing, _ := offering["asset_hosts"].([]any); len(existing) > 0 {
			offering["asset_hosts"] = hosts
		}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Load(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range document["offerings"].([]any) {
		declaration := item.(map[string]any)
		id := schema.OfferingID(declaration["id"].(string))
		offering, ok := loaded.Get(id)
		if !ok {
			t.Fatalf("missing offering %s", id)
		}
		if _, changed := declaration["asset_hosts"].([]string); changed && !reflect.DeepEqual(offering.AssetHosts, hosts) {
			t.Fatalf("%s: host declaration changed: %v", id, offering.AssetHosts)
		}
	}
}
