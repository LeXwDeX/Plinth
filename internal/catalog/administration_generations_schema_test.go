package catalog

import (
	"slices"
	"testing"
)

func TestDefaultCatalogOwnsAdministrationGenerationContract(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	generations := application.Administration.Generations
	routes := application.Runtime.HTTP.LegacyRoutes

	if routes.AdminGenerations != "/api/admin/generations" ||
		routes.AdminGeneration != "/api/admin/generations/{taskID}" ||
		routes.AdminGenerationDownload !=
			"/api/admin/generations/{taskID}/download" ||
		routes.MeshyProxyAsset != "/api/meshy/proxy-asset" {
		t.Fatalf("unexpected administration generation routes: %+v", routes)
	}
	if generations.API.PathParameters.TaskID != "taskID" {
		t.Fatalf(
			"administration generation task path parameter = %q",
			generations.API.PathParameters.TaskID,
		)
	}
	if generations.Permission != "generations.manage" {
		t.Fatalf(
			"administration generation permission = %q",
			generations.Permission,
		)
	}
	for name, value := range map[string]string{
		"data":              generations.API.Fields.Data,
		"error":             generations.API.Fields.Error,
		"total":             generations.API.Fields.Total,
		"page":              generations.API.Fields.Page,
		"page_size":         generations.API.Fields.PageSize,
		"id":                generations.API.Fields.ID,
		"user_id":           generations.API.Fields.UserID,
		"user_name":         generations.API.Fields.UserName,
		"user_email":        generations.API.Fields.UserEmail,
		"task_type":         generations.API.Fields.TaskType,
		"task_id":           generations.API.Fields.TaskID,
		"estimated_credits": generations.API.Fields.EstimatedCredits,
		"task_status":       generations.API.Fields.TaskStatus,
		"params":            generations.API.Fields.Params,
		"result_urls":       generations.API.Fields.ResultURLs,
		"thumbnail_url":     generations.API.Fields.ThumbnailURL,
		"created_at":        generations.API.Fields.CreatedAt,
		"finished_at":       generations.API.Fields.FinishedAt,
		"ledger":            generations.API.Fields.Ledger,
		"task_cache":        generations.API.Fields.TaskCache,
		"input_derivations": generations.API.Fields.InputDerivations,
		"status":            generations.API.Fields.Status,
		"provider_id":       generations.API.Fields.ProviderID,
		"cache_data":        generations.API.Fields.CacheData,
		"cached_at":         generations.API.Fields.CachedAt,
		"updated_at":        generations.API.Fields.UpdatedAt,
		"redirect":          generations.API.Fields.Redirect,
		"query_page":        generations.API.Query.Page,
		"query_page_size":   generations.API.Query.PageSize,
		"query_user_id":     generations.API.Query.UserID,
		"query_task_type":   generations.API.Query.TaskType,
		"query_status":      generations.API.Query.Status,
		"query_from":        generations.API.Query.From,
		"query_to":          generations.API.Query.To,
	} {
		if value == "" {
			t.Fatalf(
				"administration.generations.api.%s is empty",
				name,
			)
		}
	}
	if generations.Limits.DefaultPage != 1 ||
		generations.Limits.DefaultPageSize != 50 ||
		generations.Limits.MaximumPageSize != 100 {
		t.Fatalf(
			"unexpected administration generation limits: %+v",
			generations.Limits,
		)
	}
	if generations.Presentation.UserIDPreviewCharacters != 8 ||
		generations.Presentation.TaskIDPreviewCharacters != 12 {
		t.Fatalf(
			"unexpected administration generation preview limits: %+v",
			generations.Presentation,
		)
	}
	if generations.Assets.ThumbnailRole != "thumbnail" ||
		generations.Assets.DownloadFilenameTemplate !=
			"{task_id}_assets.zip" ||
		generations.Assets.AssetFilenameTemplate !=
			"{role}{variant_segment}_{ordinal}{extension}" ||
		generations.Assets.VariantSeparator != "_" ||
		generations.Assets.DuplicateSeparator != "_" ||
		generations.Assets.OrdinalBase != 1 ||
		generations.Assets.ArchiveMediaType != "application/zip" ||
		generations.Assets.DefaultMediaType !=
			"application/octet-stream" ||
		generations.Assets.UnknownExtension != ".bin" ||
		generations.Assets.MaximumPackageBytes <= 0 ||
		len(generations.Assets.MediaTypeExtensions) == 0 ||
		len(generations.Assets.DownloadExcludedRoles) == 0 ||
		generations.Assets.LegacyCacheRootSetting !=
			"legacy_asset_cache_root" ||
		generations.Assets.LegacyCachePrefixCharacters != 2 ||
		generations.Assets.LegacyCacheMetadataSuffix != ".meta.json" ||
		generations.Assets.LegacyCacheContentTypeField != "contentType" ||
		generations.Assets.RemoteDownloadTimeoutMilliseconds <= 0 ||
		generations.Assets.MaximumRemoteRedirects <= 0 ||
		generations.Assets.RemoteThumbnailURLQuery != "url" ||
		generations.Assets.RemoteThumbnailTaskIDQuery != "taskId" ||
		len(generations.Assets.PreviewImageExtensions) == 0 {
		t.Fatalf(
			"administration generation thumbnail role = %q",
			generations.Assets.ThumbnailRole,
		)
	}
	if generations.Audit.DownloadAction != "download_asset" ||
		generations.Audit.ResourceType != "admin_task" {
		t.Fatalf(
			"unexpected administration generation audit: %+v",
			generations.Audit,
		)
	}
	if !slices.ContainsFunc(application.AuditActions, func(item ValueLabel) bool {
		return item.Value == generations.Audit.DownloadAction
	}) {
		t.Fatalf(
			"administration generation audit action %q is not declared",
			generations.Audit.DownloadAction,
		)
	}
	for _, key := range requiredAdministrationGenerationTextKeys {
		if generations.Presentation.Text[key] == "" {
			t.Fatalf(
				"administration.generations.presentation.text[%q] is empty",
				key,
			)
		}
	}
	if generations.Messages.NoDownloadableAssets == "" ||
		generations.Messages.AllDownloadsFailed == "" {
		t.Fatal("administration generation download messages are empty")
	}
	legacyCacheRootSetting := generations.Assets.LegacyCacheRootSetting
	if _, exists := application.Runtime.Configuration.
		Settings[legacyCacheRootSetting]; !exists {
		t.Fatal("legacy asset cache root setting is not declared")
	}
	backgroundRemoval := slices.IndexFunc(
		application.Capabilities,
		func(item CapabilityPresentation) bool {
			return item.ID == "background-removal"
		},
	)
	if backgroundRemoval < 0 ||
		application.Capabilities[backgroundRemoval].LegacyAPI == nil ||
		application.Capabilities[backgroundRemoval].LegacyAPI.
			AssetDownload == nil {
		t.Fatal("background removal legacy asset download is missing")
	}
	download := application.Capabilities[backgroundRemoval].
		LegacyAPI.AssetDownload
	if download.CacheSubdirectory != "matting" ||
		download.CacheFilenameTemplate != "{assetToken}.png" {
		t.Fatalf("background removal legacy cache = %+v", download)
	}
}

