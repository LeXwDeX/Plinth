package catalog_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #517: why an argument was refused is Schema copy, Chinese with an English
// overlay, not a sentence in the transport code.
func TestMCPPublicInvalidDetailsAreDeclaredAndLocalized(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	localizer, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	details := reflect.ValueOf(application.MCPPublic.Messages.InvalidDetails)
	for index := range details.NumField() {
		name := details.Type().Field(index).Name
		text := details.Field(index).String()
		if strings.TrimSpace(text) == "" {
			t.Errorf("%s is empty", name)
			continue
		}
		if translated := localizer.Text("en", text); translated == text || strings.TrimSpace(translated) == "" {
			t.Errorf("%s has no English overlay: %q", name, text)
		}
	}
	for name, holder := range map[string]string{"ToolNotOffering": "{capability}", "PurposeUnknown": "{purposes}"} {
		text := details.FieldByName(name).String()
		if !strings.Contains(text, holder) || !strings.Contains(localizer.Text("en", text), holder) {
			t.Errorf("%s lost %s: %q", name, holder, text)
		}
	}
	for name, edit := range map[string]func(*catalog.MCPPublicInvalidDetails){
		"empty":                   func(d *catalog.MCPPublicInvalidDetails) { d.ImagesNotTaken = " " },
		"no capability":           func(d *catalog.MCPPublicInvalidDetails) { d.ToolNotOffering = "not offered" },
		"no purposes placeholder": func(d *catalog.MCPPublicInvalidDetails) { d.PurposeUnknown = "unknown" },
	} {
		edited := defaultApplication(t)
		edit(&edited.MCPPublic.Messages.InvalidDetails)
		if err := catalog.ValidateApplicationSchema(edited); err == nil || !strings.Contains(err.Error(), "invalid_details") {
			t.Errorf("%s: validation = %v", name, err)
		}
	}
}
