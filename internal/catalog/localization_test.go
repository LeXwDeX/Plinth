package catalog

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func defaultApplicationForLocalization(t *testing.T) ApplicationSchema {
	t.Helper()
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return product.Application()
}

// cloneLocalization deep-copies the localization block so a test can break
// one bundle without touching the shared default catalog.
func cloneLocalization(t *testing.T, application ApplicationSchema) ApplicationSchema {
	t.Helper()
	if application.Localization == nil {
		t.Fatal("default catalog does not declare application.localization")
	}
	raw, err := json.Marshal(application.Localization)
	if err != nil {
		t.Fatal(err)
	}
	var copied LocalizationSchema
	if err := json.Unmarshal(raw, &copied); err != nil {
		t.Fatal(err)
	}
	application.Localization = &copied
	messages := make(map[string]string, len(application.Content.Messages))
	for key, value := range application.Content.Messages {
		messages[key] = value
	}
	application.Content.Messages = messages
	return application
}

func TestDefaultCatalogDeclaresChineseAndEnglish(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	localization := application.Localization
	if localization == nil {
		t.Fatal("application.localization missing")
	}
	if localization.DefaultLocale != "zh-CN" {
		t.Fatalf("default locale = %q, want zh-CN", localization.DefaultLocale)
	}
	ids := []string{}
	for _, locale := range localization.Locales {
		ids = append(ids, locale.ID)
	}
	if strings.Join(ids, ",") != "zh-CN,en" {
		t.Fatalf("locales = %v, want zh-CN,en", ids)
	}
	english := localization.Bundles["en"]
	if len(english.Messages) != len(application.Content.Messages) {
		t.Fatalf("en messages = %d, zh-CN messages = %d", len(english.Messages), len(application.Content.Messages))
	}
}

func TestEnglishBundleHasNoChineseText(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	cjk := regexp.MustCompile(`[\x{3400}-\x{9fff}\x{3000}-\x{303f}\x{ff00}-\x{ffef}]`)
	english := application.Localization.Bundles["en"]
	for key, value := range english.Messages {
		if cjk.MatchString(value) {
			t.Errorf("en message %s still contains Chinese: %q", key, value)
		}
	}
	for path, value := range english.Fields {
		if cjk.MatchString(value) {
			t.Errorf("en field %s still contains Chinese: %q", path, value)
		}
	}
}

func TestLocalizationRejectsMissingAndExtraMessageKeys(t *testing.T) {
	application := cloneLocalization(t, defaultApplicationForLocalization(t))
	english := application.Localization.Bundles["en"]
	delete(english.Messages, "ui.header.logout")
	application.Localization.Bundles["en"] = english
	err := ValidateApplicationSchema(application)
	if err == nil || !strings.Contains(err.Error(), "ui.header.logout") {
		t.Fatalf("missing en key accepted or not named: %v", err)
	}

	application = cloneLocalization(t, defaultApplicationForLocalization(t))
	application.Localization.Bundles["en"].Messages["ui.only.in.english"] = "Only here"
	err = ValidateApplicationSchema(application)
	if err == nil || !strings.Contains(err.Error(), "ui.only.in.english") {
		t.Fatalf("extra en key accepted or not named: %v", err)
	}

	application = cloneLocalization(t, defaultApplicationForLocalization(t))
	application.Content.Messages["ui.only.in.chinese"] = "只有中文"
	err = ValidateApplicationSchema(application)
	if err == nil || !strings.Contains(err.Error(), "ui.only.in.chinese") {
		t.Fatalf("zh-CN key without en translation accepted: %v", err)
	}
}

func TestLocalizationRejectsPlaceholderDrift(t *testing.T) {
	application := cloneLocalization(t, defaultApplicationForLocalization(t))
	application.Localization.Bundles["en"].Messages["ui.pricing.default_estimate"] = "Default estimate: {total}"
	err := ValidateApplicationSchema(application)
	if err == nil || !strings.Contains(err.Error(), "ui.pricing.default_estimate") {
		t.Fatalf("placeholder drift accepted: %v", err)
	}
}

func TestLocalizationFieldsResolveToStringLeaves(t *testing.T) {
	for name, path := range map[string]string{
		"unknown":       "authentication.login.no_such_field",
		"not a string":  "authentication.login",
		"bad selector":  "content.navigation[key=nope].label",
		"messages":      "content.messages.ui.header.logout",
		"localization":  "localization.default_locale",
		"unterminated":  "content.navigation[key=workspace.label",
		"empty segment": "content..brand",
	} {
		t.Run(name, func(t *testing.T) {
			application := cloneLocalization(t, defaultApplicationForLocalization(t))
			application.Localization.Bundles["en"].Fields[path] = "x"
			if err := ValidateApplicationSchema(application); err == nil {
				t.Fatalf("field path %q accepted", path)
			}
		})
	}
}

