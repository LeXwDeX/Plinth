package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestCatalogLoadRejectsInvalidUploadedModelDeclarations(t *testing.T) {
	cases := []struct {
		name      string
		offering  string
		parameter string
		mutate    func(map[string]any, map[string]any, map[string]any)
	}{
		{
			name:     "unknown profile reference",
			offering: "meshy/processing/remesh", parameter: "source_asset",
			mutate: func(_, _, uploaded map[string]any) { uploaded["profile_id"] = "unknown-profile" },
		},
		{
			name:     "missing profile reference",
			offering: "meshy/processing/remesh", parameter: "source_asset",
			mutate: func(_, _, uploaded map[string]any) { delete(uploaded, "profile_id") },
		},
		{
			name:     "missing declared profile",
			offering: "meshy/processing/remesh", parameter: "source_asset",
			mutate: func(document, _, _ map[string]any) {
				application := document["application"].(map[string]any)
				runtime := application["runtime"].(map[string]any)
				inputAssets := runtime["input_assets"].(map[string]any)
				delete(inputAssets, "model_profile")
			},
		},
		{
			name:     "source role mismatch",
			offering: "meshy/processing/remesh", parameter: "source_asset",
			mutate: func(_, parameter, _ map[string]any) { parameter["semantic"] = "source-task" },
		},
		{
			name:     "source type mismatch",
			offering: "meshy/processing/remesh", parameter: "source_asset",
			mutate: func(_, parameter, _ map[string]any) { parameter["type"] = "text" },
		},
		{
			name:     "texture requirement message missing",
			offering: "meshy/processing/rigging", parameter: "source_asset",
			mutate: func(_, _, uploaded map[string]any) { delete(uploaded, "requirements") },
		},
		{
			name:     "texture requirement message blank",
			offering: "meshy/processing/rigging", parameter: "source_asset",
			mutate: func(_, _, uploaded map[string]any) { uploaded["requirements"] = "  " },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			document := readDefaultCatalogDocument(t)
			parameter := findCatalogParameter(t, document, tc.offering, tc.parameter)
			var uploaded map[string]any
			if raw, ok := parameter["uploaded_model"].(map[string]any); ok {
				uploaded = raw
			} else {
				t.Fatalf("%s.%s uploaded_model = %#v", tc.offering, tc.parameter, parameter["uploaded_model"])
			}
			tc.mutate(document, parameter, uploaded)
			encoded, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.Load(bytes.NewReader(encoded)); err == nil {
				t.Fatal("Catalog.Load accepted invalid uploaded-model declaration")
			}
		})
	}
}

func readDefaultCatalogDocument(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("default_catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func findCatalogParameter(
	t *testing.T,
	document map[string]any,
	offeringID string,
	parameterName string,
) map[string]any {
	t.Helper()
	for _, rawOffering := range document["offerings"].([]any) {
		offering, ok := rawOffering.(map[string]any)
		if !ok || offering["id"] != offeringID {
			continue
		}
		for _, rawParameter := range offering["parameters"].([]any) {
			parameter, _ := rawParameter.(map[string]any)
			if parameter["name"] == parameterName {
				return parameter
			}
		}
		t.Fatalf("parameter %q missing from %s", parameterName, offeringID)
	}
	t.Fatalf("Offering %q missing", offeringID)
	return nil
}
