package references

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assets/visionimage"
	"github.com/LeXwDeX/Plinth/internal/capture"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

var ErrUnsupportedReference = errors.New(
	"chat references: unsupported reference",
)

type InputGateway interface {
	UploadAsset(
		context.Context,
		generation.OwnerID,
		generation.InputAssetUpload,
	) (generation.InputAsset, error)
	OpenOwned(
		context.Context,
		generation.OwnerID,
		generation.AssetID,
	) (generation.InputAsset, io.ReadCloser, error)
}

type TaskGateway interface {
	Get(
		context.Context,
		generation.OwnerID,
		generation.TaskID,
	) (generation.Task, error)
	OpenAsset(
		context.Context,
		generation.OwnerID,
		generation.TaskID,
		generation.AssetID,
	) (generation.AssetContent, error)
}

// CaptureReader reads one owner-scoped 3D viewport capture record.
type CaptureReader interface {
	Get(ctx context.Context, ownerID, captureID string) (capture.Record, error)
}

type LegacyStore interface {
	Open(context.Context, string) (io.ReadCloser, int64, error)
}

type Config struct {
	Inputs              InputGateway
	Tasks               TaskGateway
	Legacy              LegacyStore
	InputAssetRoutes    []string
	TaskAssetRoute      string
	GeneratedImageRoute string
	PublicBaseURL       string
	UploadMaxBytes      int64
	// GeneratedReferenceMaxBytes bounds copying a result this platform
	// generated into an input asset (#276). It is not the upload limit: the
	// Offering the reference is submitted to checks its own limits and the
	// kernel re-encodes an image over them.
	GeneratedReferenceMaxBytes         int64
	GeneratedReferenceTooLargeTemplate string
	DefaultMediaType                   string
	DefaultAssetType                   string
	ReferenceFilename                  string
	LegacyGeneratedImage               catalog.LegacyGeneratedImageRuntimeSchema
	// VisionInlineMaxBytes <= 0 表示视觉内联未启用。
	VisionInlineMaxBytes    int64
	VisionFallbackMediaType string
	// VisionDerivative makes the vision copy of a picture over
	// VisionInlineMaxBytes (#292); nil leaves such a picture to its label.
	VisionDerivative *visionimage.Deriver
	// Captures and ModelAssetRole enable VerifyAttachment for 3D captures and
	// model objects; without them those claims are rejected.
	Captures       CaptureReader
	ModelAssetRole string
}

type Resolver struct {
	inputs               InputGateway
	tasks                TaskGateway
	legacy               LegacyStore
	inputAssetRoutes     []string
	taskAssetRoute       string
	generatedImageRoute  string
	publicBaseURL        string
	uploadMaxBytes       int64
	generatedMaxBytes    int64
	generatedTooLarge    string
	defaultMediaType     string
	defaultAssetType     string
	referenceFilename    string
	legacyGeneratedImage catalog.LegacyGeneratedImageRuntimeSchema
	// 视觉上下文（B9）：0 或空回退媒体类型时 OpenVisionImage 直接
	// 返回不可用。
	visionInlineMaxBytes    int64
	visionFallbackMediaType string
	visionDerivative        *visionimage.Deriver
	captures                CaptureReader
	modelAssetRole          string
}

func New(config Config) (*Resolver, error) {
	if config.Inputs == nil ||
		config.Tasks == nil ||
		config.Legacy == nil ||
		len(config.InputAssetRoutes) == 0 ||
		strings.TrimSpace(config.TaskAssetRoute) == "" ||
		strings.TrimSpace(config.GeneratedImageRoute) == "" ||
		config.UploadMaxBytes <= 0 ||
		config.GeneratedReferenceMaxBytes < config.UploadMaxBytes ||
		strings.TrimSpace(config.GeneratedReferenceTooLargeTemplate) == "" ||
		strings.TrimSpace(config.DefaultMediaType) == "" ||
		strings.TrimSpace(config.DefaultAssetType) == "" ||
		strings.TrimSpace(config.ReferenceFilename) == "" {
		return nil, errors.New("chat references: incomplete schema config")
	}
	return &Resolver{
		inputs:                  config.Inputs,
		tasks:                   config.Tasks,
		legacy:                  config.Legacy,
		inputAssetRoutes:        append([]string(nil), config.InputAssetRoutes...),
		taskAssetRoute:          config.TaskAssetRoute,
		generatedImageRoute:     config.GeneratedImageRoute,
		publicBaseURL:           strings.TrimRight(config.PublicBaseURL, "/"),
		uploadMaxBytes:          config.UploadMaxBytes,
		generatedMaxBytes:       config.GeneratedReferenceMaxBytes,
		generatedTooLarge:       config.GeneratedReferenceTooLargeTemplate,
		defaultMediaType:        config.DefaultMediaType,
		defaultAssetType:        config.DefaultAssetType,
		referenceFilename:       config.ReferenceFilename,
		legacyGeneratedImage:    config.LegacyGeneratedImage,
		visionInlineMaxBytes:    config.VisionInlineMaxBytes,
		visionFallbackMediaType: config.VisionFallbackMediaType,
		visionDerivative:        config.VisionDerivative,
		captures:                config.Captures,
		modelAssetRole:          config.ModelAssetRole,
	}, nil
}

