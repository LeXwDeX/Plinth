package catalog_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// TestDefaultCatalogPointsTripoAtV3API 防止未来把 tripo_base_url 默认值
// 回改到 v2 端点（Tripo v2 已弃用，v3 为唯一支持版本）。
// 见官方 v2→v3 迁移指南：base URL https://api.tripo3d.ai/v2/openapi
// → https://openapi.tripo3d.ai/v3。
func TestDefaultCatalogPointsTripoAtV3API(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()

	setting, exists := application.Runtime.Configuration.Settings["tripo_base_url"]
	if !exists {
		t.Fatal("catalog 缺少 tripo_base_url 运行配置")
	}
	if got := setting.Default; got != "https://openapi.tripo3d.ai/v3" {
		t.Fatalf(
			"tripo_base_url 默认值 = %q, want %q（Tripo v3 端点）",
			got,
			"https://openapi.tripo3d.ai/v3",
		)
	}

	wantModelByOffering := map[generation.OfferingID]string{
		"tripo/tripo-v3.1/text-to-3d":  "v3.1-20260211",
		"tripo/tripo-v3.1/image-to-3d": "v3.1-20260211",
	}
	for offeringID, expectedModel := range wantModelByOffering {
		offering, exists := taskCatalog.Get(offeringID)
		if !exists {
			t.Fatalf("catalog 缺少 tripo offering %s", offeringID)
		}
		if offering.AdapterKey != "tripo.v3" {
			t.Errorf(
				"offering %s adapter_key = %q, want tripo.v3",
				offeringID,
				offering.AdapterKey,
			)
		}
		var options struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
			t.Fatalf("offering %s provider_options 解析失败: %v", offeringID, err)
		}
		if options.Model != expectedModel {
			t.Errorf(
				"offering %s provider_options.model = %q, want %q",
				offeringID,
				options.Model,
				expectedModel,
			)
		}
	}
}
