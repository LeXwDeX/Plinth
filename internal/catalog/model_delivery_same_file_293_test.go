package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

const (
	sameHeadingField = "model_delivery.archive.readme.same_heading"
	sameLineField    = "model_delivery.archive.readme.same_line"
)

// #293: the ZIP leaves out an attachment whose bytes a packed file already holds, and
// the README names it. That text is Schema copy: the section heading and the line
// come together, the line says which slot and which entry, and a Schema that names
// neither keeps packing every attachment.
func TestSameFileReadmeDeclarationIsValidated(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if readme := loaded.Application().ModelDelivery.Archive.Readme; readme.SameHeading == "" || readme.SameLine == "" {
		t.Fatalf("the default Schema does not declare the same-file README text: %+v", readme)
	}

	// Each mutation breaks the pairing or a placeholder and nothing else: the English
	// copy is adjusted with the Chinese, so only the declaration rule can reject it.
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		"heading without line": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.Archive.Readme.SameLine = ""
			delete(a.Localization.Bundles["en"].Fields, sameLineField)
		},
		"line without heading": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.Archive.Readme.SameHeading = ""
			delete(a.Localization.Bundles["en"].Fields, sameHeadingField)
		},
		"line without the slot": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.Archive.Readme.SameLine = "- {path} 内容相同"
			a.Localization.Bundles["en"].Fields[sameLineField] = "- same as {path}"
		},
		"line without the entry": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.Archive.Readme.SameLine = "- {label} 内容相同"
			a.Localization.Bundles["en"].Fields[sameLineField] = "- {label} is the same"
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			mutate(&app)
			err = catalog.ValidateApplicationSchema(app)
			if err == nil || !strings.Contains(err.Error(), "same_heading") {
				t.Fatalf("an incomplete same-file declaration was accepted or refused for another reason: %v", err)
			}
		})
	}

	// Declaring neither is legal; removing the copy removes its translations with it.
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	app.ModelDelivery.Archive.Readme.SameHeading = ""
	app.ModelDelivery.Archive.Readme.SameLine = ""
	delete(app.Localization.Bundles["en"].Fields, sameHeadingField)
	delete(app.Localization.Bundles["en"].Fields, sameLineField)
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatalf("a Schema without the same-file text must be accepted: %v", err)
	}

	// Declared Chinese copy needs its English.
	c, err = catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app = c.Application()
	delete(app.Localization.Bundles["en"].Fields, sameLineField)
	if err := catalog.ValidateApplicationSchema(app); err == nil || !strings.Contains(err.Error(), sameLineField) {
		t.Fatalf("Chinese same-file copy accepted without English: %v", err)
	}
}
