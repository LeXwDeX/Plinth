package catalog

import (
	"strings"
	"testing"
)

// #363: membership is exact. A bundle key that differs only in case is not
// the declared locale and must not pass validation.
func TestLocalizationBundleKeysAreExact(t *testing.T) {
	application := cloneLocalization(t, defaultApplicationForLocalization(t))
	bundle := application.Localization.Bundles["en"]
	delete(application.Localization.Bundles, "en")
	application.Localization.Bundles["EN"] = bundle

	err := ValidateApplicationSchema(application)
	if err == nil || !strings.Contains(err.Error(), "bundles.EN") {
		t.Fatalf("case-variant bundle key accepted or not named: %v", err)
	}
}

// The default locale names one declared locale exactly. A differently cased
// declaration is another locale, so the default must not match it.
func TestLocalizationDefaultLocaleIsExact(t *testing.T) {
	application := cloneLocalization(t, defaultApplicationForLocalization(t))
	application.Localization.Locales[0].ID = "zh-cn"
	application.Localization.Bundles["zh-cn"] = application.Localization.Bundles["en"]

	err := ValidateApplicationSchema(application)
	if err == nil || !strings.Contains(err.Error(), "default_locale") {
		t.Fatalf("case-variant default locale accepted or not named: %v", err)
	}
}

// Constructing a projection directly bypasses Catalog validation. It must
// report an undeclared locale instead of dereferencing a missing slot.
func TestLocalizedPublicSchemasRejectUndeclaredLocales(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	application.Localization.DefaultLocale = "fr"
	application.Runtime.Frontend.DisplayLocale = "fr"

	err := withoutPanic(t, func() error {
		_, err := NewLocalizedPublicSchemas(application)
		return err
	})
	if err == nil {
		t.Fatal("undeclared default locale accepted")
	}

	projections, err := NewLocalizedPublicSchemas(defaultApplicationForLocalization(t))
	if err != nil {
		t.Fatal(err)
	}
	err = withoutPanic(t, func() error {
		_, err := projections.build("fr")
		return err
	})
	if err == nil {
		t.Fatal("undeclared locale built a projection")
	}
}

func withoutPanic(t *testing.T, run func() error) (err error) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("panicked instead of returning an error: %v", recovered)
		}
	}()
	return run()
}
