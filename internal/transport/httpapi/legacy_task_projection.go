package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

// projectLegacyOutputAssets 按 Product Schema 把平台 Asset 投影成旧前端的
// 顶层响应字段。字段名、角色和集合形态均不在 Transport 中写死。
func projectLegacyOutputAssets(
	response map[string]any,
	task generation.Task,
	projections []catalog.LegacyOutputProjectionSchema,
	assetDownload *catalog.LegacyAssetDownloadSchema,
	taskAssetRoute routing.Two,
) {
	for _, projection := range projections {
		switch projection.Mode {
		case "array":
			values := make([]string, 0, len(task.Assets))
			for _, asset := range task.Assets {
				if !legacyProjectionMatchesRole(asset, projection.Role) {
					continue
				}
				values = append(
					values,
					nativeTaskAssetURL(taskAssetRoute, task.ID, asset.ID),
				)
			}
			response[projection.Name] = values
		case "map":
			values := make(map[string]string)
			for _, asset := range task.Assets {
				if !legacyProjectionMatchesRole(asset, projection.Role) {
					continue
				}
				key := strings.TrimSpace(asset.Variant)
				if key == "" {
					key = strings.TrimSpace(projection.DefaultKey)
				}
				if key == "" {
					continue
				}
				values[key] =
					nativeTaskAssetURL(taskAssetRoute, task.ID, asset.ID)
			}
			response[projection.Name] = values
		case "scalar":
			for _, asset := range task.Assets {
				if !legacyProjectionMatchesRole(asset, projection.Role) {
					continue
				}
				response[projection.Name] =
					nativeTaskAssetURL(taskAssetRoute, task.ID, asset.ID)
				break
			}
		case "array-map":
			grouped := make(map[int]map[string]string)
			for _, asset := range task.Assets {
				if !legacyProjectionMatchesRole(asset, projection.Role) {
					continue
				}
				segments := strings.SplitN(asset.Variant, ".", 2)
				if len(segments) != 2 ||
					strings.TrimSpace(segments[1]) == "" {
					continue
				}
				index, err := strconv.Atoi(segments[0])
				if err != nil || index < 1 {
					continue
				}
				if grouped[index] == nil {
					grouped[index] = make(map[string]string)
				}
				grouped[index][segments[1]] =
					nativeTaskAssetURL(taskAssetRoute, task.ID, asset.ID)
			}
			indices := make([]int, 0, len(grouped))
			for index := range grouped {
				indices = append(indices, index)
			}
			sort.Ints(indices)
			values := make([]map[string]string, 0, len(indices))
			for _, index := range indices {
				values = append(values, grouped[index])
			}
			response[projection.Name] = values
		case "records":
			response[projection.Name] = projectLegacyRecords(
				task,
				projection,
				assetDownload,
				taskAssetRoute,
			)
		}
	}
}

type legacyProviderRecord struct {
	Identifier        string `json:"identifier"`
	Status            string `json:"status"`
	Downloadable      bool   `json:"downloadable"`
	SourceName        string `json:"source_name"`
	ErrorMessage      string `json:"error_message"`
	InvalidReason     string `json:"invalid_reason"`
	SuccessfulOrdinal int    `json:"successful_ordinal"`
}

func projectLegacyRecords(
	task generation.Task,
	projection catalog.LegacyOutputProjectionSchema,
	assetDownload *catalog.LegacyAssetDownloadSchema,
	taskAssetRoute routing.Two,
) []map[string]any {
	if projection.RecordSource == "provider-data" {
		return projectLegacyProviderRecords(task, projection, assetDownload)
	}
	values := make([]map[string]any, 0, len(task.Assets))
	for _, asset := range task.Assets {
		if !legacyProjectionMatchesRole(asset, projection.Role) {
			continue
		}
		record := make(map[string]any, len(projection.RecordFields))
		for _, field := range projection.RecordFields {
			value, exists := legacyRecordFieldValue(
				field,
				task,
				asset,
				assetDownload,
				taskAssetRoute,
			)
			if exists {
				record[field.Name] = value
			}
		}
		values = append(values, record)
	}
	return values
}

