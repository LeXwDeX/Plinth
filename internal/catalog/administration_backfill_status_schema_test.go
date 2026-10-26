package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestDefaultCatalogOwnsAdministrationBackfillStatusContract(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application().Administration.BackfillStatus
	if schema.API.Fields.Error != "error" ||
		schema.API.Fields.Message != "message" ||
		schema.API.Fields.Fixed != "fixed" ||
		schema.API.Fields.Total != "total" ||
		schema.API.Fields.SyncFixed != "syncFixed" ||
		schema.API.Fields.AsyncFixed != "asyncFixed" ||
		schema.API.Fields.Unchanged != "unchanged" {
		t.Fatalf("api fields = %#v", schema.API.Fields)
	}
	if schema.Permission != "generations.manage" ||
		schema.Statuses.Pending != "PENDING" ||
		schema.Statuses.Succeeded != "SUCCEEDED" {
		t.Fatalf(
			"permission/statuses = %q %#v",
			schema.Permission,
			schema.Statuses,
		)
	}
	if len(schema.SyncProviderIDs) != 2 ||
		schema.SyncProviderIDs[0] != "openai" ||
		schema.SyncProviderIDs[1] != "gemini" {
		t.Fatalf("sync providers = %#v", schema.SyncProviderIDs)
	}
	if schema.CacheResultFields.ModelURLs != "model_urls" ||
		schema.CacheResultFields.ThumbnailURL != "thumbnail_url" ||
		schema.CacheResultFields.ImageURLs != "image_urls" ||
		schema.CacheResultFields.TextureURLs != "texture_urls" ||
		schema.CacheResultFields.BaseColor != "base_color" ||
		schema.CacheResultFields.VideoURL != "video_url" {
		t.Fatalf("cache result fields = %#v", schema.CacheResultFields)
	}
	if schema.Messages.NoPending == "" ||
		schema.Messages.Complete == "" ||
		schema.Messages.InternalError == "" {
		t.Fatalf("messages = %#v", schema.Messages)
	}
}

func TestAdministrationBackfillStatusRejectsDuplicateSyncProvider(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.Administration.BackfillStatus.SyncProviderIDs =
		[]string{"openai", "openai"}
	if err := catalog.ValidateApplicationSchema(application); err == nil {
		t.Fatal("expected duplicate sync Provider to fail")
	}
}
