// Package catalogtest builds catalog variants for tests that exercise a
// capability the shipped Product Schema keeps disabled, or a Schema rule it
// declares on no Offering.
package catalogtest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// TripoFormatConversionID is the Offering the default Schema keeps disabled
// (2026-10-09: the 3D stage reads GLB only and Tripo conversion never returns
// GLB). Adapter and kernel contract tests still cover its code path.
const TripoFormatConversionID = "tripo/tripo-v3.1/format-conversion"

// DefaultCatalogBytes returns the default Product Schema document with the
// given Offerings switched to enabled.
func DefaultCatalogBytes(tb testing.TB, enable ...string) []byte {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("catalogtest: cannot locate default catalog")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "default_catalog.json"))
	if err != nil {
		tb.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		tb.Fatal(err)
	}
	var offerings []map[string]json.RawMessage
	if err := json.Unmarshal(document["offerings"], &offerings); err != nil {
		tb.Fatal(err)
	}
	wanted := map[string]bool{}
	for _, id := range enable {
		wanted[id] = false
	}
	for _, offering := range offerings {
		var id string
		if err := json.Unmarshal(offering["id"], &id); err != nil {
			tb.Fatal(err)
		}
		if _, ok := wanted[id]; ok {
			offering["enabled"] = json.RawMessage("true")
			wanted[id] = true
		}
	}
	for id, found := range wanted {
		if !found {
			tb.Fatalf("catalogtest: offering %q not in default catalog", id)
		}
	}
	if document["offerings"], err = json.Marshal(offerings); err != nil {
		tb.Fatal(err)
	}
	out, err := json.Marshal(document)
	if err != nil {
		tb.Fatal(err)
	}
	return out
}

// LoadDefaultEnabling loads the default Schema with the given Offerings enabled.
func LoadDefaultEnabling(tb testing.TB, enable ...string) *catalog.Catalog {
	tb.Helper()
	loaded, err := catalog.Load(bytes.NewReader(DefaultCatalogBytes(tb, enable...)))
	if err != nil {
		tb.Fatal(err)
	}
	return loaded
}

// LoadDefaultWithTripoConversion loads the default Schema with Tripo format
// conversion enabled, for tests of the conversion adapter and kernel path.
func LoadDefaultWithTripoConversion(tb testing.TB) *catalog.Catalog {
	tb.Helper()
	return LoadDefaultEnabling(tb, TripoFormatConversionID)
}

// SourceSideOfferingID is a test-local background removal whose
// SourceSideParameter declares default_by_source_side (#284). No shipped
// Offering declares that rule: on 2026-11-13 the matting service's shared GPU ran
// out of memory at the larger resolutions the rule chose for a 2048x2048 picture
// (#299), so the shipped matting resolutions are a static 1024. The rule is still
// generic Schema and kernel machinery, and its tests exercise it on this
// Offering instead of a shipped one. Its other parameters, resolution
// parameters included, are the shipped matting Offering's, without a rule.
const SourceSideOfferingID = "matting/source-side-tiers/background-removal"

// SourceSideParameter is the parameter of SourceSideOfferingID that follows the
// long side of its source_assets: 1024 up to a long side of 1024, 2048 above.
const SourceSideParameter = "birefnet_process_res"

const (
	shippedBackgroundRemovalID = "matting/default/background-removal"
	sourceSideModelID          = "matting-source-side-tiers"
)

// SourceSideDocument returns the default Product Schema document (generic JSON,
// for a test to mutate before Load) with SourceSideOfferingID added: a copy of
// the shipped matting Offering under its own model, not the default of its
// capability, whose SourceSideParameter declares the rule.
func SourceSideDocument(tb testing.TB) map[string]any {
	tb.Helper()
	var document map[string]any
	if err := json.Unmarshal(DefaultCatalogBytes(tb), &document); err != nil {
		tb.Fatal(err)
	}
	offerings, _ := document["offerings"].([]any)
	var template map[string]any
	for _, entry := range offerings {
		if offering, _ := entry.(map[string]any); offering["id"] == shippedBackgroundRemovalID {
			template = offering
		}
	}
	if template == nil {
		tb.Fatalf("catalogtest: offering %q not in default catalog", shippedBackgroundRemovalID)
	}
	encoded, err := json.Marshal(template)
	if err != nil {
		tb.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		tb.Fatal(err)
	}
	clone["id"] = SourceSideOfferingID
	clone["model_id"] = sourceSideModelID
	clone["revision"] = "2026-11-13.1"
	clone["default"] = false
	clone["display_order"] = 1
	declared := false
	parameters, _ := clone["parameters"].([]any)
	for _, entry := range parameters {
		if parameter, _ := entry.(map[string]any); parameter["name"] == SourceSideParameter {
			parameter["default_by_source_side"] = map[string]any{
				"source": "source_assets",
				"tiers": []any{
					map[string]any{"max_source_side": 1024, "value": 1024},
					map[string]any{"value": 2048},
				},
			}
			declared = true
		}
	}
	if !declared {
		tb.Fatalf("catalogtest: %q has no parameter %q", shippedBackgroundRemovalID, SourceSideParameter)
	}
	document["offerings"] = append(offerings, clone)
	application, _ := document["application"].(map[string]any)
	discovery, _ := application["provider_discovery"].(map[string]any)
	models, _ := discovery["legacy_models"].([]any)
	discovery["legacy_models"] = append(models, map[string]any{
		"model_id": sourceSideModelID, "provider_key": "matting", "recommended": false,
	})
	return document
}

// LoadWithSourceSideOffering loads SourceSideDocument.
func LoadWithSourceSideOffering(tb testing.TB) *catalog.Catalog {
	tb.Helper()
	encoded, err := json.Marshal(SourceSideDocument(tb))
	if err != nil {
		tb.Fatal(err)
	}
	loaded, err := catalog.Load(bytes.NewReader(encoded))
	if err != nil {
		tb.Fatal(err)
	}
	return loaded
}

// DropUnusedOfferingTexts removes the offering translations no offering of a
// generic catalog document uses any more. A test that strips copy out of an
// offering calls it, since Load rejects a translation nothing uses.
func DropUnusedOfferingTexts(document map[string]any) {
	encoded, err := json.Marshal(document["offerings"])
	if err != nil {
		return
	}
	localization, _ := document["application"].(map[string]any)["localization"].(map[string]any)
	bundles, _ := localization["bundles"].(map[string]any)
	for _, raw := range bundles {
		bundle, _ := raw.(map[string]any)
		texts, _ := bundle["offering_texts"].(map[string]any)
		for source := range texts {
			quoted, _ := json.Marshal(source)
			if !bytes.Contains(encoded, quoted) {
				delete(texts, source)
			}
		}
	}
}
