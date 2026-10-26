// Package catalogtest builds catalog variants for tests that exercise a
// capability the shipped Product Schema keeps disabled.
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
