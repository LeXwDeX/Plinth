package catalog

import (
	"strings"
	"testing"
)

// #363 keeps the pair guard for every count message, including a single-locale
// external Catalog. An old CATALOG_FILE that adds a count message must add its
// `_one` form too; a wording that does not change with the count repeats the
// same text.
func TestCountForms363SingleLocaleCatalogMustAddPairs(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	application.Localization = nil
	application.Content.Messages["ui.test.count"] = "{count} items"

	err := ValidateApplicationSchema(application)
	if err == nil || !strings.Contains(err.Error(), "ui.test.count_one") {
		t.Fatalf("missing single-locale count pair accepted or not actionable: %v", err)
	}
}

func TestCountForms363SingleLocaleCatalogAcceptsPairs(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	application.Localization = nil
	application.Content.Messages["ui.test.count"] = "{count} items"
	application.Content.Messages["ui.test.count_one"] = "{count} item"
	application.Content.Messages["ui.test.invariant"] = "{count}/{maximum}"
	application.Content.Messages["ui.test.invariant_one"] = "{count}/{maximum}"

	if err := ValidateApplicationSchema(application); err != nil {
		t.Fatalf("complete single-locale pairs rejected: %v", err)
	}
}

// Canvas carries its own count copy and follows the same migration rule.
func TestCanvasCountForms363SingleLocaleCatalogMustAddPairs(t *testing.T) {
	for name, breakIt := range map[string]func(map[string]string){
		"missing singular": func(text map[string]string) {
			text["ui.test.count"] = "{count} nodes"
		},
		"placeholder drift": func(text map[string]string) {
			text["ui.test.count"] = "{count} nodes"
			text["ui.test.count_one"] = "{count} node {extra}"
		},
	} {
		t.Run(name, func(t *testing.T) {
			application := defaultApplicationForLocalization(t)
			application.Localization = nil
			text := make(map[string]string, len(application.Canvas.Presentation.Text))
			for key, value := range application.Canvas.Presentation.Text {
				text[key] = value
			}
			application.Canvas.Presentation.Text = text
			breakIt(text)

			err := ValidateApplicationSchema(application)
			if err == nil || !strings.Contains(err.Error(), "presentation.text") || !strings.Contains(err.Error(), "ui.test.count_one") {
				t.Fatalf("canvas count pair accepted or not actionable: %v", err)
			}
		})
	}
}

func TestCanvasCountForms363EnglishBundleKeepsPair(t *testing.T) {
	application := cloneLocalization(t, defaultApplicationForLocalization(t))
	delete(application.Localization.Bundles["en"].Fields, "canvas.presentation.text[relative.minutes_ago_one]")

	err := ValidateApplicationSchema(application)
	if err == nil || !strings.Contains(err.Error(), "canvas.presentation.text[relative.minutes_ago_one]") {
		t.Fatalf("English canvas count pair accepted or not named: %v", err)
	}
}

func TestCanvasCountForms363SingleLocaleCatalogAcceptsPair(t *testing.T) {
	application := defaultApplicationForLocalization(t)
	application.Localization = nil
	text := make(map[string]string, len(application.Canvas.Presentation.Text))
	for key, value := range application.Canvas.Presentation.Text {
		text[key] = value
	}
	text["ui.test.count"] = "{count} nodes"
	text["ui.test.count_one"] = "{count} node"
	application.Canvas.Presentation.Text = text

	if err := ValidateApplicationSchema(application); err != nil {
		t.Fatalf("complete canvas count pair rejected: %v", err)
	}
}
