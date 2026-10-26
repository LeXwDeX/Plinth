package modeldelivery

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The README inside the ZIP is written in the UI locale of the download
// request; without a localizer or in the default locale it keeps the Schema's
// own words.
func TestReadmeFollowsTheRequestLocale(t *testing.T) {
	f := newFixture(t)
	f.tripoFull("tripo-task", "owner")
	application := f.catalog.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	f.service.Localize(texts)
	english := application.Localization.Bundles["en"].Fields

	archive, err := f.service.Prepare(catalog.WithLocale(context.Background(), "en"), generation.OwnTasks("owner"), "tripo-task")
	if err != nil {
		t.Fatal(err)
	}
	readme := readZip(t, archive)["README.txt"]
	archive.Close()
	for _, path := range []string{"model_delivery.archive.readme.title", "model_delivery.archive.readme.included_heading"} {
		if !strings.Contains(readme, english[path]) {
			t.Fatalf("English README lacks %s:\n%s", path, readme)
		}
	}
	if !strings.Contains(readme, strings.ReplaceAll(english["model_delivery.archive.readme.converted_obj_note"], "{path}", "model.obj")) {
		t.Fatalf("English README lacks the converted OBJ note:\n%s", readme)
	}
	if strings.Contains(readme, application.ModelDelivery.Archive.Readme.Title) {
		t.Fatalf("English README kept the Chinese title:\n%s", readme)
	}

	if readme := readmeOf(t, f, "tripo-task"); !strings.Contains(readme, application.ModelDelivery.Archive.Readme.Title) {
		t.Fatalf("default README lost its title:\n%s", readme)
	}
	// The service's own Schema is never rewritten by a localized download.
	if f.service.schema.Archive.Readme.Title != application.ModelDelivery.Archive.Readme.Title {
		t.Fatal("localized download changed the service Schema")
	}
}
