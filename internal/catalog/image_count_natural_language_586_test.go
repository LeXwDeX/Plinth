package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #586: the number of images follows what the person says. The 2D craft
// brief tells the Agent to submit one call per different picture, one scene
// per prompt, and to keep the count parameter for variants of one prompt; no
// shipped text sends the person to a panel count any more.
func TestTwoDimensionalCraftBriefTurnsTheImageCountIntoNaturalLanguage(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := product.Application().Chat
	var brief string
	for _, prompt := range chat.Agent.CraftPrompts {
		if prompt.ID == "image2d_production" {
			brief = prompt.Text
		}
	}
	if brief == "" {
		t.Fatal("the image2d_production craft brief is missing")
	}
	for _, rule := range []string{
		"张数按用户的话定",
		"几张不同内容的图分次提交，每条 prompt 只写一个场景",
		"写进几个场景或「几张图」会被拼成多格总览",
		"n 等张数参数只出同一 prompt 的变体",
		"系列一致：同一角色或成组图片先定身份锚",
		"提交多次前先一句话说明几次、每次一个场景",
		"经用户明确同意才用 crop_image_regions 裁块",
		"每块只有原图一部分分辨率",
	} {
		if !strings.Contains(brief, rule) {
			t.Errorf("the 2D craft brief does not say %q", rule)
		}
	}
	// Each of those calls is still authorized by the session approval policy,
	// which the system prompt states once (#407).
	if !strings.Contains(chat.Agent.SystemPrompt, "付费步骤由服务端按会话的「批准方式」授权") {
		t.Error("the system prompt no longer says paid steps follow the session approval policy")
	}
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{"【生成张数】", "[Image count]", "出图张数由用户在面板", "a count the person owns",
		"The number of images is the person's own setting", "chat.image_count."} {
		if bytes.Contains(source, []byte(stale)) {
			t.Errorf("the default Catalog still says %q", stale)
		}
	}
	// The concept quantity card keeps asking how many concept images to see.
	if !strings.Contains(chat.ConceptDirections.ContextTemplate, "这次想先看几张概念图？") {
		t.Error("the concept quantity card question went with the panel count")
	}
}

// #586: a custom CATALOG_FILE written before the panel count went may still
// declare the image count block, its English words and the count control's
// texts. Load drops them and the document still loads.
func TestCatalogWithRetiredImageCountStillLoads(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(source, &document); err != nil {
		t.Fatal(err)
	}
	application := document["application"].(map[string]any)
	application["chat"].(map[string]any)["image_count"] = map[string]any{
		"context_template":      "\n\n【生成张数】用户本会话把 2D 生成的张数设为 {count} 张。",
		"offering_capabilities": []string{"text-to-image", "image-to-image"},
		"limit_template":        "{model} 一次最多出 {count} 张，用它时按 {count} 张出。",
		"uncounted_template":    "{models} 没有张数参数。",
		"model_separator":       "、",
		"omitted_template":      "{model} 在当前设置下不发送张数参数（{reason}）。",
		"reason_template":       "{parameter}为「{value}」",
	}
	messages := application["content"].(map[string]any)["messages"].(map[string]any)
	messages["ui.studio.params.count_less"] = "减少数量"
	messages["ui.studio.params.default_count"] = "4"
	english := application["localization"].(map[string]any)["bundles"].(map[string]any)["en"].(map[string]any)
	english["fields"].(map[string]any)["chat.image_count.context_template"] = "\n\n[Image count] {count}"
	english["messages"].(map[string]any)["ui.studio.params.summary_count"] = "{count} images"
	legacy, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Load(bytes.NewReader(legacy)); err != nil {
		t.Fatalf("a Catalog with the retired image count keys is refused: %v", err)
	}
}
