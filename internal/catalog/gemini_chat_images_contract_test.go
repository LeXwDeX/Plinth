package catalog_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// TestGeminiOfferingsDeclareChatImageContract 校验目录里**全部** Google 图片
// Offering 的 chat/completions 图像契约与尺寸字段映射。模型清单从目录派生：
// 新增或移除 Google 图片身份时契约自动跟随，不在测试里另抄一份模型名
// （上游网关实测只支持 gemini-3-pro-image，其余候选名返回
// "model not supported by any target"）。
func TestGeminiOfferingsDeclareChatImageContract(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, capability := range []generation.Capability{
		generation.TextToImage,
		generation.ImageToImage,
	} {
		for _, offering := range c.List(capability) {
			if offering.ProviderID != "gemini" {
				continue
			}
			checked++
			var opts map[string]any
			_ = json.Unmarshal(offering.ProviderOptions, &opts)
			if opts["endpoint"] != "chat/completions" ||
				opts["auth_header"] != "Authorization" ||
				opts["auth_prefix"] != "Bearer " {
				t.Fatalf("wire contract: %s", offering.ProviderOptions)
			}
			fields := map[string]string{}
			for _, parameter := range offering.Parameters {
				fields[parameter.Name] = parameter.WireName
			}
			if fields["aspect_ratio"] != "eca_image_config.aspect_ratio" ||
				fields["image_size"] != "eca_image_config.image_size" {
				t.Fatalf("dimensions missing: %#v", fields)
			}
			if offering.ModelID == "" || offering.Capability != capability {
				t.Fatalf("模型身份或能力错位: %#v", offering)
			}
		}
	}
	if checked == 0 {
		t.Fatal("目录中没有 Google 图片 Offering，契约测试失去意义")
	}
}