func projectLegacyProviderRecords(
	task generation.Task,
	projection catalog.LegacyOutputProjectionSchema,
	assetDownload *catalog.LegacyAssetDownloadSchema,
) []map[string]any {
	providerRecords := make([]legacyProviderRecord, 0)
	if len(task.ProviderData) == 0 ||
		json.Unmarshal(task.ProviderData, &providerRecords) != nil {
		return []map[string]any{}
	}
	assets := make([]generation.Asset, 0, len(task.Assets))
	for _, asset := range task.Assets {
		if legacyProjectionMatchesRole(asset, projection.Role) {
			assets = append(assets, asset)
		}
	}
	values := make([]map[string]any, 0, len(providerRecords))
	for _, providerRecord := range providerRecords {
		record := make(map[string]any, len(projection.RecordFields))
		for _, field := range projection.RecordFields {
			value, exists := legacyProviderRecordFieldValue(
				field,
				task,
				providerRecord,
				assets,
				assetDownload,
			)
			if exists {
				record[field.Name] = value
			}
		}
		values = append(values, record)
	}
	return values
}

func legacyProviderRecordFieldValue(
	field catalog.LegacyRecordFieldSchema,
	task generation.Task,
	record legacyProviderRecord,
	assets []generation.Asset,
	assetDownload *catalog.LegacyAssetDownloadSchema,
) (any, bool) {
	if len(field.Literal) > 0 {
		var value any
		if err := json.Unmarshal(field.Literal, &value); err != nil {
			return nil, false
		}
		return value, true
	}
	switch field.Source {
	case "provider-record-identifier":
		index := record.SuccessfulOrdinal - 1
		if index >= 0 && index < len(assets) {
			return string(assets[index].ID), true
		}
		return nil, false
	case "provider-record-status":
		return record.Status, true
	case "provider-record-downloadable":
		return record.Downloadable, true
	case "provider-record-source-name":
		if hasExternalAddress(record.SourceName) {
			return nil, false
		}
		return record.SourceName, true
	case "provider-record-error-message":
		return nil, false
	case "provider-record-invalid-reason":
		return nil, false
	case "legacy-download-url-by-successful-ordinal":
		index := record.SuccessfulOrdinal - 1
		if assetDownload == nil || index < 0 || index >= len(assets) {
			return nil, false
		}
		token := legacyAssetToken(task.ID, assets[index].ID)
		placeholder := "{" + assetDownload.TokenParameter + "}"
		return strings.ReplaceAll(
			assetDownload.Path,
			placeholder,
			token,
		), true
	default:
		return nil, false
	}
}

var publicExternalAddress = regexp.MustCompile(`(?i)(https?://|//[a-z0-9][a-z0-9.-]*\.[a-z]{2,}(?:[:/?#]|$))`)

func hasExternalAddress(value string) bool {
	for step := 0; step < 4; step++ {
		if publicExternalAddress.MatchString(value) {
			return true
		}
		decoded := html.UnescapeString(value)
		decoded = strings.ReplaceAll(decoded, `\/`, `/`)
		decoded = strings.ReplaceAll(decoded, `\u002f`, `/`)
		decoded = strings.ReplaceAll(decoded, `\u002F`, `/`)
		decoded = strings.ReplaceAll(decoded, `\u003a`, `:`)
		decoded = strings.ReplaceAll(decoded, `\u003A`, `:`)
		if unescaped, err := url.QueryUnescape(decoded); err == nil {
			decoded = unescaped
		}
		if decoded == value {
			return false
		}
		value = decoded
	}
	return publicExternalAddress.MatchString(value)
}

func redactExternalAddresses(value any) any {
	switch current := value.(type) {
	case string:
		if hasExternalAddress(current) {
			return ""
		}
		return current
	case []any:
		for index, item := range current {
			current[index] = redactExternalAddresses(item)
		}
		return current
	case map[string]any:
		for key, item := range current {
			if hasExternalAddress(key) {
				delete(current, key)
				continue
			}
			current[key] = redactExternalAddresses(item)
		}
		return current
	default:
		return value
	}
}

func legacyRecordFieldValue(
	field catalog.LegacyRecordFieldSchema,
	task generation.Task,
	asset generation.Asset,
	assetDownload *catalog.LegacyAssetDownloadSchema,
	taskAssetRoute routing.Two,
) (any, bool) {
	if len(field.Literal) > 0 {
		var value any
		if err := json.Unmarshal(field.Literal, &value); err != nil {
			return nil, false
		}
		return value, true
	}
	token := legacyAssetToken(task.ID, asset.ID)
	switch field.Source {
	case "asset-token":
		return token, true
	case "asset-url":
		return nativeTaskAssetURL(
			taskAssetRoute,
			task.ID,
			asset.ID,
		), true
	case "asset-id":
		return string(asset.ID), true
	case "asset-role":
		return asset.Role, true
	case "asset-variant":
		return asset.Variant, true
	case "asset-media-type":
		return asset.MediaType, true
	case "asset-ordinal":
		return asset.Ordinal, true
	case "legacy-download-url":
		if assetDownload == nil {
			return nil, false
		}
		placeholder := "{" + assetDownload.TokenParameter + "}"
		return strings.ReplaceAll(
			assetDownload.Path,
			placeholder,
			token,
		), true
	default:
		return nil, false
	}
}

