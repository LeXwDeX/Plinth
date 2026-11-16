package modeldelivery

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #300: the administration download reads the README wording for a repeated file
// from this service, so it says what the one-click ZIP says, in the language of the
// request.
func TestRepeatTextIsTheSchemaWordingInTheRequestLocale(t *testing.T) {
	f := newFixture(t)
	application := f.catalog.Application()
	schema := application.ModelDelivery.Archive.Readme
	if !f.service.skipsIdenticalFiles() {
		t.Fatal("the default Schema names no section for repeated files")
	}

	// Without a localizer, and in the default locale, the Schema's own words.
	for _, ctx := range []context.Context{context.Background(), catalog.WithLocale(context.Background(), "zh")} {
		text := f.service.RepeatText(ctx)
		if text.Filename != schema.Filename || text.Heading != schema.SameHeading || text.Line != schema.SameLine || !text.Declared() {
			t.Fatalf("text = %+v, want the Schema's %q / %q / %q", text, schema.Filename, schema.SameHeading, schema.SameLine)
		}
	}

	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	f.service.Localize(texts)
	english := application.Localization.Bundles["en"].Fields
	text := f.service.RepeatText(catalog.WithLocale(context.Background(), "en"))
	if text.Heading != english["model_delivery.archive.readme.same_heading"] || text.Line != english["model_delivery.archive.readme.same_line"] ||
		text.Heading == schema.SameHeading || !text.Declared() {
		t.Fatalf("English text = %+v, want the bundle's %q / %q", text, english["model_delivery.archive.readme.same_heading"], english["model_delivery.archive.readme.same_line"])
	}
	// Asking in English does not rewrite the service's own Schema.
	if after := f.service.RepeatText(context.Background()); after.Heading != schema.SameHeading {
		t.Fatalf("a localized read changed the default wording: %+v", after)
	}
}
