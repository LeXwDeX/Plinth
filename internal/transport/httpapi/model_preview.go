package httpapi

import (
	"context"
	"io"
	"net/http"
	"strconv"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// ModelPreviewReader opens a native owned model upload only after validating
// its complete bytes and persisted identity metadata.
type ModelPreviewReader interface {
	OpenPreview(context.Context, generation.OwnerID, generation.AssetID) (generation.InputAsset, io.ReadCloser, error)
}

func (s *Server) previewInputModel(
	writer http.ResponseWriter,
	request *http.Request,
	owner generation.OwnerID,
) {
	s.setPrivateAssetCacheControl(writer)
	if s.modelPreviews == nil {
		s.writeMessageProblem(
			writer,
			http.StatusServiceUnavailable,
			"unavailable",
			"error.input_asset_service_unavailable",
		)
		return
	}
	profile := s.application.Runtime.InputAssets.ModelProfile
	if profile == nil || len(profile.PreviewMediaTypes) == 0 || profile.UploadMediaType == "" {
		http.NotFound(writer, request)
		return
	}

	asset, body, err := s.modelPreviews.OpenPreview(
		request.Context(),
		owner,
		generation.AssetID(request.PathValue("assetID")),
	)
	if body != nil {
		defer body.Close()
	}
	if err != nil {
		s.writeDomainError(writer, err)
		return
	}
	if body == nil {
		s.writeDomainError(writer, &generation.Error{
			Code:      generation.ErrUnavailable,
			Operation: "preview_input_model",
		})
		return
	}
	mediaType := normalizedUploadMediaType(asset.MediaType)
	if !slicesContains(profile.MediaTypes, mediaType) || !slicesContains(profile.PreviewMediaTypes, mediaType) {
		http.NotFound(writer, request)
		return
	}

	assetAudit := s.application.Runtime.InputAssets.Audit
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       assetAudit.AccessAction,
		ActorID:      string(owner),
		ResourceType: assetAudit.ResourceType,
		ResourceID:   string(asset.ID),
	})
	writer.Header().Set("Content-Type", profile.UploadMediaType)
	writer.Header().Set("Content-Length", strconv.FormatInt(asset.Size, 10))
	writer.Header().Set("ETag", `"`+asset.SHA256+`"`)
	setSandboxedBytesHeaders(writer)
	writer.WriteHeader(http.StatusOK)
	_, _ = io.Copy(writer, body)
}
