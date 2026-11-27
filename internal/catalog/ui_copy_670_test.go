package catalog

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// #670: the all-screens audit found Schema copy that showed raw identifiers,
// English leftovers in the Chinese default, and project messages with no
// English at all.

// bareOptionLabel is an option label that only repeats its lower-case wire
// value (`bottom`, `full_color`, `3mf`): the panel then shows an identifier.
// Sizes such as `1024x1024` carry a lone `x` and are readable as they are.
var bareOptionLabel = regexp.MustCompile(`[a-z]{2}`)

func TestOfferingOptionLabelsAreNotBareIdentifiers670(t *testing.T) {
	document, err := decodeGenericJSON(defaultCatalog)
	if err != nil {
		t.Fatal(err)
	}
	offerings, _ := document["offerings"].([]any)
	var bare []string
	for _, rawOffering := range offerings {
		offering, _ := rawOffering.(map[string]any)
		parameters, _ := offering["parameters"].([]any)
		for _, rawParameter := range parameters {
			parameter, _ := rawParameter.(map[string]any)
			options, _ := parameter["options"].([]any)
			for _, rawOption := range options {
				option, _ := rawOption.(map[string]any)
				label, _ := option["label"].(string)
				value, _ := option["value"].(string)
				if label == value && bareOptionLabel.MatchString(label) {
					bare = append(bare, offering["id"].(string)+" "+parameter["name"].(string)+"="+value)
				}
			}
		}
	}
	if len(bare) > 0 {
		t.Fatalf("options whose label is the bare identifier: %v", bare)
	}
}

func TestOfferingOptionLabelsHaveChineseAndEnglish670(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	document, err := decodeGenericJSON(defaultCatalog)
	if err != nil {
		t.Fatal(err)
	}
	texts := product.Application().Localization.Bundles["en"].OfferingTexts
	type option struct{ offering, parameter, value, zh, en string }
	expected := []option{
		{"meshy/processing/resize", "origin_at", "bottom", "底部", "Bottom"},
		{"meshy/processing/resize", "origin_at", "center", "中心", "Center"},
		{"meshy/processing/analyze-printability", "printer_type", "fdm", "FDM（熔融沉积）", "FDM (fused deposition)"},
		{"meshy/processing/analyze-printability", "printer_type", "sla", "SLA（光固化）", "SLA (resin)"},
		{"meshy/processing/analyze-printability", "printer_type", "full_color", "全彩", "Full color"},
		{"meshy/processing/repair-printability", "printer_type", "fdm", "FDM（熔融沉积）", "FDM (fused deposition)"},
		{"meshy/processing/repair-printability", "printer_type", "sla", "SLA（光固化）", "SLA (resin)"},
		{"meshy/processing/repair-printability", "printer_type", "full_color", "全彩", "Full color"},
		{"meshy/processing/multi-color-print", "style", "realistic", "写实", "Realistic"},
		{"meshy/processing/multi-color-print", "printer_brand", "bambu", "拓竹（Bambu Lab）", "Bambu Lab"},
		{"meshy/prime/text-to-motion", "mode", "prime", "最高质量（Prime）", "Highest quality (Prime)"},
		{"meshy/creative-lab/keychain/build", "badge_shape", "rounded-rect", "圆角矩形", "Rounded rectangle"},
		{"meshy/creative-lab/fridge-magnet/build", "relief_curve", "s-curve", "S 形曲线", "S-curve"},
		{"meshy/creative-lab/lamp/prototype", "image_subject", "landscape", "风景", "Landscape"},
		{"meshy/creative-lab/lamp/build", "light_source_preset", "bambu_mh001_60mm", "拓竹 MH001（60 mm）", "Bambu Lab MH001 (60 mm)"},
		{"meshy/creative-lab/lamp/build", "output_format", "stl", "STL", "STL"},
		{"meshy/creative-lab/keycap/build", "base_model", "cherry-mx-1x1-r1", "Cherry MX 1×1 键帽（R1 排）", "Cherry MX 1×1 keycap (row R1)"},
		{"meshy/creative-lab/fidget-pixel/prototype", "type", "person", "人物", "Person"},
		{"meshy/creative-lab/fidget-pixel/build", "shape", "hex", "六边形", "Hexagon"},
		{"meshy/creative-lab/fidget-pixel/build", "output_format", "3mf", "3MF", "3MF"},
	}
	labels := map[string]string{}
	offerings, _ := document["offerings"].([]any)
	for _, rawOffering := range offerings {
		offering, _ := rawOffering.(map[string]any)
		parameters, _ := offering["parameters"].([]any)
		for _, rawParameter := range parameters {
			parameter, _ := rawParameter.(map[string]any)
			options, _ := parameter["options"].([]any)
			for _, rawOption := range options {
				item, _ := rawOption.(map[string]any)
				value, _ := item["value"].(string)
				label, _ := item["label"].(string)
				labels[offering["id"].(string)+" "+parameter["name"].(string)+"="+value] = label
			}
		}
	}
	for _, want := range expected {
		key := want.offering + " " + want.parameter + "=" + want.value
		if labels[key] != want.zh {
			t.Errorf("%s label = %q, want %q", key, labels[key], want.zh)
			continue
		}
		if !chineseTextCharacter.MatchString(want.zh) {
			continue
		}
		if texts[want.zh] != want.en {
			t.Errorf("%s English = %q, want %q", key, texts[want.zh], want.en)
		}
	}
}

