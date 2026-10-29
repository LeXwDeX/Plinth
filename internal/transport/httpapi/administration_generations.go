package httpapi

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type administrationGenerationHandler func(
	http.ResponseWriter,
	*http.Request,
	access.Principal,
)

func (server *Server) registerAdministrationGenerationContracts() {
	routes := server.application.Runtime.HTTP.LegacyRoutes
	server.mux.HandleFunc(
		"GET "+routes.AdminGenerations,
		server.withAdministrationGenerationPrincipal(
			server.listAdministrationGenerations,
		),
	)
	server.mux.HandleFunc(
		"GET "+routes.AdminGeneration,
		server.withAdministrationGenerationPrincipal(
			server.getAdministrationGeneration,
		),
	)
	server.mux.HandleFunc(
		"GET "+routes.AdminGenerationDownload,
		server.withAdministrationGenerationPrincipal(
			server.downloadAdministrationGeneration,
		),
	)
}

func (server *Server) withAdministrationGenerationPrincipal(
	next administrationGenerationHandler,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		principal, err := server.identity.Identify(request)
		if err != nil {
			server.writeAdministrationGenerationError(
				writer,
				http.StatusUnauthorized,
				server.application.Authentication.Messages.Unauthenticated,
			)
			return
		}
		permission := access.Permission(
			server.application.Administration.Generations.Permission,
		)
		if !principal.Can(permission) {
			server.writeAdministrationGenerationError(
				writer,
				http.StatusForbidden,
				server.application.Authentication.Messages.Forbidden,
			)
			return
		}
		next(writer, request, principal)
	}
}

func (server *Server) listAdministrationGenerations(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	schema := server.application.Administration.Generations
	queryFields := schema.API.Query
	values := request.URL.Query()
	page, err := server.administrationGenerations.QueryGenerations(
		request.Context(),
		administration.GenerationQuery{
			Page: administrationAuditLogInteger(
				values.Get(queryFields.Page),
				schema.Limits.DefaultPage,
			),
			PageSize: administrationAuditLogInteger(
				values.Get(queryFields.PageSize),
				schema.Limits.DefaultPageSize,
			),
			UserID:   values.Get(queryFields.UserID),
			TaskType: values.Get(queryFields.TaskType),
			Status:   values.Get(queryFields.Status),
			From: administrationAuditLogTime(
				values.Get(queryFields.From),
			),
			To: administrationAuditLogTime(
				values.Get(queryFields.To),
			),
		},
	)
	if err != nil {
		server.logger.Error(
			"list administration generations",
			"error",
			err,
		)
		server.writeAdministrationGenerationError(
			writer,
			http.StatusInternalServerError,
			schema.Messages.InternalError,
		)
		return
	}
	records := make([]map[string]any, 0, len(page.Records))
	for _, record := range page.Records {
		record.NativeQuote = server.administrationQuoteIn(request, record.Offering, record.NativeQuote)
		records = append(
			records,
			administrationGenerationWire(
				schema.API.Fields,
				record,
				true,
			),
		)
	}
	server.recordAdministrationReadAudit(
		request,
		principal,
		catalog.AdministrationReadAuditSchema{
			ReadAction:   schema.Audit.ReadAction,
			ResourceType: schema.Audit.ResourceType,
		},
		values.Get(queryFields.UserID),
	)
	fields := schema.API.Fields
	server.writeAdministrationGenerationJSON(
		writer,
		http.StatusOK,
		map[string]any{
			fields.Data:     records,
			fields.Total:    page.Total,
			fields.Page:     page.Page,
			fields.PageSize: page.PageSize,
		},
	)
}

