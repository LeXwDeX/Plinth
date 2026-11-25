package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #492: the system prompt makes "look at every 3D result before delivering it" a
// hard rule, but the read-only tools read GLB only and a quad P2 result is an
// FBX. The prompt must therefore say what to do when the format cannot be read:
// no paid conversion behind the person's back, record not_inspectable with the
// real refused inspection, and say plainly that the model was not checked.
func TestSystemPromptForbidsAPaidConversionForAnUnreadableModelFormat(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	prompt := product.Application().Chat.Agent.SystemPrompt
	for _, want := range []string{
		"不得为此付费转换", "record_self_check", "not_inspectable", "没做模型检查",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the system prompt lacks %q", want)
		}
	}
	// The rule still demands a real check: an unreadable format excuses the
	// check, it does not make the result checked.
	if !strings.Contains(prompt, "自检以真实调用为据") {
		t.Error("the system prompt no longer says self-check records come from real calls")
	}
}

// #492: the two not-GLB refusals used to end with "convert the model to GLB
// first", which sends the Agent to spend the person's credits on a conversion
// the person never asked for. They now state the fact and offer both ways out:
// a GLB file of the same model, or a paid conversion the person agrees to.
func TestNotGLBRefusalsNameTheFormatFactAndBothOptions(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	source := product.Application().Chat.ModelReview.Source
	english := product.Application().Localization.Bundles["en"].Fields
	// #632: the default-locale copy is Chinese; the en bundle keeps the
	// English wording, and both state the same fact and options.
	for name, text := range map[string]string{
		"source_not_glb":       source.SourceNotGLB,
		"source_bytes_not_glb": source.SourceBytesNotGLB,
	} {
		for _, want := range []string{
			"{asset}", "{media}", "不是二进制 glTF（GLB）",
			"list_session_models", "需要用户同意付费转换为 GLB",
			"record_self_check 的 not_inspectable",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q: %q", name, want, text)
			}
		}
		translated := english["chat.model_review.source."+name]
		if strings.Contains(translated, "convert the model to GLB first") {
			t.Errorf("%s still orders the Agent to convert the model: %q", name, translated)
		}
		for _, want := range []string{
			"{asset}", "{media}", "not binary glTF (GLB)",
			"list_session_models", "the person has to agree to a paid conversion to GLB",
			"record_self_check status not_inspectable",
		} {
			if !strings.Contains(translated, want) {
				t.Errorf("en %s lacks %q: %q", name, want, translated)
			}
		}
	}
}

// #492: not_inspectable is the state the Agent writes when the format cannot be
// read, so both the status and the refusal that guards it name that case.
func TestSelfCheckTextsNameTheUnreadableFormatCase(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	texts := product.Application().Chat.SelfCheck.Texts
	app := product.Application()
	translated := app.Localization.Bundles["en"].Fields["chat.self_check.texts.record_uncheckable_unproven"]
	for text, wants := range map[string][2]string{
		texts.RecordUncheckableUnproven: {"保存格式无法读取", "先检查模型"},
		translated:                      {"the saved format cannot be read", "Inspect the model first"},
	} {
		if !strings.Contains(text, wants[0]) {
			t.Errorf("record_uncheckable_unproven omits the unreadable format: %q", text)
		}
		if !strings.Contains(text, wants[1]) {
			t.Errorf("record_uncheckable_unproven no longer requires a real inspection first: %q", text)
		}
	}
	// The status enum itself tells the Agent which refusals qualify.
	for _, tool := range app.Chat.AgentTools {
		if tool.Name != "record_self_check" {
			continue
		}
		var parameters struct {
			Properties struct {
				Status struct {
					Description string `json:"description"`
				} `json:"status"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
			t.Fatalf("record_self_check parameters are not readable: %v", err)
		}
		if !strings.Contains(parameters.Properties.Status.Description, "its format is not readable") {
			t.Errorf("the not_inspectable status omits the unreadable format: %q",
				parameters.Properties.Status.Description)
		}
		return
	}
	t.Fatal("the record_self_check tool is missing")
}
