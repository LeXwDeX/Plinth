package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// AccountInputAssetLister is the deliberately narrow owner-scoped query used
// by the personal resource page. Storage keys are returned only to the server
// and are never included in its response DTO.
type AccountInputAssetLister interface {
	ListOwnedInputAssets(
		context.Context,
		generation.OwnerID,
		int,
		int,
	) ([]generation.InputAsset, int, error)
}

type accountUsageResponse struct {
	PriceTotals      []generation.NativeAmount `json:"price_totals,omitempty"`
	UserID           string                    `json:"user_id"`
	TotalTasks       int                       `json:"total_tasks"`
	SucceededTasks   int                       `json:"succeeded_tasks"`
	FailedTasks      int                       `json:"failed_tasks"`
	EstimatedCredits int                       `json:"estimated_credits"`
	ByType           map[string]int            `json:"by_type"`
	LastActiveAt     *time.Time                `json:"last_active_at"`
}

type accountInputAssetResponse struct {
	ID               generation.AssetID `json:"id"`
	AssetType        string             `json:"asset_type"`
	OriginalFilename string             `json:"original_filename"`
	MediaType        string             `json:"media_type"`
	Size             int64              `json:"size"`
	SHA256           string             `json:"sha256"`
	CreatedAt        time.Time          `json:"created_at"`
	URL              string             `json:"url"`
}

type accountInputAssetPage struct {
	Data     []accountInputAssetResponse `json:"data"`
	Total    int                         `json:"total"`
	Page     int                         `json:"page"`
	PageSize int                         `json:"page_size"`
}

type accountResourcesResponse struct {
	Tasks      []taskResponse        `json:"tasks"`
	NextCursor string                `json:"next_cursor"`
	Uploads    accountInputAssetPage `json:"uploads"`
}

func (s *Server) registerAccountResourcesContracts() {
	routes := s.application.Runtime.HTTP.Routes
	if routes.AccountUsage != "" {
		s.mux.HandleFunc("GET "+routes.AccountUsage, s.withPrincipal(s.getAccountUsage))
	}
	if routes.AccountResources != "" {
		s.mux.HandleFunc("GET "+routes.AccountResources, s.withPrincipal(s.getAccountResources))
	}
}

func (s *Server) getAccountUsage(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	if !principal.Can(access.PermissionTasksReadOwn) {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", s.application.Authentication.Messages.Forbidden)
		return
	}
	query := request.URL.Query()
	usageSchema := s.application.Administration.Usage
	summary, err := s.administrationUsage.Summarize(request.Context(), administration.UsageRequest{
		UserID:    principal.ID(),
		Days:      boundedPositiveInteger(query.Get("days"), usageSchema.Defaults.Days, 1, usageSchema.Validation.MaximumDays),
		StartDate: query.Get("start_date"),
		EndDate:   query.Get("end_date"),
	})
	if err != nil {
		if errors.Is(err, administration.ErrUsageInvalidRange) {
			s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", usageSchema.Messages.InvalidRange)
			return
		}
		s.logger.Error("get account usage", "error", err)
		s.writeMessageProblem(writer, http.StatusInternalServerError, "internal_error", s.application.Authentication.Messages.InternalError)
		return
	}
	response := accountUsageResponse{
		UserID: principal.ID(),
		ByType: map[string]int{},
	}
	if len(summary) > 0 {
		entry := summary[0]
		response.TotalTasks = entry.TotalTasks
		response.SucceededTasks = entry.SucceededTasks
		response.FailedTasks = entry.FailedTasks
		response.EstimatedCredits = entry.EstimatedCredits
		response.PriceTotals = entry.PriceTotals
		response.ByType = entry.ByType
		if !entry.LastActiveAt.IsZero() {
			lastActive := entry.LastActiveAt.UTC()
			response.LastActiveAt = &lastActive
		}
	}
	writeJSON(writer, http.StatusOK, response)
}

func (s *Server) getAccountResources(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	if !principal.Can(access.PermissionTasksReadOwn) {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", s.application.Authentication.Messages.Forbidden)
		return
	}
	if s.accountInputAssets == nil {
		s.writeMessageProblem(writer, http.StatusServiceUnavailable, "unavailable", "error.input_asset_service_unavailable")
		return
	}
	query := request.URL.Query()
	limits := s.application.Administration.UserAssets.Limits
	pageSize := boundedPositiveInteger(query.Get("page_size"), limits.DefaultPageSize, 1, limits.MaximumPageSize)
	page := boundedPositiveInteger(query.Get("page"), limits.DefaultPage, 1, int(^uint(0)>>1)/pageSize+1)
	taskLimit := boundedPositiveInteger(query.Get("limit"), pageSize, 1, limits.MaximumPageSize)
	owner := generation.OwnerID(principal.ID())
	tasks, err := s.generation.List(request.Context(), generation.OwnTasks(owner), generation.ListQuery{
		Cursor: query.Get("cursor"),
		Limit:  taskLimit,
	})
	if err != nil {
		s.writeDomainError(writer, err)
		return
	}
	uploads, totalUploads, err := s.accountInputAssets.ListOwnedInputAssets(
		request.Context(), owner, page, pageSize,
	)
	if err != nil {
		s.logger.Error("list account input assets", "owner_id", principal.ID(), "error", err)
		s.writeMessageProblem(writer, http.StatusInternalServerError, "internal_error", s.application.Authentication.Messages.InternalError)
		return
	}
	response := accountResourcesResponse{
		Tasks:      make([]taskResponse, 0, len(tasks.Tasks)),
		NextCursor: tasks.NextCursor,
		Uploads: accountInputAssetPage{
			Data:     make([]accountInputAssetResponse, 0, len(uploads)),
			Total:    totalUploads,
			Page:     page,
			PageSize: pageSize,
		},
	}
	for _, task := range tasks.Tasks {
		response.Tasks = append(response.Tasks, s.newTaskResponse(requestLocale(request), task))
	}
	for _, asset := range uploads {
		response.Uploads.Data = append(response.Uploads.Data, accountInputAssetResponse{
			ID:               asset.ID,
			AssetType:        asset.AssetType,
			OriginalFilename: asset.OriginalFilename,
			MediaType:        asset.MediaType,
			Size:             asset.Size,
			SHA256:           asset.SHA256,
			CreatedAt:        asset.CreatedAt.UTC(),
			URL:              s.assetItemRoute.Render(string(asset.ID)),
		})
	}
	writeJSON(writer, http.StatusOK, response)
}
