package catalog

// Asset delivery, input assets and upload configuration.
import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

func validateAssetDeliveryRuntime(schema AssetDeliveryRuntimeSchema) error {
	taskAssetAudit := schema.TaskAssetAudit
	for name, value := range map[string]string{
		"download_action":        taskAssetAudit.DownloadAction,
		"resource_type":          taskAssetAudit.ResourceType,
		"detail_fields.task_id":  taskAssetAudit.DetailFields.TaskID,
		"detail_fields.asset_id": taskAssetAudit.DetailFields.AssetID,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("task_asset_audit.%s 不能为空", name)
		}
	}
	if err := validateAssetPreview(schema.AssetPreview); err != nil {
		return err
	}
	contract := schema.LegacyGeneratedImage
	if strings.TrimSpace(contract.FilenameSeparator) == "" ||
		strings.ContainsAny(contract.FilenameSeparator, "/\\") ||
		contract.OrdinalBase < 0 ||
		len(contract.AssetRoles) == 0 ||
		len(contract.ExtensionMediaTypes) == 0 ||
		strings.TrimSpace(contract.DefaultMediaType) == "" ||
		strings.TrimSpace(contract.CacheControl) == "" ||
		strings.TrimSpace(contract.NotFoundMessageKey) == "" ||
		strings.TrimSpace(contract.AuditAction) == "" ||
		strings.TrimSpace(contract.AuditResourceType) == "" {
		return errors.New("legacy_generated_image 配置不完整")
	}
	if contract.CacheControl != "private, no-store" {
		return errors.New("legacy_generated_image 缓存策略必须是 private, no-store")
	}
	roles := map[string]struct{}{}
	for _, role := range contract.AssetRoles {
		if strings.TrimSpace(role) == "" {
			return errors.New("legacy_generated_image asset role 不能为空")
		}
		if _, exists := roles[role]; exists {
			return fmt.Errorf("legacy_generated_image asset role 重复: %q", role)
		}
		roles[role] = struct{}{}
	}
	for extension, mediaType := range contract.ExtensionMediaTypes {
		if strings.TrimSpace(extension) == "" ||
			strings.ContainsAny(extension, "./\\") ||
			strings.TrimSpace(mediaType) == "" {
			return fmt.Errorf(
				"legacy_generated_image extension media type 无效: %q",
				extension,
			)
		}
	}
	proxy := schema.Proxy
	queryFields := []string{
		proxy.API.Query.URL,
		proxy.API.Query.Filename,
		proxy.API.Query.TaskID,
		proxy.API.Query.Size,
		proxy.API.Fields.Error,
	}
	seenFields := make(map[string]struct{}, len(queryFields))
	for _, field := range queryFields {
		if strings.TrimSpace(field) == "" ||
			strings.ContainsAny(field, " ?&=/#") {
			return errors.New("proxy API 字段配置无效")
		}
		if _, exists := seenFields[field]; exists {
			return fmt.Errorf("proxy API 字段重复: %q", field)
		}
		seenFields[field] = struct{}{}
	}
	statuses := proxy.API.Statuses
	if statuses.Success < 200 || statuses.Success >= 300 ||
		statuses.BadRequest < 400 || statuses.BadRequest >= 500 ||
		statuses.Forbidden < 400 || statuses.Forbidden >= 500 ||
		statuses.NotFound < 400 || statuses.NotFound >= 500 ||
		statuses.BadGateway < 500 || statuses.BadGateway >= 600 {
		return errors.New("proxy API 状态码配置无效")
	}
	if proxy.Security.HostSource != "offering_asset_hosts" ||
		len(proxy.Security.AllowedSchemes) == 0 {
		return errors.New("proxy security 配置不完整")
	}
	seenSchemes := make(map[string]struct{}, len(proxy.Security.AllowedSchemes))
	for _, scheme := range proxy.Security.AllowedSchemes {
		normalized := strings.ToLower(strings.TrimSpace(scheme))
		if normalized == "" || strings.ContainsAny(normalized, ":/\\") {
			return fmt.Errorf("proxy allowed scheme 无效: %q", scheme)
		}
		if _, exists := seenSchemes[normalized]; exists {
			return fmt.Errorf("proxy allowed scheme 重复: %q", scheme)
		}
		seenSchemes[normalized] = struct{}{}
	}
	seenHosts := make(map[string]struct{}, len(proxy.Security.AllowedHosts))
	for _, host := range proxy.Security.AllowedHosts {
		normalized := strings.TrimSuffix(
			strings.ToLower(strings.TrimSpace(host)),
			".",
		)
		if normalized == "" ||
			strings.ContainsAny(normalized, ":/\\\\ ?#@") {
			return fmt.Errorf("proxy allowed host 无效: %q", host)
		}
		if _, exists := seenHosts[normalized]; exists {
			return fmt.Errorf("proxy allowed host 重复: %q", host)
		}
		seenHosts[normalized] = struct{}{}
	}
	cache := proxy.Cache
	for name, value := range map[string]string{
		"root_setting":                 cache.RootSetting,
		"metadata_suffix":              cache.MetadataSuffix,
		"metadata_fields.url":          cache.MetadataFields.URL,
		"metadata_fields.content_type": cache.MetadataFields.ContentType,
		"metadata_fields.size":         cache.MetadataFields.Size,
		"metadata_fields.cached_at":    cache.MetadataFields.CachedAt,
		"control":                      cache.Control,
		"status_header":                cache.StatusHeader,
		"hit_value":                    cache.HitValue,
		"miss_value":                   cache.MissValue,
		"refreshed_value":              cache.RefreshedValue,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("proxy cache.%s 不能为空", name)
		}
	}
	if cache.Control != "private, no-store" || cache.CORSOrigin != "" {
		return errors.New("proxy 资产必须禁用共享缓存和跨域访问")
	}
	if cache.ShardCharacters <= 0 ||
		cache.ShardCharacters > 64 ||
		strings.ContainsAny(cache.MetadataSuffix, "/\\") {
		return errors.New("proxy cache 文件布局配置无效")
	}
	if proxy.HTTP.RequestTimeoutMilliseconds <= 0 ||
		proxy.HTTP.ResponseMaxBytes <= 0 ||
		proxy.HTTP.MaximumRedirects <= 0 {
		return errors.New("proxy HTTP 限制必须大于 0")
	}
	thumbnail := proxy.Thumbnail
	for name, value := range map[string]string{
		"root_setting":      thumbnail.RootSetting,
		"request_value":     thumbnail.RequestValue,
		"output_media_type": thumbnail.OutputMediaType,
		"filename_suffix":   thumbnail.FilenameSuffix,
		"header":            thumbnail.Header,
		"header_value":      thumbnail.HeaderValue,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("proxy thumbnail.%s 不能为空", name)
		}
	}
	if thumbnail.MaximumInputBytes <= 0 || thumbnail.MaximumPixels <= 0 || thumbnail.DecodeConcurrency <= 0 ||
		thumbnail.MaximumDimension <= 0 ||
		strings.ContainsAny(thumbnail.FilenameSuffix, "/\\") {
		return errors.New("proxy thumbnail 配置无效")
	}
	metadata := proxy.Metadata
	if len(metadata.SupportedMediaTypes) == 0 ||
		!strings.Contains(metadata.CopyrightTemplate, "{year}") ||
		strings.TrimSpace(metadata.Artist) == "" ||
		strings.TrimSpace(metadata.Software) == "" {
		return errors.New("proxy metadata 配置不完整")
	}
	seenMediaTypes := make(
		map[string]struct{},
		len(metadata.SupportedMediaTypes),
	)
	for _, mediaType := range metadata.SupportedMediaTypes {
		normalized := strings.ToLower(strings.TrimSpace(mediaType))
		if normalized == "" {
			return errors.New("proxy metadata media type 不能为空")
		}
		if _, exists := seenMediaTypes[normalized]; exists {
			return fmt.Errorf(
				"proxy metadata media type 重复: %q",
				mediaType,
			)
		}
		seenMediaTypes[normalized] = struct{}{}
	}
	if strings.TrimSpace(proxy.Audit.Action) == "" ||
		strings.TrimSpace(proxy.Audit.ResourceType) == "" ||
		strings.TrimSpace(proxy.Audit.DetailFields.Filename) == "" ||
		strings.TrimSpace(proxy.Audit.DetailFields.Size) == "" ||
		proxy.Audit.ResourceIDMaximumCharacters <= 0 {
		return errors.New("proxy audit 配置不完整")
	}
	for name, value := range map[string]string{
		"missing_url":    proxy.Messages.MissingURL,
		"invalid_url":    proxy.Messages.InvalidURL,
		"forbidden_host": proxy.Messages.ForbiddenHost,
		"fetch_failed":   proxy.Messages.FetchFailed,
		"default_media":  proxy.DefaultMediaType,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("proxy %s 不能为空", name)
		}
	}
	if len(proxy.MediaTypeExtensions) == 0 {
		return errors.New("proxy media type extensions 不能为空")
	}
	if strings.TrimSpace(
		proxy.MediaTypeExtensions[proxy.DefaultMediaType],
	) == "" {
		return errors.New(
			"proxy default_media_type 必须声明文件扩展名",
		)
	}
	for mediaType, extension := range proxy.MediaTypeExtensions {
		if strings.TrimSpace(mediaType) == "" ||
			!strings.HasPrefix(extension, ".") ||
			strings.ContainsAny(extension, "/\\") {
			return fmt.Errorf(
				"proxy media type extension 无效: %q=%q",
				mediaType,
				extension,
			)
		}
	}
	return nil
}