func TestAdministrationGenerationRoutesRequireConfiguredTaskParameter(
	t *testing.T,
) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.Runtime.HTTP.LegacyRoutes.AdminGeneration =
		"/custom/admin/generations/{id}"

	if err := ValidateApplicationSchema(application); err == nil {
		t.Fatal("expected mismatched administration generation route parameter")
	}
}

func TestApplicationReturnsDefensiveAdministrationGenerationClone(
	t *testing.T,
) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := taskCatalog.Application()
	first.Administration.Generations.Presentation.Text["title"] = "mutated"
	first.Administration.Generations.Presentation.DateTimeOptions["year"] =
		"short"
	first.Administration.Generations.Assets.PreviewImageExtensions[0] =
		"mutated"
	first.Administration.Generations.Assets.
		MediaTypeExtensions["image/png"] = ".mutated"
	first.Administration.Generations.Assets.DownloadExcludedRoles[0] =
		"mutated"
	second := taskCatalog.Application()

	if second.Administration.Generations.Presentation.Text["title"] ==
		"mutated" ||
		second.Administration.Generations.Presentation.
			DateTimeOptions["year"] == "short" ||
		second.Administration.Generations.Assets.
			PreviewImageExtensions[0] == "mutated" ||
		second.Administration.Generations.Assets.
			MediaTypeExtensions["image/png"] == ".mutated" ||
		second.Administration.Generations.Assets.
			DownloadExcludedRoles[0] == "mutated" {
		t.Fatal("administration generation presentation maps leaked")
	}
}
