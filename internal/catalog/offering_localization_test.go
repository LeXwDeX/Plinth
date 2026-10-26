package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// mutateDefaultCatalog decodes the embedded catalog, lets the test change it
// and loads the result.
func mutateDefaultCatalog(t *testing.T, change func(document map[string]any)) error {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(defaultCatalog))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	change(document)
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(bytes.NewReader(encoded))
	return err
}

func englishOfferingTexts(document map[string]any) map[string]any {
	localization := document["application"].(map[string]any)["localization"].(map[string]any)
	english := localization["bundles"].(map[string]any)["en"].(map[string]any)
	return english["offering_texts"].(map[string]any)
}

func TestEnglishOfferingTextsCoverEveryOffering(t *testing.T) {
	var document struct {
		Offerings []any `json:"offerings"`
	}
	if err := json.Unmarshal(defaultCatalog, &document); err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	collectChineseTexts(document.Offerings, used)
	if len(used) == 0 {
		t.Fatal("default offerings have no Chinese copy to translate")
	}
	english := defaultApplicationForLocalization(t).Localization.Bundles["en"].OfferingTexts
	for source := range used {
		translated, exists := english[source]
		if !exists {
			t.Fatalf("offering text %q has no English", source)
		}
		if chineseTextCharacter.MatchString(translated) {
			t.Fatalf("English for %q still has Chinese: %q", source, translated)
		}
	}
}

func TestOfferingLocalizationRejectsMissingAndStaleTexts(t *testing.T) {
	err := mutateDefaultCatalog(t, func(document map[string]any) {
		delete(englishOfferingTexts(document), "提示词")
	})
	if err == nil || !strings.Contains(err.Error(), "提示词") {
		t.Fatalf("offering text without English accepted: %v", err)
	}

	err = mutateDefaultCatalog(t, func(document map[string]any) {
		englishOfferingTexts(document)["没有任何 Offering 使用的文案"] = "Unused"
	})
	if err == nil || !strings.Contains(err.Error(), "过期") {
		t.Fatalf("stale offering text accepted: %v", err)
	}
}

func TestOfferingTextsRejectPlaceholderDriftAndEmptyCopy(t *testing.T) {
	application := cloneLocalization(t, defaultApplicationForLocalization(t))
	application.Localization.Bundles["en"].OfferingTexts["Matting 返回未知批次状态 {status}"] = "Matting returned an unknown batch state"
	if err := ValidateApplicationSchema(application); err == nil || !strings.Contains(err.Error(), "占位符") {
		t.Fatalf("placeholder drift accepted: %v", err)
	}

	application = cloneLocalization(t, defaultApplicationForLocalization(t))
	application.Localization.Bundles["en"].OfferingTexts["提示词"] = " "
	if err := ValidateApplicationSchema(application); err == nil || !strings.Contains(err.Error(), "译文为空") {
		t.Fatalf("empty offering translation accepted: %v", err)
	}
}

func TestLocalizationCoverageAcceptsStringFieldRoots(t *testing.T) {
	application := cloneLocalization(t, defaultApplicationForLocalization(t))
	const path = "chat.agent.title_prompt"
	found := false
	for _, root := range application.Localization.Coverage {
		found = found || root == path
	}
	if !found {
		t.Fatalf("coverage does not name %s", path)
	}
	delete(application.Localization.Bundles["en"].Fields, path)
	err := ValidateApplicationSchema(application)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("uncovered string root accepted: %v", err)
	}
}

func TestTextLocalizerTranslatesOfferingCopyAndJSON(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	localizer, err := NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	english := application.Localization.Bundles["en"].OfferingTexts
	if got := localizer.Text("en", "提示词"); got != english["提示词"] {
		t.Fatalf("offering text = %q", got)
	}

	value := map[string]any{
		"label":   "提示词",
		"options": []any{map[string]any{"value": "a-pose", "label": "无"}},
		"count":   json.Number("3"),
		"free":    "not schema copy",
	}
	localized := localizer.LocalizeJSON("en", value).(map[string]any)
	if localized["label"] != english["提示词"] || localized["free"] != "not schema copy" {
		t.Fatalf("localized = %#v", localized)
	}
	option := localized["options"].([]any)[0].(map[string]any)
	if option["label"] != english["无"] || option["value"] != "a-pose" {
		t.Fatalf("localized option = %#v", option)
	}
	if got := localizer.LocalizeJSON("zh-CN", map[string]any{"label": "提示词"}); got.(map[string]any)["label"] != "提示词" {
		t.Fatalf("default locale changed: %#v", got)
	}
}

func TestLocaleTravelsInContext(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	localizer, err := NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithLocale(context.Background(), "en")
	if LocaleFrom(ctx) != "en" || LocaleFrom(context.Background()) != "" {
		t.Fatal("locale not carried by context")
	}
	if got := localizer.TextIn(ctx, "提示词"); got != application.Localization.Bundles["en"].OfferingTexts["提示词"] {
		t.Fatalf("TextIn = %q", got)
	}
	var missing *TextLocalizer
	if got := missing.TextIn(ctx, "提示词"); got != "提示词" {
		t.Fatalf("nil localizer = %q", got)
	}
}

func TestPublicProjectionListsPersistedDefaultsInEveryLocale(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	const key = "ui.canvas.layers.initial_name"
	want := []string{application.Content.Messages[key], application.Localization.Bundles["en"].Messages[key]}
	for _, locale := range []string{"zh-CN", "en"} {
		document := decodeLocalizedPublic(t, application, locale)
		texts := lookup(t, document, "localization", "persisted_default_texts", key).([]any)
		if len(texts) != 2 || texts[0] != want[0] || texts[1] != want[1] {
			t.Fatalf("%s persisted defaults = %v, want %v", locale, texts, want)
		}
	}

	broken := cloneLocalization(t, application)
	broken.Localization.PersistedDefaults = []string{"ui.no_such_key"}
	if err := ValidateApplicationSchema(broken); err == nil || !strings.Contains(err.Error(), "ui.no_such_key") {
		t.Fatalf("unknown persisted default accepted: %v", err)
	}
}

func TestCoverageTreatsFullWidthPunctuationAsChineseCopy(t *testing.T) {
	application := cloneLocalization(t, defaultApplicationForLocalization(t))
	const path = "model_delivery.archive.readme.included_line"
	if !strings.Contains(application.ModelDelivery.Archive.Readme.IncludedLine, "：") {
		t.Fatalf("fixture expects a full-width colon in %s", path)
	}
	delete(application.Localization.Bundles["en"].Fields, path)
	if err := ValidateApplicationSchema(application); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("punctuation-only Chinese copy accepted without English: %v", err)
	}
}
