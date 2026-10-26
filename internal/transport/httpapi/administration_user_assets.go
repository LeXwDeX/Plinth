package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type administrationUserAssetsHandler func(
	http.ResponseWriter,
	*http.Request,
	access.Principal,
)

func (server *Server) registerAdministrationUserAssetsContract() {
	route := server.application.Runtime.HTTP.LegacyRoutes.AdminUserAssets
	server.mux.HandleFunc(
		"GET "+route,
		server.withAdministrationUserAssetsPrincipal(
			server.getAdministrationUserAssets,
		),
	)
}

func (server *Server) withAdministrationUserAssetsPrincipal(
	next administrationUserAssetsHandler,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		principal, err := server.identity.Identify(request)
		if err != nil {
			server.writeAdministrationUserAssetsError(
				writer,
				http.StatusUnauthorized,
				server.application.Authentication.Messages.Unauthenticated,
			)
			return
		}
		permission := access.Permission(
			server.application.Administration.UserAssets.Permission,
		)
		if !principal.Can(permission) {
			server.writeAdministrationUserAssetsError(
				writer,
				http.StatusForbidden,
				server.application.Authentication.Messages.Forbidden,
			)
			return
		}
		next(writer, request, principal)
	}
}

func (server *Server) getAdministrationUserAssets(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	schema := server.application.Administration.UserAssets
	queryFields := schema.API.Query
	values := request.URL.Query()
	overview, err := server.administrationUserAssets.QueryUserAssets(
		request.Context(),
		administration.UserAssetsRequest{
			UserID: request.PathValue(
				schema.API.PathParameters.UserID,
			),
			PageSize: administrationUserAssetsInteger(
				values.Get(queryFields.PageSize),
			),
			UploadsPage: administrationUserAssetsInteger(
				values.Get(queryFields.UploadsPage),
			),
			GenerationsPage: administrationUserAssetsInteger(
				values.Get(queryFields.GenerationsPage),
			),
			DownloadsPage: administrationUserAssetsInteger(
				values.Get(queryFields.DownloadsPage),
			),
			From: administrationUserAssetsTime(
				values.Get(queryFields.From),
			),
			To: administrationUserAssetsTime(
				values.Get(queryFields.To),
			),
		},
	)
	if err != nil {
		if errors.Is(err, administration.ErrUserAssetsNotFound) {
			server.writeAdministrationUserAssetsError(
				writer,
				http.StatusNotFound,
				schema.Messages.UserNotFound,
			)
			return
		}
		server.logger.Error(
			"get administration user assets",
			"error",
			err,
		)
		server.writeAdministrationUserAssetsError(
			writer,
			http.StatusInternalServerError,
			schema.Messages.InternalError,
		)
		return
	}
	server.recordAdministrationReadAudit(
		request,
		principal,
		schema.Audit,
		request.PathValue(schema.API.PathParameters.UserID),
	)
	for index := range overview.Generations.Data {
		record := &overview.Generations.Data[index]
		record.NativeQuote = server.administrationQuoteIn(request, record.Offering, record.NativeQuote)
	}
	writeJSON(
		writer,
		http.StatusOK,
		administrationUserAssetsWire(schema, overview),
	)
}

func administrationUserAssetsInteger(value string) int {
	parsed, _ := strconv.Atoi(value)
	return parsed
}

func administrationUserAssetsTime(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	parsed = parsed.UTC()
	return &parsed
}

func administrationUserAssetsWire(
	schema catalog.AdministrationUserAssetsSchema,
	overview administration.UserAssetsOverview,
) map[string]any {
	envelope := schema.API.EnvelopeFields
	return map[string]any{
		envelope.User: administrationUserAssetsUserWire(
			schema.API.UserFields,
			overview.User,
		),
		envelope.Summary: administrationUserAssetsSummaryWire(
			schema.API.SummaryFields,
			overview.Summary,
		),
		envelope.Uploads: administrationUserAssetsUploadPageWire(
			schema,
			overview.Uploads,
		),
		envelope.Generations: administrationUserAssetsGenerationPageWire(
			schema,
			overview.Generations,
		),
		envelope.Downloads: administrationUserAssetsDownloadPageWire(
			schema,
			overview.Downloads,
		),
	}
}

func administrationUserAssetsUserWire(
	fields catalog.AdministrationUserAssetsUserFieldSchema,
	user administration.User,
) map[string]any {
	return map[string]any{
		fields.ID:        user.ID,
		fields.Username:  user.Username,
		fields.Nickname:  user.Nickname,
		fields.Email:     user.Email,
		fields.Name:      user.Name,
		fields.IsAdmin:   user.Admin,
		fields.IsActive:  user.Active,
		fields.CreatedAt: user.CreatedAt.UTC().Format(time.RFC3339Nano),
		fields.LastLogin: administrationUserAssetsOptionalTime(
			user.LastLogin,
		),
	}
}