func validateImageUpload(schema ImageUploadSchema) error {
	if schema.MaximumPixels <= 0 {
		return errors.New("maximum_pixels 必须大于 0")
	}
	for i, conversion := range schema.Conversions {
		if len(conversion.SourceMediaTypes)+len(conversion.SourceExtensions) == 0 {
			return fmt.Errorf("conversions[%d] 缺少输入格式", i)
		}
		switch conversion.TargetMediaType {
		case "image/png", "image/jpeg", "image/webp":
		default:
			return fmt.Errorf("conversions[%d] 输出格式不支持浏览器编码", i)
		}
		for _, mediaType := range conversion.SourceMediaTypes {
			if !strings.HasPrefix(mediaType, "image/") || strings.Contains(mediaType, "*") ||
				mediaType != strings.ToLower(strings.TrimSpace(mediaType)) {
				return fmt.Errorf("conversions[%d] 输入媒体类型无效", i)
			}
		}
		for _, extension := range conversion.SourceExtensions {
			if !strings.HasPrefix(extension, ".") || len(extension) < 2 ||
				extension != strings.ToLower(strings.TrimSpace(extension)) {
				return fmt.Errorf("conversions[%d] 输入扩展名无效", i)
			}
		}
	}
	return nil
}

func validateInputAssetRuntime(schema InputAssetRuntimeSchema) error {
	if err := validateGeneratedModelProfile(schema); err != nil {
		return err
	}
	if err := validateModelInputProfile(schema); err != nil {
		return err
	}
	if strings.TrimSpace(schema.UploadFieldName) == "" ||
		schema.UploadMaxBytes <= 0 ||
		strings.TrimSpace(schema.DefaultMediaType) == "" ||
		strings.TrimSpace(schema.DefaultAssetType) == "" ||
		strings.TrimSpace(schema.JSONFormatParameter) == "" ||
		strings.TrimSpace(schema.JSONFormatValue) == "" ||
		strings.TrimSpace(schema.BinaryCacheControl) == "" {
		return errors.New("上传、格式与缓存配置不能为空")
	}
	if schema.BinaryCacheControl != "private, no-store" {
		return errors.New("输入资产缓存策略必须是 private, no-store")
	}
	if err := validateInputImageLimits(schema); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"id":                schema.API.Fields.ID,
		"asset_id":          schema.API.Fields.AssetID,
		"asset_type":        schema.API.Fields.AssetType,
		"stored_sha256":     schema.API.Fields.StoredSHA256,
		"original_filename": schema.API.Fields.OriginalFilename,
		"media_type":        schema.API.Fields.MediaType,
		"file_size":         schema.API.Fields.FileSize,
		"data_uri":          schema.API.Fields.DataURI,
		"created_at":        schema.API.Fields.CreatedAt,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("api.fields.%s 不能为空", name)
		}
	}
	for name, value := range map[string]string{
		"upload_action":          schema.Audit.UploadAction,
		"access_action":          schema.Audit.AccessAction,
		"resource_type":          schema.Audit.ResourceType,
		"detail_fields.filename": schema.Audit.DetailFields.Filename,
		"detail_fields.media_type": schema.Audit.DetailFields.
			MediaType,
		"detail_fields.file_size": schema.Audit.DetailFields.FileSize,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("audit.%s 不能为空", name)
		}
	}
	if schema.StoredContentPolicy != "preserve-source" {
		return fmt.Errorf(
			"stored_content_policy 不受支持: %q",
			schema.StoredContentPolicy,
		)
	}
	if len(schema.TypeRules) == 0 {
		return errors.New("type_rules 不能为空")
	}
	if len(schema.AllowedUploadMediaTypes) == 0 {
		return errors.New("allowed_upload_media_types 不能为空")
	}
	if len(schema.InlineMediaTypes) == 0 {
		return errors.New("inline_media_types 不能为空")
	}
	for name, values := range map[string][]string{
		"allowed_upload_media_types": schema.AllowedUploadMediaTypes,
		"inline_media_types":         schema.InlineMediaTypes,
	} {
		for _, mediaType := range values {
			if mediaType == "" ||
				mediaType != strings.ToLower(mediaType) ||
				!strings.Contains(mediaType, "/") {
				return fmt.Errorf("%s 无效: %q", name, mediaType)
			}
		}
	}
	assetTypes := map[string]struct{}{}
	mediaTypes := map[string]struct{}{}
	extensions := map[string]struct{}{}
	for index, rule := range schema.TypeRules {
		if strings.TrimSpace(rule.AssetType) == "" ||
			len(rule.MediaTypes)+len(rule.Extensions) == 0 {
			return fmt.Errorf("type_rules[%d] 缺少类型或匹配条件", index)
		}
		if _, exists := assetTypes[rule.AssetType]; exists {
			return fmt.Errorf("asset_type 重复: %q", rule.AssetType)
		}
		assetTypes[rule.AssetType] = struct{}{}
		for _, mediaType := range rule.MediaTypes {
			if mediaType == "" || mediaType != strings.ToLower(mediaType) {
				return fmt.Errorf("media_type 无效: %q", mediaType)
			}
			if _, exists := mediaTypes[mediaType]; exists {
				return fmt.Errorf("media_type 重复: %q", mediaType)
			}
			mediaTypes[mediaType] = struct{}{}
		}
		for _, extension := range rule.Extensions {
			if !strings.HasPrefix(extension, ".") ||
				extension != strings.ToLower(extension) {
				return fmt.Errorf("extension 无效: %q", extension)
			}
			if _, exists := extensions[extension]; exists {
				return fmt.Errorf("extension 重复: %q", extension)
			}
			extensions[extension] = struct{}{}
		}
	}
	if _, exists := assetTypes[schema.DefaultAssetType]; !exists {
		return fmt.Errorf(
			"default_asset_type 引用了未知类型: %q",
			schema.DefaultAssetType,
		)
	}
	return nil
}

