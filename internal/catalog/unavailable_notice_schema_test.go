package catalog

import (
	"strings"
	"testing"
)

// #93: the shared "unavailable" notice draws only Schema copy. The default
// Catalog declares every key in Chinese and English with exactly its
// placeholders, and a translation with a missing or extra placeholder is
// refused at load.
func TestDefaultCatalogDeclaresTheUnavailableNotice(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	for key := range unavailableNoticeMessages {
		if strings.TrimSpace(app.Content.Messages[key]) == "" {
			t.Errorf("message %s missing", key)
		}
		if strings.TrimSpace(app.Localization.Bundles["en"].Messages[key]) == "" {
			t.Errorf("en message %s missing", key)
		}
	}
	broken := app
	broken.Content.Messages = map[string]string{}
	for key, value := range app.Content.Messages {
		broken.Content.Messages[key] = value
	}
	broken.Content.Messages["ui.unavailable.label"] = "无法使用"
	if err := validateUnavailableNotices(broken); err == nil {
		t.Fatal("a label without {reason} was accepted")
	}
	broken.Content.Messages["ui.unavailable.label"] = "无法使用，{reason}{extra}"
	if err := validateUnavailableNotices(broken); err == nil {
		t.Fatal("a label with an unknown placeholder was accepted")
	}
}

// #94: every short reason ("无法使用，<reason>") is declared in the default copy
// and in every bundle with the same placeholders.
func TestUnavailableReasonsAreTranslatedWithTheirPlaceholders(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	count := 0
	for key := range app.Content.Messages {
		if isUnavailableReason(key) {
			count++
		}
	}
	if count == 0 {
		t.Fatal("the default Catalog declares no short reasons")
	}
	clone := func() ApplicationSchema {
		broken := app
		localization := *app.Localization
		localization.Bundles = map[string]LocaleBundle{}
		for locale, bundle := range app.Localization.Bundles {
			messages := map[string]string{}
			for key, value := range bundle.Messages {
				messages[key] = value
			}
			bundle.Messages = messages
			localization.Bundles[locale] = bundle
		}
		broken.Localization = &localization
		broken.Content.Messages = map[string]string{}
		for key, value := range app.Content.Messages {
			broken.Content.Messages[key] = value
		}
		return broken
	}
	missing := clone()
	missing.Content.Messages["ui.unavailable.reason.test_only"] = "面数超过 {limit}"
	if err := validateUnavailableReasons(missing); err == nil {
		t.Fatal("a short reason without a translation was accepted")
	}
	mismatch := clone()
	mismatch.Content.Messages["ui.unavailable.reason.test_only"] = "面数超过 {limit}"
	bundle := mismatch.Localization.Bundles["en"]
	bundle.Messages["ui.unavailable.reason.test_only"] = "Over {max} faces"
	mismatch.Localization.Bundles["en"] = bundle
	if err := validateUnavailableReasons(mismatch); err == nil {
		t.Fatal("a translation with other placeholders was accepted")
	}
	orphan := clone()
	bundle = orphan.Localization.Bundles["en"]
	bundle.Messages["ui.unavailable.reason.test_only"] = "Orphan"
	orphan.Localization.Bundles["en"] = bundle
	if err := validateUnavailableReasons(orphan); err == nil {
		t.Fatal("a translated reason without default copy was accepted")
	}
}