func legacyAssetToken(
	taskID generation.TaskID,
	assetID generation.AssetID,
) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(string(taskID) + "\x00" + string(assetID)),
	)
}

func parseLegacyAssetToken(
	value string,
) (generation.TaskID, generation.AssetID, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", "", false
	}
	segments := strings.SplitN(string(decoded), "\x00", 2)
	if len(segments) != 2 ||
		strings.TrimSpace(segments[0]) == "" ||
		strings.TrimSpace(segments[1]) == "" {
		return "", "", false
	}
	return generation.TaskID(segments[0]), generation.AssetID(segments[1]), true
}

func legacyProjectionMatchesRole(
	asset generation.Asset,
	role string,
) bool {
	return role == "*" || asset.Role == role
}

// projectLegacyResultAssets 是旧任务结果投影模块的唯一接口。
// capability Schema 决定容器、字段以及资产匹配规则；这里不识别任何
// capability 或 Provider 名称。
func projectLegacyResultAssets(
	response map[string]any,
	task generation.Task,
	schema catalog.LegacyTaskAPISchema,
	taskAssetRoute routing.Two,
) {
	if schema.ResultField == "" ||
		(len(schema.ResultAssets) == 0 &&
			len(schema.ResultCollections) == 0) {
		return
	}
	result := make(
		map[string]any,
		len(schema.ResultAssets)+len(schema.ResultCollections),
	)
	used := make(
		map[generation.AssetID]struct{},
		len(schema.ResultAssets)+len(schema.ResultCollections),
	)
	for _, projection := range schema.ResultAssets {
		if !catalog.LegacyOfferingApplies(projection.OfferingIDs, task.Offering.ID) {
			continue
		}
		if variant, exists := projection.OfferingVariants[task.Offering.ID]; exists {
			projection.Variant = variant
			projection.MediaTypes = nil // Explicit Offering variants never infer by MIME.
		}
		asset, exists := selectLegacyResultAsset(
			task.Assets,
			projection,
			used,
		)
		if !exists {
			continue
		}
		used[asset.ID] = struct{}{}
		result[projection.Name] = nativeTaskAssetURL(
			taskAssetRoute,
			task.ID,
			asset.ID,
		)
	}
	for _, projection := range schema.ResultCollections {
		if !catalog.LegacyOfferingApplies(projection.OfferingIDs, task.Offering.ID) {
			continue
		}
		collection := map[string]string{}
		for _, asset := range task.Assets {
			if _, claimed := used[asset.ID]; claimed ||
				asset.Role != projection.Role ||
				!strings.HasPrefix(
					asset.Variant,
					projection.VariantPrefix,
				) {
				continue
			}
			name := strings.TrimPrefix(
				asset.Variant,
				projection.VariantPrefix,
			)
			if strings.TrimSpace(name) == "" {
				continue
			}
			used[asset.ID] = struct{}{}
			collection[name] = nativeTaskAssetURL(
				taskAssetRoute,
				task.ID,
				asset.ID,
			)
		}
		if len(collection) > 0 {
			result[projection.Name] = collection
		}
	}
	response[schema.ResultField] = result
}

func selectLegacyResultAsset(
	assets []generation.Asset,
	projection catalog.LegacyResultAssetSchema,
	used map[generation.AssetID]struct{},
) (generation.Asset, bool) {
	if projection.Variant != "" {
		for _, asset := range assets {
			if _, claimed := used[asset.ID]; claimed {
				continue
			}
			if asset.Role == projection.Role &&
				asset.Variant == projection.Variant {
				return asset, true
			}
		}
	}
	for _, asset := range assets {
		if _, claimed := used[asset.ID]; claimed {
			continue
		}
		// 带有明确 Variant 的资产只能按 Variant 投影，避免未知语义仅因
		// MIME 相同而落入错误字段。MIME 回退仅服务旧数据和通用 mock。
		if asset.Role != projection.Role || asset.Variant != "" {
			continue
		}
		for _, mediaType := range projection.MediaTypes {
			if asset.MediaType == mediaType {
				return asset, true
			}
		}
	}
	return generation.Asset{}, false
}

func nativeTaskAssetURL(
	taskAssetRoute routing.Two,
	taskID generation.TaskID,
	assetID generation.AssetID,
) string {
	return taskAssetRoute.Render(string(taskID), string(assetID))
}
