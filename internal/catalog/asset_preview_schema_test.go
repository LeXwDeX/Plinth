package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestAssetPreviewIsDeclaredAndOnlyItsRequestIsPublic(t *testing.T) {
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	preview := source.Application().Runtime.AssetDelivery.AssetPreview
	if preview.Query != "size" || preview.RequestValue != "preview" || preview.CacheSubdirectory != "asset-previews" ||
		preview.MaximumDimension != 512 || preview.OutputMediaType != "image/png" || len(preview.SourceMediaTypes) == 0 {
		t.Fatalf("task asset preview = %#v", preview)
	}
	encoded, err := json.Marshal(source.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	var public struct {
		Runtime struct {
			AssetDelivery struct {
				AssetPreview map[string]any `json:"asset_preview"`
			} `json:"asset_delivery"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(encoded, &public); err != nil {
		t.Fatal(err)
	}
	got := public.Runtime.AssetDelivery.AssetPreview
	if len(got) != 2 || got["query"] != "size" || got["request_value"] != "preview" {
		t.Fatalf("public asset_preview = %v, want only the query and its value", got)
	}
}

func TestAssetPreviewValidationRejectsUnsafeOrIncompleteDeclarations(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]struct {
		field string
		value any
		want  string
	}{
		"empty query":        {"query", "", "asset_preview.query"},
		"query with symbols": {"request_value", "a&b", "asset_preview.request_value"},
		"nested cache":       {"cache_subdirectory", "../previews", "cache_subdirectory"},
		"jpeg output":        {"output_media_type", "image/jpeg", "image/png"},
		"zero dimension":     {"maximum_dimension", 0, "大于 0"},
		"zero concurrency":   {"decode_concurrency", 0, "大于 0"},
		"undecodable source": {"source_media_types", []string{"image/tiff"}, "不能解码"},
		"duplicate source":   {"source_media_types", []string{"image/png", "image/png"}, "重复"},
	} {
		t.Run(name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Fatal(err)
			}
			delivery := document["application"].(map[string]any)["runtime"].(map[string]any)["asset_delivery"].(map[string]any)
			delivery["asset_preview"].(map[string]any)[change.field] = change.value
			changed, _ := json.Marshal(document)
			if _, err := catalog.Load(bytes.NewReader(changed)); err == nil || !strings.Contains(err.Error(), change.want) {
				t.Fatalf("error = %v, want it to mention %q", err, change.want)
			}
		})
	}
}