func (resolver *Resolver) Resolve(
	ctx context.Context,
	owner generation.OwnerID,
	values []string,
) ([]generation.AssetID, error) {
	if owner == "" {
		return nil, ErrUnsupportedReference
	}
	result := make([]generation.AssetID, 0, len(values))
	for _, value := range values {
		id, err := resolver.resolveOne(ctx, owner, value)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: %w",
				ErrUnsupportedReference,
				err,
			)
		}
		result = append(result, id)
	}
	return result, nil
}

func (resolver *Resolver) resolveOne(
	ctx context.Context,
	owner generation.OwnerID,
	value string,
) (generation.AssetID, error) {
	if strings.HasPrefix(value, "data:") {
		body, mediaType, err := decodeDataURI(value)
		if err != nil {
			return "", err
		}
		return resolver.upload(
			ctx,
			owner,
			bytes.NewReader(body),
			mediaType,
			resolver.uploadMaxBytes,
		)
	}
	referencePath, err := resolver.localPath(value)
	if err != nil {
		return "", err
	}
	for _, route := range resolver.inputAssetRoutes {
		if values, matched := matchRoute(route, referencePath); matched &&
			len(values) == 1 {
			id := generation.AssetID(values[0])
			_, body, err := resolver.inputs.OpenOwned(ctx, owner, id)
			if err != nil {
				return "", err
			}
			if err := body.Close(); err != nil {
				return "", err
			}
			return id, nil
		}
	}
	if values, matched := matchRoute(
		resolver.taskAssetRoute,
		referencePath,
	); matched && len(values) == 2 {
		content, err := resolver.tasks.OpenAsset(
			ctx,
			owner,
			generation.TaskID(values[0]),
			generation.AssetID(values[1]),
		)
		if err != nil {
			return "", err
		}
		defer content.Body.Close()
		return resolver.uploadGenerated(
			ctx,
			owner,
			content.Body,
			content.Size,
			defaultString(content.MediaType, resolver.defaultMediaType),
		)
	}
	if values, matched := matchRoute(
		resolver.generatedImageRoute,
		referencePath,
	); matched && len(values) == 1 {
		return resolver.resolveGenerated(ctx, owner, values[0])
	}
	return "", ErrUnsupportedReference
}

func (resolver *Resolver) resolveGenerated(
	ctx context.Context,
	owner generation.OwnerID,
	filename string,
) (generation.AssetID, error) {
	taskID, ordinal, ok := parseGeneratedFilename(
		resolver.legacyGeneratedImage,
		filename,
	)
	if !ok {
		return "", ErrUnsupportedReference
	}
	task, err := resolver.tasks.Get(ctx, owner, taskID)
	if err != nil {
		return "", err
	}
	if task.ID != taskID || task.OwnerID != owner {
		return "", &generation.Error{
			Code:      generation.ErrForbidden,
			Operation: "resolve_reference",
		}
	}
	if asset, found := generatedAsset(
		resolver.legacyGeneratedImage,
		task.Assets,
		ordinal,
	); found {
		content, err := resolver.tasks.OpenAsset(ctx, owner, task.ID, asset.ID)
		if err != nil {
			return "", err
		}
		defer content.Body.Close()
		return resolver.uploadGenerated(
			ctx,
			owner,
			content.Body,
			content.Size,
			defaultString(content.MediaType, asset.MediaType),
		)
	}
	// 旧目录没有 owner 元数据。只有已验证本人 Task、且没有原生产物
	// 的文件才可兼容读取；任务缺席、墓碑和存储错误均不能授予归属。
	body, size, err := resolver.legacy.Open(ctx, filename)
	if err != nil {
		return "", err
	}
	defer body.Close()
	extension := strings.TrimPrefix(
		strings.ToLower(path.Ext(filename)),
		".",
	)
	mediaType := resolver.legacyGeneratedImage.ExtensionMediaTypes[extension]
	return resolver.uploadGenerated(
		ctx,
		owner,
		body,
		size,
		defaultString(mediaType, resolver.legacyGeneratedImage.DefaultMediaType),
	)
}

