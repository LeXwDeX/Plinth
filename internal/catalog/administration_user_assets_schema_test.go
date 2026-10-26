package catalog

import (
	"slices"
	"testing"
)

func TestDefaultCatalogOwnsAdministrationUserAssetsContract(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	schema := application.Administration.UserAssets

	if got := application.Runtime.HTTP.LegacyRoutes.AdminUserAssets; got !=
		"/api/admin/user-assets/{userID}" {
		t.Fatalf("admin user assets route = %q", got)
	}
	if schema.API.PathParameters.UserID != "userID" {
		t.Fatalf(
			"admin user assets path parameter = %q",
			schema.API.PathParameters.UserID,
		)
	}
	for name, value := range map[string]string{
		"error":               schema.API.EnvelopeFields.Error,
		"user":                schema.API.EnvelopeFields.User,
		"summary":             schema.API.EnvelopeFields.Summary,
		"uploads":             schema.API.EnvelopeFields.Uploads,
		"generations":         schema.API.EnvelopeFields.Generations,
		"downloads":           schema.API.EnvelopeFields.Downloads,
		"data":                schema.API.PageFields.Data,
		"total":               schema.API.PageFields.Total,
		"page":                schema.API.PageFields.Page,
		"page_size":           schema.API.PageFields.PageSize,
		"user_id":             schema.API.UserFields.ID,
		"username":            schema.API.UserFields.Username,
		"nickname":            schema.API.UserFields.Nickname,
		"email":               schema.API.UserFields.Email,
		"name":                schema.API.UserFields.Name,
		"is_admin":            schema.API.UserFields.IsAdmin,
		"is_active":           schema.API.UserFields.IsActive,
		"user_created_at":     schema.API.UserFields.CreatedAt,
		"last_login":          schema.API.UserFields.LastLogin,
		"total_uploads":       schema.API.SummaryFields.TotalUploads,
		"total_generations":   schema.API.SummaryFields.TotalGenerations,
		"total_downloads":     schema.API.SummaryFields.TotalDownloads,
		"total_storage_bytes": schema.API.SummaryFields.TotalStorageBytes,
		"upload_id":           schema.API.UploadFields.ID,
		"asset_type":          schema.API.UploadFields.AssetType,
		"original_filename":   schema.API.UploadFields.OriginalFilename,
		"stored_filename":     schema.API.UploadFields.StoredFilename,
		"mime_type":           schema.API.UploadFields.MimeType,
		"file_size":           schema.API.UploadFields.FileSize,
		"storage_path":        schema.API.UploadFields.StoragePath,
		"upload_created_at":   schema.API.UploadFields.CreatedAt,
		"generation_id":       schema.API.GenerationFields.ID,
		"task_type":           schema.API.GenerationFields.TaskType,
		"task_id":             schema.API.GenerationFields.TaskID,
		"task_status":         schema.API.GenerationFields.TaskStatus,
		"result_urls":         schema.API.GenerationFields.ResultURLs,
		"params":              schema.API.GenerationFields.Params,
		"estimated_credits":   schema.API.GenerationFields.EstimatedCredits,
		"thumbnail_url":       schema.API.GenerationFields.ThumbnailURL,
		"generation_created":  schema.API.GenerationFields.CreatedAt,
		"finished_at":         schema.API.GenerationFields.FinishedAt,
		"download_id":         schema.API.DownloadFields.ID,
		"action":              schema.API.DownloadFields.Action,
		"resource_type":       schema.API.DownloadFields.ResourceType,
		"resource_id":         schema.API.DownloadFields.ResourceID,
		"detail":              schema.API.DownloadFields.Detail,
		"download_created_at": schema.API.DownloadFields.CreatedAt,
		"query_page_size":     schema.API.Query.PageSize,
		"uploads_page":        schema.API.Query.UploadsPage,
		"generations_page":    schema.API.Query.GenerationsPage,
		"downloads_page":      schema.API.Query.DownloadsPage,
		"query_from":          schema.API.Query.From,
		"query_to":            schema.API.Query.To,
	} {
		if value == "" {
			t.Fatalf("administration.user_assets %s is empty", name)
		}
	}
	if schema.Permission != "generations.manage" ||
		schema.Filters.ImageAssetType != "image" ||
		schema.Filters.ModelAssetType != "model" ||
		schema.Filters.ImageMediaTypePrefix != "image/" ||
		schema.Filters.GeneratedImageResourceType != "gpt-image" ||
		schema.Limits.DefaultPage != 1 ||
		schema.Limits.DefaultPageSize != 50 ||
		schema.Limits.MaximumPageSize != 100 ||
		schema.Limits.FrontendPageSize != 20 ||
		schema.Limits.DefaultRecentDays != 3 ||
		schema.Presentation.FilenamePreviewCharacters != 30 ||
		schema.Presentation.TaskIDPreviewCharacters != 12 ||
		schema.Presentation.ResourceIDPreviewCharacters != 40 ||
		schema.Presentation.BytesPerUnit != 1000 ||
		len(schema.Presentation.SizeUnits) < 4 {
		t.Fatalf("unexpected admin user assets contract: %+v", schema)
	}
	for _, action := range schema.Filters.DownloadActions {
		if !slices.ContainsFunc(application.AuditActions, func(item ValueLabel) bool {
			return item.Value == action.Action
		}) {
			t.Fatalf("download action %q is not declared", action.Action)
		}
	}
	for _, key := range requiredAdministrationUserAssetsTextKeys {
		if schema.Presentation.Text[key] == "" {
			t.Fatalf(
				"administration.user_assets.presentation.text[%q] is empty",
				key,
			)
		}
	}
}

func TestAdministrationUserAssetsSchemaRequiresConfiguredRouteParameter(
	t *testing.T,
) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.Runtime.HTTP.LegacyRoutes.AdminUserAssets =
		"/custom/admin/user-assets/{id}"

	if err := ValidateApplicationSchema(application); err == nil {
		t.Fatal("expected mismatched user assets path parameter to be rejected")
	}
}

func TestApplicationReturnsDefensiveUserAssetsPresentationClone(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := taskCatalog.Application()
	first.Administration.UserAssets.Presentation.Text["select_user_label"] =
		"mutated"
	first.Administration.UserAssets.Presentation.SizeUnits[0] = "mutated"
	first.Administration.UserAssets.Filters.DownloadActions[0].Label = "mutated"
	second := taskCatalog.Application()

	if second.Administration.UserAssets.Presentation.Text["select_user_label"] ==
		"mutated" ||
		second.Administration.UserAssets.Presentation.SizeUnits[0] == "mutated" ||
		second.Administration.UserAssets.Filters.DownloadActions[0].Label ==
			"mutated" {
		t.Fatal("user assets schema leaked through Application clone")
	}
}