func (server *Server) getAdministrationGeneration(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	schema := server.application.Administration.Generations
	taskID := request.PathValue(schema.API.PathParameters.TaskID)
	detail, found, err := server.administrationGenerations.GetGeneration(
		request.Context(),
		taskID,
	)
	if err != nil {
		server.logger.Error(
			"get administration generation",
			"task_id",
			taskID,
			"error",
			err,
		)
		server.writeAdministrationGenerationError(
			writer,
			http.StatusInternalServerError,
			schema.Messages.InternalError,
		)
		return
	}
	if !found {
		server.writeAdministrationGenerationError(
			writer,
			http.StatusNotFound,
			schema.Messages.TaskNotFound,
		)
		return
	}
	server.recordAdministrationReadAudit(
		request,
		principal,
		catalog.AdministrationReadAuditSchema{
			ReadAction:   schema.Audit.ReadAction,
			ResourceType: schema.Audit.ResourceType,
		},
		detail.Ledger.TaskID,
	)
	fields := schema.API.Fields
	detail.Ledger.NativeQuote = server.administrationQuoteIn(request, detail.Ledger.Offering, detail.Ledger.NativeQuote)
	var cache any
	if detail.TaskCache != nil {
		cache = administrationGenerationCacheWire(
			fields,
			*detail.TaskCache,
		)
	}
	response := map[string]any{
		fields.Ledger: administrationGenerationWire(
			fields,
			detail.Ledger,
			false,
		),
		fields.TaskCache: cache,
	}
	// The ledger params already show the frozen input's asset IDs; a
	// reference image re-encoded to fit its Offering (#276) is also explained:
	// its source within the same owner, method, sizes and dimensions. A
	// Schema that names no field keeps the detail as it was.
	if fields.InputDerivations != "" {
		if derivations := server.administrationInputDerivations(request, principal, detail.Ledger.TaskID); len(derivations) > 0 {
			response[fields.InputDerivations] = derivations
		}
	}
	server.writeAdministrationGenerationJSON(writer, http.StatusOK, response)
}

// administrationInputDerivations reads the native Task's derived input
// images within the administrator's own task read scope (tasks.read.any
// reaches every owner); legacy ledger rows and read failures yield none.
func (server *Server) administrationInputDerivations(
	request *http.Request,
	principal access.Principal,
	taskID string,
) []generation.InputDerivationView {
	if server.generation == nil || taskID == "" {
		return nil
	}
	task, err := server.generation.GetScoped(request.Context(), taskReadScope(principal), generation.TaskID(taskID))
	if err != nil {
		return nil
	}
	derivations, err := server.generation.TaskInputDerivations(request.Context(), task)
	if err != nil {
		server.logger.Warn("read administration input derivations", "task_id", taskID, "error", err)
		return nil
	}
	return derivations
}

func (server *Server) downloadAdministrationGeneration(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	schema := server.application.Administration.Generations
	taskID := request.PathValue(schema.API.PathParameters.TaskID)
	stream, err := server.administrationGenerationDownloads.OpenDownload(
		request.Context(),
		taskID,
	)
	if err != nil {
		status := http.StatusInternalServerError
		message := schema.Messages.InternalError
		switch {
		case errors.Is(err, administration.ErrGenerationDownloadNotFound):
			status = http.StatusNotFound
			message = schema.Messages.TaskNotFound
		case errors.Is(err, administration.ErrGenerationDownloadNoAssets):
			status = http.StatusNotFound
			message = schema.Messages.NoDownloadableAssets
		case errors.Is(err, administration.ErrGenerationDownloadTooLarge):
			status = http.StatusRequestEntityTooLarge
			message = schema.Messages.DownloadFailed
		case errors.Is(err, administration.ErrGenerationDownloadFailed):
			status = http.StatusNotFound
			message = schema.Messages.AllDownloadsFailed
		}
		server.logger.Error(
			"download administration generation",
			"task_id",
			taskID,
			"error",
			err,
		)
		server.writeAdministrationGenerationError(
			writer,
			status,
			message,
		)
		return
	}
	server.recordProductAudit(request, administration.AuditEvent{
		Action:       schema.Audit.DownloadAction,
		ActorID:      principal.ID(),
		ActorName:    principal.Claims.Name,
		ResourceType: schema.Audit.ResourceType,
		ResourceID:   taskID,
	})
	writer.Header().Set("Content-Type", stream.MediaType)
	writer.Header().Set(
		"Content-Disposition",
		mime.FormatMediaType(
			"attachment",
			map[string]string{"filename": stream.Filename},
		),
	)
	if stream.Size >= 0 {
		writer.Header().Set(
			"Content-Length",
			strconv.FormatInt(stream.Size, 10),
		)
	}
	writer.Header().Set(
		"Cache-Control",
		server.application.Runtime.HTTP.PrivateCacheControl,
	)
	setSandboxedBytesHeaders(writer)
	writer.WriteHeader(http.StatusOK)
	if _, err := stream.WriteTo(writer); err != nil {
		server.logger.Error(
			"stream administration generation download",
			"task_id", taskID,
			"error", err,
		)
	}
}

