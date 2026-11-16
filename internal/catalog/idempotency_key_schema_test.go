package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #302: a request without a usable Idempotency-Key is answered with Schema
// copy that names the header, in every declared locale, instead of the generic
// "invalid request" text. The copy is required, so a Catalog that drops it does
// not load and the answer can never carry an empty detail.
func TestIdempotencyKeyRequiredCopyIsDeclaredInEveryLocale(t *testing.T) {
	const key = "error.idempotency_key_required"
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	generic := application.Content.Messages["error.invalid_input"]
	texts := map[string]string{"default": application.Content.Messages[key]}
	for locale, bundle := range application.Localization.Bundles {
		texts[locale] = bundle.Messages[key]
	}
	if len(texts) < 2 {
		t.Fatalf("the default Catalog declares no non-default locale: %v", texts)
	}
	for locale, text := range texts {
		if strings.TrimSpace(text) == "" {
			t.Errorf("%s copy for %s is empty", locale, key)
		}
		if !strings.Contains(text, "Idempotency-Key") {
			t.Errorf("%s copy %q does not name the Idempotency-Key header", locale, text)
		}
		if text == generic {
			t.Errorf("%s copy for %s is the generic invalid-input text", locale, key)
		}
	}
}

func TestIdempotencyKeyRequiredCopyCannotBeDropped(t *testing.T) {
	const key = "error.idempotency_key_required"
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		// Dropped from every locale together, so only the required-copy rule can refuse it.
		"removed from every locale": func(a *catalog.ApplicationSchema) {
			delete(a.Content.Messages, key)
			for _, bundle := range a.Localization.Bundles {
				delete(bundle.Messages, key)
			}
		},
		"blank in the default copy":     func(a *catalog.ApplicationSchema) { a.Content.Messages[key] = " " },
		"missing from the English copy": func(a *catalog.ApplicationSchema) { delete(a.Localization.Bundles["en"].Messages, key) },
	} {
		t.Run(name, func(t *testing.T) {
			product, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			application := product.Application()
			mutate(&application)
			if err := catalog.ValidateApplicationSchema(application); err == nil {
				t.Fatal("ValidateApplicationSchema accepted a Catalog without the Idempotency-Key copy")
			}
		})
	}
}
