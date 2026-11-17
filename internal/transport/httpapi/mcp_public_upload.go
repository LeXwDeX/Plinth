package httpapi

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/mcppublic"
)

// upload_image (#332): a reference picture as base64, for a caller that cannot
// send multipart. The picture takes the web API's own road in: the media type is
// checked against the deployment's allowed upload types, and the bytes go to the
// same input service (size cap, storage, registration), so what an upload may be
// is decided once. This tool adds only what the transport needs: the Schema's
// cap on the base64 route, and the type read from the bytes instead of taken
// from the caller. A picture belongs to the person, not to an asset.
func (s *Server) mcpUploadImage(call *mcpCall) (mcpOutcome, error) {
	service := s.application.MCPPublic
	upload := service.Upload
	if s.inputAssets == nil {
		return mcpOutcome{}, errMCPUploadUnavailable
	}
	invalid := func() mcpOutcome {
		return *s.mcpMessageProblem(call, "upload_invalid", service.Messages.UploadInvalid, mcppublic.Subject{})
	}
	text := strings.Join(strings.Fields(mcpStringArgument(call, "data")), "")
	decoded, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		if decoded, err = base64.RawStdEncoding.DecodeString(text); err != nil {
			return invalid(), nil
		}
	}
	if len(decoded) == 0 {
		return invalid(), nil
	}
	limit := upload.MaxBytes
	if configured := s.application.Runtime.InputAssets.UploadMaxBytes; configured > 0 && configured < limit {
		limit = configured
	}
	if int64(len(decoded)) > limit {
		return *s.mcpMessageProblem(call, "upload_too_large", service.Messages.UploadTooLarge, mcppublic.Subject{},
			"{max}", strconv.FormatInt(limit, 10)), nil
	}
	mediaType := normalizedUploadMediaType(http.DetectContentType(decoded))
	if !slices.Contains(upload.MediaTypes, mediaType) || !s.uploadMediaTypeAllowed(mediaType) {
		return *s.mcpMessageProblem(call, "upload_type_not_allowed", service.Messages.UploadTypeNotAllowed, mcppublic.Subject{},
			"{types}", strings.Join(upload.MediaTypes, ", ")), nil
	}
	filename := path.Base(strings.ReplaceAll(mcpStringArgument(call, "filename"), "\\", "/"))
	if filename == "." || filename == "/" || len([]rune(filename)) > upload.FilenameMaxLength {
		filename = ""
	}
	inputRuntime := s.application.Runtime.InputAssets
	asset, err := s.inputAssets.UploadAsset(call.ctx, call.owner, generation.InputAssetUpload{
		Body: bytes.NewReader(decoded), OriginalFilename: filename,
		AssetType: s.inputAssetType(mediaType, filename), MediaType: mediaType, MaxBytes: limit,
	})
	if err != nil {
		if outcome, failure := s.mcpDomainProblem(call, err, mcppublic.Subject{}); outcome != nil || failure != nil {
			if outcome != nil {
				return *outcome, nil
			}
			return mcpOutcome{}, failure
		}
		return mcpOutcome{}, err
	}
	marks, client := service.Audit, mcpClient{}
	if known, ok := call.ctx.Value(mcpClientKey{}).(mcpClient); ok {
		client = known
	}
	s.writeAuditEvent(call.ctx, administration.AuditEvent{
		Action: inputRuntime.Audit.UploadAction, ActorID: string(call.owner), ActorName: call.ownerName,
		ResourceType: inputRuntime.Audit.ResourceType, ResourceID: string(asset.ID),
		Detail: map[string]any{
			inputRuntime.Audit.DetailFields.Filename: filename, inputRuntime.Audit.DetailFields.MediaType: mediaType,
			inputRuntime.Audit.DetailFields.FileSize: asset.Size, marks.EntryField: marks.EntryValue, marks.AccessKeyField: call.keyID,
		},
		IPAddress: client.address, UserAgent: client.userAgent,
	})
	return mcpOutcome{data: map[string]any{"image": map[string]any{
		"id": string(asset.ID), "media_type": asset.MediaType, "size": asset.Size, "sha256": asset.SHA256,
	}}}, nil
}

var errMCPUploadUnavailable = errors.New("mcp: the input asset service is not assembled")
