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
