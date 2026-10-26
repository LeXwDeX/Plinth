package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// The README note for a converted OBJ is Schema text: it needs its {path},
// and it is only meaningful together with the roles and providers that say
// which OBJ is a conversion result.
func TestConvertedObjNoteDeclarationIsValidated(t *testing.T) {
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		"note without path": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.Archive.Readme.ConvertedObjNote = "no placeholder"
		},
		"note without roles": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.Archive.ObjPackage.ConvertedRoles = nil
		},
		"note without providers": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.Archive.ObjPackage.ConvertedProviders = nil
		},
		"roles without note": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.Archive.Readme.ConvertedObjNote = ""
		},
		"download name without extension": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.DownloadFilenameTemplate = "model-{task_id}"
		},
		"download name with a path": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.DownloadFilenameTemplate = "x/model-{task_id}{extension}"
		},
		"reference note without references": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.Archive.Readme.ConvertedObjReferenceNote = "{path}"
		},
		"reference note without path": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.Archive.Readme.ConvertedObjReferenceNote = "{references}"
		},
		"converted offerings without reference note": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.Archive.Readme.ConvertedObjReferenceNote = ""
		},
		"package note without path": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.Archive.Readme.ConvertedObjPackageNote = "{count}"
		},
		"package note without count": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.Archive.Readme.ConvertedObjPackageNote = "{path}"
		},
		"package note without providers": func(a *catalog.ApplicationSchema) {
			a.ModelDelivery.Archive.Readme.ConvertedObjNote = ""
			a.ModelDelivery.Archive.ObjPackage.ConvertedRoles = nil
			a.ModelDelivery.Archive.ObjPackage.ConvertedProviders = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			mutate(&app)
			if err := catalog.ValidateApplicationSchema(app); err == nil {
				t.Fatal("an incomplete converted-OBJ declaration was accepted")
			}
		})
	}
	// Declaring none of it is legal: the README simply has no such note.
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	app.ModelDelivery.Archive.Readme.ConvertedObjNote = ""
	app.ModelDelivery.Archive.Readme.ConvertedObjPackageNote = ""
	app.ModelDelivery.Archive.Readme.ConvertedObjReferenceNote = ""
	app.ModelDelivery.Archive.ObjPackage.ConvertedRoles = nil
	app.ModelDelivery.Archive.ObjPackage.ConvertedProviders = nil
	app.ModelDelivery.Archive.ObjPackage.ConvertedOfferings = nil
	// Removing optional copy removes its translations with it.
	delete(app.Localization.Bundles["en"].Fields, "model_delivery.archive.readme.converted_obj_note")
	delete(app.Localization.Bundles["en"].Fields, "model_delivery.archive.readme.converted_obj_package_note")
	delete(app.Localization.Bundles["en"].Fields, "model_delivery.archive.readme.converted_obj_reference_note")
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatalf("an absent note must be accepted: %v", err)
	}
}
