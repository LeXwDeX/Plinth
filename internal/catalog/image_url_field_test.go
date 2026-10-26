package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// 下游内网零互联网：图片响应的 url_field 必须配 asset_hosts，否则 URL 通道无处可取。
func TestImageURLFieldRequiresAssetHosts(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	var gpt map[string]any
	for _, item := range document["offerings"].([]any) {
		offering := item.(map[string]any)
		if offering["id"] == "openai/gpt-image-2/text-to-image" {
			gpt = offering
		}
	}
	if gpt == nil {
		t.Fatal("gpt-image-2 offering missing")
	}
	response := gpt["provider_options"].(map[string]any)["response"].(map[string]any)
	if _, declared := response["url_field"]; declared {
		t.Fatal("gpt-image-2 returns base64 only and must not declare url_field")
	}
	response["url_field"] = "url"
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Load(bytes.NewReader(mutated)); err == nil || !strings.Contains(err.Error(), "asset_hosts") {
		t.Fatalf("url_field without asset_hosts must be rejected, got %v", err)
	}
	gpt["asset_hosts"] = []any{"images.example.com"}
	mutated, _ = json.Marshal(document)
	if _, err := catalog.Load(bytes.NewReader(mutated)); err != nil {
		t.Fatalf("url_field with asset_hosts must load: %v", err)
	}
}