func TestChineseDefaultMessagesDropEnglishLeftovers670(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	zh := application.Content.Messages
	en := application.Localization.Bundles["en"].Messages
	for key, want := range map[string]string{
		"ui.header.menu_toggle":            "切换菜单",
		"ui.retexture.title":               "换贴图",
		"ui.retexture.start":               "开始换贴图",
		"ui.retexture.task_created":        "换贴图任务已创建",
		"ui.retexture.upload_task_created": "模型已上传，换贴图任务已创建",
		"ui.background_removal.title":      "去背景",
		"ui.canvas.models.auto":            "自动",
		"ui.studio.agent_scope.short_auto": "自动",
		"ui.model_3d.native_mode_notice":   "✦ 原生模式：直接输出最高精度网格，不限制面数",
		"ui.workspace.skills.slug":         "标识",
		"ui.workspace.skills.description":  "说明",
		"ui.chat.input.skill_locked":       "已锁定参数面板（质量自动设为高）",
	} {
		if zh[key] != want {
			t.Errorf("zh %s = %q, want %q", key, zh[key], want)
		}
		if en[key] == "" || chineseTextCharacter.MatchString(en[key]) {
			t.Errorf("en %s = %q, want English", key, en[key])
		}
	}
	refused := zh["ui.workspace.processing.source_refused"]
	if strings.Contains(refused, "Remesh") || !strings.Contains(refused, "「重拓扑」") {
		t.Errorf("zh ui.workspace.processing.source_refused = %q", refused)
	}
	if en["ui.retexture.title"] != "Retexture" || en["ui.header.menu_toggle"] != "Toggle menu" {
		t.Errorf("English copy changed: %q / %q", en["ui.retexture.title"], en["ui.header.menu_toggle"])
	}
	if got := en["ui.project.reference_limit"]; got != "At most {maximum} images" {
		t.Errorf("en ui.project.reference_limit = %q", got)
	}
}

func TestProjectMessagesAreTranslated670(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	if !slices.Contains(application.Localization.Coverage, "project.messages") {
		t.Fatal("localization.coverage does not include project.messages")
	}
	document, err := localizationDocument(application)
	if err != nil {
		t.Fatal(err)
	}
	node, err := resolveLocalizationNode(document, "project.messages")
	if err != nil {
		t.Fatal(err)
	}
	messages, _ := node.(map[string]any)
	if len(messages) < 19 {
		t.Fatalf("project.messages has %d keys", len(messages))
	}
	fields := application.Localization.Bundles["en"].Fields
	localizer, err := NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	for key, raw := range messages {
		source, _ := raw.(string)
		translated := fields["project.messages."+key]
		if translated == "" || chineseTextCharacter.MatchString(translated) {
			t.Errorf("project.messages.%s English = %q", key, translated)
		}
		if got := localizer.Text("en", source); chineseTextCharacter.MatchString(got) {
			t.Errorf("project.messages.%s localizes to %q", key, got)
		}
	}
	// A project message added later without English must fail validation.
	broken := cloneLocalization(t, application)
	delete(broken.Localization.Bundles["en"].Fields, "project.messages.unknown_project")
	err = ValidateApplicationSchema(broken)
	if err == nil || !strings.Contains(err.Error(), "project.messages.unknown_project") {
		t.Fatalf("untranslated project message accepted: %v", err)
	}
}

func TestProviderDisplayNamesAreTranslated670(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	localizer, err := NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	for _, connection := range application.Runtime.ProviderConnections {
		if !chineseTextCharacter.MatchString(connection.DisplayName) {
			continue
		}
		path := "runtime.provider_connections[id=" + connection.ID + "].display_name"
		if got := localizer.Text("en", connection.DisplayName); chineseTextCharacter.MatchString(got) {
			t.Errorf("%s localizes to %q", path, got)
		}
	}
}