func administrationGenerationWire(
	fields catalog.AdministrationGenerationFieldSchema,
	record administration.GenerationRecord,
	legacyJSONText bool,
) map[string]any {
	params := any(record.Params)
	resultURLs := any(record.ResultURLs)
	if legacyJSONText {
		params = string(record.Params)
		if encoded, err := json.Marshal(record.ResultURLs); err == nil {
			resultURLs = string(encoded)
		} else {
			resultURLs = "[]"
		}
	}
	var thumbnail any
	if record.ThumbnailURL != "" {
		thumbnail = record.ThumbnailURL
	}
	result := map[string]any{
		fields.ID:               record.ID,
		fields.UserID:           record.UserID,
		fields.UserName:         record.UserName,
		fields.UserEmail:        record.UserEmail,
		fields.TaskType:         record.TaskType,
		fields.TaskID:           record.TaskID,
		fields.EstimatedCredits: record.EstimatedCredits,
		fields.TaskStatus:       record.TaskStatus,
		fields.Params:           params,
		fields.ResultURLs:       resultURLs,
		fields.ThumbnailURL:     thumbnail,
		fields.CreatedAt:        record.CreatedAt,
		fields.FinishedAt:       record.FinishedAt,
	}
	if record.NativeQuote != nil {
		delete(result, fields.EstimatedCredits)
		result["native_quote"] = record.NativeQuote
	}
	return result
}

func administrationGenerationCacheWire(
	fields catalog.AdministrationGenerationFieldSchema,
	cache administration.GenerationCache,
) map[string]any {
	return map[string]any{
		fields.TaskID:     cache.TaskID,
		fields.TaskType:   cache.TaskType,
		fields.Status:     cache.Status,
		fields.ProviderID: cache.ProviderID,
		// Native opaque checkpoints and legacy provider cache data may contain
		// signed URLs, upstream IDs and credentials. They stay server-side.
		fields.CacheData: json.RawMessage(`{}`),
		fields.CachedAt:  cache.CachedAt,
		fields.UpdatedAt: cache.UpdatedAt,
	}
}

func (server *Server) writeAdministrationGenerationError(
	writer http.ResponseWriter,
	status int,
	message string,
) {
	field := server.application.Administration.Generations.API.Fields.Error
	server.writeAdministrationGenerationJSON(
		writer,
		status,
		map[string]any{field: message},
	)
}

func (server *Server) writeAdministrationGenerationJSON(
	writer http.ResponseWriter,
	status int,
	payload map[string]any,
) {
	writer.Header().Set(
		"Cache-Control",
		server.application.Runtime.HTTP.PrivateCacheControl,
	)
	// writeJSON is the single JSON exit: it adds the request ID to every
	// error body (#118) and keeps the response locale marking.
	writeJSON(writer, status, payload)
}

// administrationQuoteIn is the frozen quote of a generation record with its fee
// item names (offering copy) in the request locale. The administration list,
// detail and user asset views all read quotes through it.
func (server *Server) administrationQuoteIn(
	request *http.Request,
	offering administration.GenerationOffering,
	quote *generation.NativeQuote,
) *generation.NativeQuote {
	return server.quoteIn(requestLocale(request), generation.Offering{
		ID:         generation.OfferingID(offering.ID),
		ProviderID: offering.ProviderID,
		ModelID:    offering.ModelID,
		Capability: generation.Capability(offering.Capability),
	}, quote)
}
