package httpapi

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

var errLegacyGeneratedImageNotFound = errors.New(
	"legacy generated image not found",
)

func (s *Server) legacyGeneratedImage(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
) {
	s.setPrivateAssetCacheControl(writer)
	contract := s.application.Runtime.AssetDelivery.LegacyGeneratedImage
	filename := path.Base(request.PathValue("filename"))
	content, err := s.openUnifiedLegacyGeneratedImage(
		request,
		taskReadScope(principal),
		contract,
		filename,
	)
	if err != nil {
		s.writeLegacyGeneratedImageDomainError(writer, err, contract)
		return
	}
	s.streamLegacyGeneratedImage(
		writer,
		request,
		principal,
		contract,
		filename,
		content,
	)
}

func (s *Server) openUnifiedLegacyGeneratedImage(
	request *http.Request,
	scope generation.TaskListScope,
	contract catalog.LegacyGeneratedImageRuntimeSchema,
	filename string,
) (generation.AssetContent, error) {
	taskID, ordinal, ok := parseLegacyGeneratedImageFilename(
		contract,
		filename,
	)
	if !ok {
		return generation.AssetContent{}, errLegacyGeneratedImageNotFound
	}
	task, err := s.generation.GetScoped(request.Context(), scope, taskID)
	if err != nil {
		return generation.AssetContent{}, err
	}
	asset, ok := legacyGeneratedImageAsset(contract, task.Assets, ordinal)
	if !ok {
		// 旧目录不记录 owner。通过当前读取 scope 验证 Task 后，才可
		// 读取没有原生 Asset 的旧文件；Task/OpenAsset 错误不走回退。
		body, size, err := s.legacyGeneratedImages.Open(request.Context(), filename)
		if err != nil {
			if os.IsNotExist(err) {
				return generation.AssetContent{}, errLegacyGeneratedImageNotFound
			}
			return generation.AssetContent{}, err
		}
		return generation.AssetContent{Body: body, Size: size}, nil
	}
	content, err := s.generation.OpenAssetScoped(
		request.Context(),
		scope,
		task.ID,
		asset.ID,
	)
	if err != nil {
		return generation.AssetContent{}, err
	}
	return content, nil
}

func (s *Server) streamLegacyGeneratedImage(
	writer http.ResponseWriter,
	request *http.Request,
	principal access.Principal,
	contract catalog.LegacyGeneratedImageRuntimeSchema,
	filename string,
	content generation.AssetContent,
) {
	defer func() {
		if closeErr := content.Body.Close(); closeErr != nil {
			s.logger.WarnContext(
				request.Context(),
				"关闭兼容图片资产失败",
				"error",
				closeErr,
			)
		}
	}()

	extension := strings.TrimPrefix(
		strings.ToLower(path.Ext(filename)),
		".",
	)
	mediaType := contract.ExtensionMediaTypes[extension]
	if mediaType == "" {
		mediaType = contract.DefaultMediaType
	}
	writer.Header().Set("Content-Type", mediaType)
	s.setStoredAssetDeliveryHeaders(writer, mediaType, safeDownloadFilename(path.Base(filename)))
	s.setPrivateAssetCacheControl(writer)
	if content.Size > 0 {
		writer.Header().Set("Content-Length", strconv.FormatInt(content.Size, 10))
	}
	writer.WriteHeader(http.StatusOK)
	if _, err := io.Copy(writer, content.Body); err != nil {
		s.logger.WarnContext(
			request.Context(),
			"流式返回兼容图片资产失败",
			"error",
			err,
		)
		return
	}
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       contract.AuditAction,
		ActorID:      principal.ID(),
		ActorName:    principal.Claims.Name,
		ResourceType: contract.AuditResourceType,
		ResourceID:   filename,
	})
}

func parseLegacyGeneratedImageFilename(
	contract catalog.LegacyGeneratedImageRuntimeSchema,
	filename string,
) (generation.TaskID, int, bool) {
	extension := path.Ext(filename)
	stem := strings.TrimSuffix(filename, extension)
	separator := strings.LastIndex(stem, contract.FilenameSeparator)
	if separator <= 0 {
		return "", 0, false
	}
	index, err := strconv.Atoi(
		stem[separator+len(contract.FilenameSeparator):],
	)
	if err != nil || index < contract.OrdinalBase {
		return "", 0, false
	}
	taskID := generation.TaskID(stem[:separator])
	return taskID, index - contract.OrdinalBase + 1, taskID != ""
}

func legacyGeneratedImageAsset(
	contract catalog.LegacyGeneratedImageRuntimeSchema,
	assets []generation.Asset,
	ordinal int,
) (generation.Asset, bool) {
	for _, role := range contract.AssetRoles {
		for _, asset := range assets {
			if asset.Role == role && asset.Ordinal == ordinal {
				return asset, true
			}
		}
	}
	return generation.Asset{}, false
}

func (s *Server) writeLegacyGeneratedImageDomainError(
	writer http.ResponseWriter,
	err error,
	contract catalog.LegacyGeneratedImageRuntimeSchema,
) {
	noteCause(writer, err)
	var domainErr *generation.Error
	if errors.As(err, &domainErr) &&
		(domainErr.Code == generation.ErrNotFound ||
			domainErr.Code == generation.ErrForbidden) {
		s.writeLegacyError(
			writer,
			http.StatusNotFound,
			contract.NotFoundMessageKey,
		)
		return
	}
	if errors.Is(err, errLegacyGeneratedImageNotFound) {
		s.writeLegacyError(
			writer,
			http.StatusNotFound,
			contract.NotFoundMessageKey,
		)
		return
	}
	s.writeLegacyError(
		writer,
		http.StatusInternalServerError,
		"error.internal",
	)
}