// uploadGenerated copies a result this platform generated into an input asset
// under the generated-reference limit, not the upload limit (#276). A result
// over it is refused before any byte is read, with its size and the limit.
func (resolver *Resolver) uploadGenerated(
	ctx context.Context,
	owner generation.OwnerID,
	body io.Reader,
	size int64,
	mediaType string,
) (generation.AssetID, error) {
	if size > resolver.generatedMaxBytes {
		return "", &generation.Error{
			Code:      generation.ErrInvalidInput,
			Operation: "resolve_reference",
			Detail: schema.FillSizeRefusal(
				resolver.generatedTooLarge, "", size, resolver.generatedMaxBytes,
			),
			UserFacing:     true,
			DetailTemplate: resolver.generatedTooLarge,
			DetailValues: []string{
				"{size}", schema.SizeText(size, true),
				"{limit}", schema.SizeText(resolver.generatedMaxBytes, false),
			},
		}
	}
	return resolver.upload(ctx, owner, body, mediaType, resolver.generatedMaxBytes)
}

func (resolver *Resolver) upload(
	ctx context.Context,
	owner generation.OwnerID,
	body io.Reader,
	mediaType string,
	maxBytes int64,
) (generation.AssetID, error) {
	asset, err := resolver.inputs.UploadAsset(
		ctx,
		owner,
		generation.InputAssetUpload{
			Body:             body,
			OriginalFilename: resolver.uploadFilename(mediaType),
			AssetType:        resolver.defaultAssetType,
			MediaType:        defaultString(mediaType, resolver.defaultMediaType),
			MaxBytes:         maxBytes,
		},
	)
	if err != nil {
		return "", err
	}
	return asset.ID, nil
}

// uploadFilename 为配置的参考文件名补上媒体类型扩展名：
// 参数级校验按扩展名过滤（如蒙版只接受 .png），无扩展名会被拒绝。
func (resolver *Resolver) uploadFilename(mediaType string) string {
	filename := resolver.referenceFilename
	if path.Ext(filename) != "" {
		return filename
	}
	for _, candidate := range []string{".png", ".jpg", ".jpeg", ".webp", ".gif"} {
		if extensionMatchesMediaType(mediaType, candidate) {
			return filename + candidate
		}
	}
	if extensions, err := mime.ExtensionsByType(mediaType); err == nil && len(extensions) > 0 {
		return filename + extensions[0]
	}
	return filename
}

func extensionMatchesMediaType(mediaType string, extension string) bool {
	extensions, err := mime.ExtensionsByType(mediaType)
	if err != nil {
		return false
	}
	for _, candidate := range extensions {
		if candidate == extension {
			return true
		}
	}
	return false
}

func (resolver *Resolver) localPath(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return "", ErrUnsupportedReference
	}
	if parsed.IsAbs() || parsed.Host != "" {
		if resolver.publicBaseURL == "" {
			return "", ErrUnsupportedReference
		}
		base, baseErr := url.Parse(resolver.publicBaseURL)
		if baseErr != nil ||
			parsed.Scheme != base.Scheme ||
			parsed.Host != base.Host {
			return "", ErrUnsupportedReference
		}
	}
	decoded, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil || decoded == "" {
		return "", ErrUnsupportedReference
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == "." || segment == ".." {
			return "", ErrUnsupportedReference
		}
	}
	return decoded, nil
}

func decodeDataURI(value string) ([]byte, string, error) {
	comma := strings.IndexByte(value, ',')
	if comma <= len("data:") {
		return nil, "", ErrUnsupportedReference
	}
	metadata := value[len("data:"):comma]
	encoded := value[comma+1:]
	base64Encoded := false
	if strings.HasSuffix(metadata, ";base64") {
		base64Encoded = true
		metadata = strings.TrimSuffix(metadata, ";base64")
	}
	mediaType, _, err := mime.ParseMediaType(metadata)
	if err != nil {
		return nil, "", err
	}
	if base64Encoded {
		body, err := base64.StdEncoding.DecodeString(encoded)
		return body, mediaType, err
	}
	decoded, err := url.PathUnescape(encoded)
	return []byte(decoded), mediaType, err
}

func matchRoute(template string, target string) ([]string, bool) {
	templateParts := splitPath(template)
	targetParts := splitPath(target)
	if len(templateParts) != len(targetParts) {
		return nil, false
	}
	var values []string
	for index := range templateParts {
		part := templateParts[index]
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			if targetParts[index] == "" {
				return nil, false
			}
			values = append(values, targetParts[index])
			continue
		}
		if part != targetParts[index] {
			return nil, false
		}
	}
	return values, true
}

func splitPath(value string) []string {
	return strings.Split(strings.Trim(value, "/"), "/")
}

func parseGeneratedFilename(
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

func generatedAsset(
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

func defaultString(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