func TestLocalizationSelectorsMatchNumericIdentities(t *testing.T) {
	document := map[string]any{"ranges": []any{
		map[string]any{"days": json.Number("7"), "label": "近 7 天"},
		map[string]any{"days": json.Number("30"), "label": "近 30 天"},
	}}
	parent, key, err := resolveLocalizationLeaf(document, "ranges[days=30].label")
	if err != nil || parent[key] != "近 30 天" {
		t.Fatalf("numeric selector = %v %v %v", parent, key, err)
	}
	if got := listItemSelector(document["ranges"].([]any), 0); got != "[days=7]" {
		t.Fatalf("suggested selector = %q", got)
	}
}

func TestLocalizationPathsAddressDottedMapKeys(t *testing.T) {
	document := map[string]any{"text": map[string]any{"board.empty": "空画板", "plain": "x"},
		"rows": []any{map[string]any{"key": "a", "labels": map[string]any{"x.y": "甲"}}}}
	for path, want := range map[string]string{
		"text[board.empty]":       "空画板",
		"text.plain":              "x",
		"rows[key=a].labels[x.y]": "甲",
	} {
		parent, key, err := resolveLocalizationLeaf(document, path)
		if err != nil || parent[key] != want {
			t.Fatalf("%s = %v %q %v", path, parent, key, err)
		}
	}
	var missing []string
	collectUncoveredChinese(document, "doc", map[leafKey]bool{}, &missing)
	if strings.Join(missing, ",") != "doc.rows[key=a].labels[x.y],doc.text[board.empty]" {
		t.Fatalf("suggested paths = %v", missing)
	}
}

func TestLocalizationCoverageRequiresEveryChineseLeaf(t *testing.T) {
	application := cloneLocalization(t, defaultApplicationForLocalization(t))
	delete(application.Localization.Bundles["en"].Fields, "authentication.login.submit_label")
	err := ValidateApplicationSchema(application)
	if err == nil || !strings.Contains(err.Error(), "authentication.login.submit_label") {
		t.Fatalf("uncovered Chinese leaf accepted: %v", err)
	}
}

func TestLocalizationRequiresOneBundlePerNonDefaultLocale(t *testing.T) {
	application := cloneLocalization(t, defaultApplicationForLocalization(t))
	delete(application.Localization.Bundles, "en")
	if err := ValidateApplicationSchema(application); err == nil {
		t.Fatal("declared locale without bundle accepted")
	}

	application = cloneLocalization(t, defaultApplicationForLocalization(t))
	application.Localization.Bundles["zh-CN"] = LocaleBundle{}
	if err := ValidateApplicationSchema(application); err == nil {
		t.Fatal("bundle for the default locale accepted")
	}

	application = cloneLocalization(t, defaultApplicationForLocalization(t))
	application.Localization.DefaultLocale = "fr"
	if err := ValidateApplicationSchema(application); err == nil {
		t.Fatal("default locale outside locales accepted")
	}
}

func TestApplicationWithoutLocalizationStillValidates(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	application.Localization = nil
	if err := ValidateApplicationSchema(application); err != nil {
		t.Fatalf("single-locale schema rejected: %v", err)
	}
	if got := application.ResolveLocale("en", "en-US"); got != application.Runtime.Frontend.DisplayLocale {
		t.Fatalf("single-locale resolve = %q", got)
	}
}

func TestResolveLocaleOrder(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	for _, test := range []struct {
		name, preferred, accept, want string
	}{
		{"default", "", "", "zh-CN"},
		{"persisted choice wins", "en", "zh-CN,zh;q=0.9", "en"},
		{"persisted choice is case-insensitive", "EN", "", "en"},
		{"unknown persisted choice falls through", "fr", "en-GB,en;q=0.8", "en"},
		{"accept-language region falls back to language", "", "en-GB", "en"},
		{"accept-language order by quality", "", "fr;q=1, zh-TW;q=0.9, en;q=0.5", "zh-CN"},
		{"zero quality is ignored", "", "en;q=0", "zh-CN"},
		{"wildcard is ignored", "", "*", "zh-CN"},
		{"garbage header", "", ";;;,q=", "zh-CN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := application.ResolveLocale(test.preferred, test.accept); got != test.want {
				t.Fatalf("ResolveLocale(%q, %q) = %q, want %q", test.preferred, test.accept, got, test.want)
			}
		})
	}
}