func assetHostsFromOfferings(
	offerings generationMap,
) []string {
	hosts := make(map[string]struct{})
	for _, offering := range offerings {
		for _, host := range offering.AssetHosts {
			normalized := strings.TrimSuffix(
				strings.ToLower(strings.TrimSpace(host)),
				".",
			)
			if normalized != "" {
				hosts[normalized] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(hosts))
	for host := range hosts {
		result = append(result, host)
	}
	sort.Strings(result)
	return result
}

func validateAssetPreview(preview AssetPreviewRuntimeSchema) error {
	for name, value := range map[string]string{
		"query":              preview.Query,
		"request_value":      preview.RequestValue,
		"cache_subdirectory": preview.CacheSubdirectory,
		"output_media_type":  preview.OutputMediaType,
	} {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, " ?&=#") {
			return fmt.Errorf("asset_preview.%s 无效", name)
		}
	}
	if strings.ContainsAny(preview.CacheSubdirectory, "/\\") || preview.CacheSubdirectory == "." || preview.CacheSubdirectory == ".." {
		return errors.New("asset_preview.cache_subdirectory 必须是存储根下的一级目录名")
	}
	if preview.OutputMediaType != "image/png" {
		return errors.New("asset_preview.output_media_type 目前只支持 image/png")
	}
	if preview.MaximumDimension <= 0 || preview.MaximumInputBytes <= 0 || preview.MaximumPixels <= 0 || preview.DecodeConcurrency <= 0 {
		return errors.New("asset_preview 限制必须大于 0")
	}
	if len(preview.SourceMediaTypes) == 0 {
		return errors.New("asset_preview.source_media_types 不能为空")
	}
	seen := make(map[string]struct{}, len(preview.SourceMediaTypes))
	for _, mediaType := range preview.SourceMediaTypes {
		normalized := strings.ToLower(strings.TrimSpace(mediaType))
		if _, decodable := previewDecodableMediaTypes[normalized]; !decodable {
			return fmt.Errorf("asset_preview 不能解码 %q", mediaType)
		}
		if _, exists := seen[normalized]; exists {
			return fmt.Errorf("asset_preview.source_media_types 重复: %q", mediaType)
		}
		seen[normalized] = struct{}{}
	}
	return nil
}

// The picture formats the preview renderer (internal/assetdelivery) can decode.
var previewDecodableMediaTypes = map[string]struct{}{
	"image/png":  {},
	"image/jpeg": {},
	"image/gif":  {},
	"image/webp": {},
}
