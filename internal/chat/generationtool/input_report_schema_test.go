package generationtool

import (
	"regexp"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

var hanLetters = regexp.MustCompile(`\p{Han}`)

// The sentences of the missing-input report are Schema copy in both languages
// (A3, #121): Chinese by default, the previous English wording for an English
// turn, and the sent-fields summary the same way.
func TestInputReportSentencesAreSchemaCopyInBothLanguages(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	executor := Executor{chat: application.Chat, texts: texts}
	offering, ok := product.Get("hyper3d/texture/local-model")
	if !ok {
		t.Fatal("Hyper3D texture offering missing")
	}
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Name == "retexture_model" {
			tool = candidate
		}
	}
	input := map[string]any{"source_asset": "asset-1", "prompt": "mossy secret words", "steps": []any{1.0}, "strict": true, "scale": 2.0, "extra": map[string]any{}}
	for locale, want := range map[string]struct{ gap, shape []string }{
		"zh": {[]string{"缺少必填输入「reference_image」", "本对话里的图片", "reference_handles"}, []string{"已发送字段：", "prompt=文本（18 个字符）", "steps=列表（1 项）", "strict=布尔值", "scale=数字", "extra=值"}},
		"en": {[]string{`missing required input "reference_image"`, "image from this chat", "reference_handles"}, []string{"fields sent: ", "prompt=text(18 chars)", "steps=list(1)", "strict=boolean", "scale=number", "extra=value"}},
	} {
		command := chat.ToolCommand{Tool: tool, Options: chat.TurnOptions{Locale: locale}}
		gaps := executor.inputGaps(command, offering, input)
		if len(gaps) != 1 {
			t.Fatalf("%s gaps = %q", locale, gaps)
		}
		shape := executor.inputShape(command, input)
		for _, fragment := range want.gap {
			if !strings.Contains(gaps[0], fragment) {
				t.Errorf("%s gap %q lacks %q", locale, gaps[0], fragment)
			}
		}
		for _, fragment := range want.shape {
			if !strings.Contains(shape, fragment) {
				t.Errorf("%s shape %q lacks %q", locale, shape, fragment)
			}
		}
		if strings.Contains(gaps[0], "secret") || strings.Contains(shape, "secret") {
			t.Errorf("%s report echoed a value: %q %q", locale, gaps[0], shape)
		}
		if locale == "en" && (hanLetters.MatchString(shape) || strings.Contains(gaps[0], "{")) {
			t.Errorf("english report not fully translated: %q %q", gaps[0], shape)
		}
	}
	// A declared enumeration is listed after its type, in the turn's language.
	enumerated := generation.Offering{Parameters: []generation.Parameter{{Name: "mode", Label: "模式", Type: "select", Required: true,
		Options: []generation.ParameterOption{{Value: "fast"}, {Value: "best"}}}}}
	gap := executor.inputGaps(chat.ToolCommand{Tool: tool, Options: chat.TurnOptions{Locale: "en"}}, enumerated, map[string]any{})
	if len(gap) != 1 || !strings.Contains(gap[0], "allowed values: fast, best") || !strings.Contains(gap[0], "Mode, select") {
		t.Fatalf("enumerated gap = %q", gap)
	}
}
