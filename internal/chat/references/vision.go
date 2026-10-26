package references

import (
	"context"
	"encoding/base64"
	"io"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// OpenVisionImage 把用户拥有的资产/任务产物 URL 转成模型可直接读取的
// data URI（视觉上下文）。只读打开，不创建新资产记录——避免每轮为
// 历史图片无限新增 input asset。所有权按 owner 校验；非图片、不支持的
// URL 一律返回 ok=false，由调用方按 AI 图标签占位。
//
// #292：超过 vision_inline_max_bytes 的图片不再只给文字标签，而是交给
// VisionDerivative 生成一份缩小重编码的视觉副本（只给模型看，不进
// Provider 参考图）；副本也做不到上限内时才返回 ok=false。
func (resolver *Resolver) OpenVisionImage(
	ctx context.Context,
	owner generation.OwnerID,
	rawURL string,
) (string, bool) {
	if resolver.visionInlineMaxBytes <= 0 || owner == "" {
		return "", false
	}
	referencePath, err := resolver.localPath(rawURL)
	if err != nil {
		return "", false
	}
	for _, route := range resolver.inputAssetRoutes {
		values, matched := matchRoute(route, referencePath)
		if !matched || len(values) != 1 {
			continue
		}
		asset, body, err := resolver.inputs.OpenOwned(
			ctx,
			owner,
			generation.AssetID(values[0]),
		)
		if err != nil {
			return "", false
		}
		defer body.Close()
		return resolver.inlineVision(
			ctx,
			body,
			resolver.visionMediaType(asset.MediaType),
			asset.Size,
			asset.SHA256,
		)
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
			return "", false
		}
		defer content.Body.Close()
		return resolver.inlineVision(
			ctx,
			content.Body,
			resolver.visionMediaType(content.MediaType),
			content.Size,
			content.SHA256,
		)
	}
	// 旧 generated 图路由与未知 URL：不内联（旧上传路径会新建
	// input asset，视觉上下文不做这种副作用）。
	return "", false
}

func (resolver *Resolver) visionMediaType(mediaType string) string {
	if mediaType == "" {
		return resolver.visionFallbackMediaType
	}
	return mediaType
}

// inlineVision returns the picture as a data URI when it is within the inline
// limit, byte for byte as before. A larger picture gets its vision copy (#292):
// one already made for this digest is served without reading the body.
func (resolver *Resolver) inlineVision(
	ctx context.Context,
	body io.Reader,
	mediaType string,
	size int64,
	digest string,
) (string, bool) {
	if !strings.HasPrefix(strings.ToLower(mediaType), "image/") {
		return "", false
	}
	maxBytes := resolver.visionInlineMaxBytes
	deriver := resolver.visionDerivative
	if deriver != nil && size > maxBytes {
		if made, ok := deriver.Cached(digest, maxBytes); ok {
			return dataURI(made.MediaType, made.Body), true
		}
	}
	data, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil || len(data) == 0 {
		return "", false
	}
	if int64(len(data)) <= maxBytes {
		return dataURI(mediaType, data), true
	}
	if deriver == nil {
		return "", false
	}
	rest, err := io.ReadAll(io.LimitReader(body, deriver.MaximumInputBytes()-int64(len(data))+1))
	if err != nil {
		return "", false
	}
	made, err := deriver.Derive(ctx, append(data, rest...), digest, maxBytes)
	if err != nil {
		return "", false
	}
	return dataURI(made.MediaType, made.Body), true
}

func dataURI(mediaType string, data []byte) string {
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
}