func decodeLocalizedPublic(t *testing.T, application ApplicationSchema, locale string) map[string]any {
	t.Helper()
	projections, err := NewLocalizedPublicSchemas(application)
	if err != nil {
		t.Fatal(err)
	}
	projection := projections.For(locale)
	var decoded map[string]any
	if err := json.Unmarshal(projection.JSON, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func lookup(t *testing.T, document map[string]any, path ...string) any {
	t.Helper()
	var current any = document
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("%v is not an object at %q", current, key)
		}
		current = object[key]
	}
	return current
}

func TestLocalizedPublicSchemaProjectsEnglish(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	english := decodeLocalizedPublic(t, application, "en")
	if got := lookup(t, english, "content", "messages", "ui.header.logout"); got != application.Localization.Bundles["en"].Messages["ui.header.logout"] {
		t.Fatalf("en logout = %v", got)
	}
	if got := lookup(t, english, "authentication", "login", "submit_label"); got != application.Localization.Bundles["en"].Fields["authentication.login.submit_label"] {
		t.Fatalf("en login submit = %v", got)
	}
	if got := lookup(t, english, "runtime", "frontend", "display_locale"); got != "en" {
		t.Fatalf("en display_locale = %v", got)
	}
	if got := lookup(t, english, "localization", "active_locale"); got != "en" {
		t.Fatalf("en active_locale = %v", got)
	}
	if _, leaked := lookup(t, english, "localization").(map[string]any)["bundles"]; leaked {
		t.Fatal("public projection leaks every locale bundle")
	}
	for _, item := range lookup(t, english, "task_states").([]any) {
		state := item.(map[string]any)
		if regexp.MustCompile(`[\x{3400}-\x{9fff}]`).MatchString(state["label"].(string)) {
			t.Fatalf("en task state still Chinese: %v", state)
		}
	}
}

func TestLocalizedPublicSchemaKeepsChineseDefault(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	chinese := decodeLocalizedPublic(t, application, "zh-CN")
	if got := lookup(t, chinese, "content", "messages", "ui.header.logout"); got != application.Content.Messages["ui.header.logout"] {
		t.Fatalf("zh-CN logout = %v", got)
	}
	if got := lookup(t, chinese, "authentication", "login", "submit_label"); got != application.Authentication.Login.SubmitLabel {
		t.Fatalf("zh-CN submit = %v", got)
	}
	if got := lookup(t, chinese, "localization", "active_locale"); got != "zh-CN" {
		t.Fatalf("zh-CN active_locale = %v", got)
	}
	// Unknown locales fall back to the default projection.
	unknown := decodeLocalizedPublic(t, application, "fr")
	if got := lookup(t, unknown, "localization", "active_locale"); got != "zh-CN" {
		t.Fatalf("unknown locale active_locale = %v", got)
	}
}

func TestLocalizedPublicSchemasHaveDistinctETags(t *testing.T) {
	projections, err := NewLocalizedPublicSchemas(defaultApplicationForLocalization(t))
	if err != nil {
		t.Fatal(err)
	}
	chinese, english := projections.For("zh-CN"), projections.For("en")
	if chinese.ETag == english.ETag || !strings.HasPrefix(english.ETag, `"sha256-`) {
		t.Fatalf("etags zh-CN=%s en=%s", chinese.ETag, english.ETag)
	}
	if english.Schema.Content.Brand.MetadataTitle == chinese.Schema.Content.Brand.MetadataTitle {
		t.Fatalf("typed en projection kept the Chinese title %q", english.Schema.Content.Brand.MetadataTitle)
	}
}

func TestCloneApplicationCopiesLocaleBundles(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	clone := cloneApplication(application)
	clone.Localization.Bundles["en"].Messages["ui.header.logout"] = "changed"
	if application.Localization.Bundles["en"].Messages["ui.header.logout"] == "changed" {
		t.Fatal("a cloned application shares its locale bundles with the source")
	}
}

func TestLocalizedProjectionDoesNotMutateSource(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	before := application.Content.Messages["ui.header.logout"]
	if _, err := NewLocalizedPublicSchemas(application); err != nil {
		t.Fatal(err)
	}
	if application.Content.Messages["ui.header.logout"] != before {
		t.Fatal("projection rewrote the shared message map")
	}
}