func administrationUserAssetsSummaryWire(
	fields catalog.AdministrationUserAssetsSummaryFieldSchema,
	summary administration.UserAssetsSummary,
) map[string]any {
	return map[string]any{
		fields.TotalUploads:      summary.TotalUploads,
		fields.TotalGenerations:  summary.TotalGenerations,
		fields.TotalDownloads:    summary.TotalDownloads,
		fields.TotalStorageBytes: summary.TotalStorageBytes,
	}
}

func administrationUserAssetsUploadPageWire(
	schema catalog.AdministrationUserAssetsSchema,
	page administration.UserAssetsPage[administration.UserAssetUpload],
) map[string]any {
	fields := schema.API.UploadFields
	data := make([]map[string]any, 0, len(page.Data))
	for _, upload := range page.Data {
		data = append(data, map[string]any{
			fields.ID:               upload.ID,
			fields.AssetType:        upload.AssetType,
			fields.OriginalFilename: upload.OriginalFilename,
			fields.StoredFilename:   upload.StoredFilename,
			fields.MimeType:         upload.MimeType,
			fields.FileSize:         upload.FileSize,
			fields.StoragePath:      upload.StoragePath,
			fields.CreatedAt: upload.CreatedAt.UTC().
				Format(time.RFC3339Nano),
		})
	}
	return administrationUserAssetsPageWire(
		schema.API.PageFields,
		data,
		page.Total,
		page.Page,
		page.PageSize,
	)
}

func administrationUserAssetsGenerationPageWire(
	schema catalog.AdministrationUserAssetsSchema,
	page administration.UserAssetsPage[administration.UserAssetGeneration],
) map[string]any {
	fields := schema.API.GenerationFields
	data := make([]map[string]any, 0, len(page.Data))
	for _, generation := range page.Data {
		resultURLs, err := json.Marshal(generation.ResultURLs)
		if err != nil {
			resultURLs = []byte("[]")
		}
		params := generation.Params
		if len(params) == 0 {
			params = json.RawMessage(`{}`)
		}
		entry := map[string]any{
			fields.ID:               generation.ID,
			fields.TaskType:         generation.TaskType,
			fields.TaskID:           generation.TaskID,
			fields.TaskStatus:       generation.TaskStatus,
			fields.ResultURLs:       string(resultURLs),
			fields.Params:           string(params),
			fields.EstimatedCredits: generation.EstimatedCredits,
			fields.ThumbnailURL: administrationUserAssetsOptionalString(
				generation.ThumbnailURL,
			),
			fields.CreatedAt: generation.CreatedAt.UTC().
				Format(time.RFC3339Nano),
			fields.FinishedAt: administrationUserAssetsOptionalTime(
				generation.FinishedAt,
			),
		}
		if generation.NativeQuote != nil {
			delete(entry, fields.EstimatedCredits)
			entry["native_quote"] = generation.NativeQuote
		}
		data = append(data, entry)
	}
	return administrationUserAssetsPageWire(
		schema.API.PageFields,
		data,
		page.Total,
		page.Page,
		page.PageSize,
	)
}

func administrationUserAssetsDownloadPageWire(
	schema catalog.AdministrationUserAssetsSchema,
	page administration.UserAssetsPage[administration.UserAssetDownload],
) map[string]any {
	fields := schema.API.DownloadFields
	data := make([]map[string]any, 0, len(page.Data))
	for _, download := range page.Data {
		data = append(data, map[string]any{
			fields.ID:           download.ID,
			fields.Action:       download.Action,
			fields.ResourceType: download.ResourceType,
			fields.ResourceID:   download.ResourceID,
			fields.Detail:       download.Detail,
			fields.CreatedAt: download.CreatedAt.UTC().
				Format(time.RFC3339Nano),
		})
	}
	return administrationUserAssetsPageWire(
		schema.API.PageFields,
		data,
		page.Total,
		page.Page,
		page.PageSize,
	)
}

func administrationUserAssetsPageWire(
	fields catalog.AdministrationUserAssetsPageFieldSchema,
	data []map[string]any,
	total int,
	page int,
	pageSize int,
) map[string]any {
	return map[string]any{
		fields.Data:     data,
		fields.Total:    total,
		fields.Page:     page,
		fields.PageSize: pageSize,
	}
}

func administrationUserAssetsOptionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func administrationUserAssetsOptionalString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (server *Server) writeAdministrationUserAssetsError(
	writer http.ResponseWriter,
	status int,
	message string,
) {
	writeJSON(
		writer,
		status,
		map[string]any{
			server.application.Administration.UserAssets.API.
				EnvelopeFields.Error: message,
		},
	)
}
